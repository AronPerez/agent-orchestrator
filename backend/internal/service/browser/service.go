// Package browser owns authorization and dispatch for session-scoped browser
// commands. HTTP controllers remain transport-only adapters.
package browser

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

var actions = map[string]struct{}{
	"open": {}, "snapshot": {}, "click": {}, "dblclick": {}, "focus": {}, "fill": {}, "type": {}, "press": {},
	"hover": {}, "highlight": {}, "unhighlight": {}, "scrollintoview": {}, "drag": {}, "tabs": {}, "tab-new": {},
	"tab-select": {}, "tab-close": {}, "scroll": {}, "select": {}, "check": {},
	"uncheck": {}, "get": {}, "wait": {}, "screenshot": {}, "network-start": {},
	"network-status": {}, "network-list": {}, "network-stop": {}, "network-clear": {},
	"console": {}, "errors": {}, "frame": {}, "dialog": {},
	"devtools-open": {}, "devtools-close": {},
}

type sessionReader interface {
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

type runtime interface {
	Status() browserruntime.Status
	Execute(
		ctx context.Context,
		sessionID domain.SessionID,
		action string,
		args map[string]interface{},
		profileKey string,
	) (browserruntime.Result, error)
}

// projectReader reads a project's config. Only the daemon knows a session's
// project and that project's settings, so only the daemon can decide whether a
// browser session gets a persistent profile.
type projectReader interface {
	Get(ctx context.Context, id domain.ProjectID) (project.GetResult, error)
}

// Service validates worker ownership and lifecycle state before dispatching to
// the Electron runtime.
type Service struct {
	sessions  sessionReader
	projects  projectReader
	runtime   runtime
	authority *Authority
}

// New creates a browser service. projects may be nil, in which case every
// session gets the default ephemeral profile — failing closed is the correct
// direction here, since the failure mode is "log in again", not "credentials
// shared more widely than intended".
func New(sessions sessionReader, projects projectReader, runtime runtime, authority *Authority) *Service {
	return &Service{sessions: sessions, projects: projects, runtime: runtime, authority: authority}
}

// StatusResult is transport state plus which profile mode this session's
// browser is running on. The mode is reported, not inferred by the caller: a
// user must never have to guess whether their logins are being kept.
type StatusResult struct {
	browserruntime.Status
	PersistentProfile bool
}

// Status returns transport state after validating the session owner.
func (s *Service) Status(ctx context.Context, sessionID domain.SessionID, capability string) (StatusResult, error) {
	session, err := s.authorize(ctx, sessionID, capability)
	if err != nil {
		return StatusResult{}, err
	}
	return StatusResult{
		Status:            s.runtime.Status(),
		PersistentProfile: s.profileKey(ctx, session) != "",
	}, nil
}

// Execute validates ownership and dispatches one supported action.
func (s *Service) Execute(
	ctx context.Context,
	sessionID domain.SessionID,
	capability string,
	action string,
	args map[string]interface{},
) (browserruntime.Result, string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	session, err := s.authorize(ctx, sessionID, capability)
	if err != nil {
		return browserruntime.Result{}, action, err
	}
	if _, ok := actions[action]; !ok {
		return browserruntime.Result{}, action, apierr.Invalid(
			"BROWSER_ACTION_UNSUPPORTED",
			"Unsupported browser action",
			nil,
		)
	}
	// Resolved on EVERY command, not just the first: ensureSession in the app is
	// reached agent-first, before any panel mounts, so the very first agent
	// command is what creates the partition. Deriving the key here removes the
	// renderer-timing race entirely — there is no window in which a session is
	// created without its project's answer.
	result, err := s.runtime.Execute(ctx, sessionID, action, args, s.profileKey(ctx, session))
	return result, action, err
}

func (s *Service) authorize(ctx context.Context, sessionID domain.SessionID, capability string) (domain.Session, error) {
	session, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	if session.IsTerminated {
		return domain.Session{}, apierr.Conflict("SESSION_TERMINATED", "Session is terminated", nil)
	}
	if s.authority == nil || !s.authority.Valid(
		sessionID,
		strings.TrimSpace(capability),
		session.Metadata.BrowserCapabilityVerifier,
	) {
		return domain.Session{}, apierr.Forbidden("BROWSER_CAPABILITY_INVALID", "Browser capability is invalid")
	}
	return session, nil
}

// profileKey returns the opaque partition key for this session's browser
// profile, or "" for the default throwaway one.
//
// It is keyed on the PROJECT, never on the session and never globally. Per
// session would not survive what was asked for (staying logged in across
// sessions); global would mean one injected agent anywhere reaches every
// credential on the machine. Project scope is the bounded middle, and it is what
// the opt-in describes.
//
// Every failure path returns "": an unreadable project, a degraded one, a
// missing config, or no project reader at all all mean "no opt-in found", and
// the safe answer to that is the isolated default.
func (s *Service) profileKey(ctx context.Context, session domain.Session) string {
	if s.projects == nil || session.ProjectID == "" {
		return ""
	}
	res, err := s.projects.Get(ctx, session.ProjectID)
	if err != nil || res.Project == nil || res.Project.Config == nil {
		return ""
	}
	if !res.Project.Config.BrowserPersistentProfile {
		return ""
	}
	return string(res.Project.ID)
}
