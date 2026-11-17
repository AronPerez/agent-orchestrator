// Package decision owns the decision ledger's actor and state rules.
package decision

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type Store interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	AppendDecisionEntry(context.Context, domain.DecisionEntry, time.Time) (domain.DecisionEntry, domain.DecisionCall, error)
	GetDecisionCall(context.Context, string) (domain.DecisionCall, bool, error)
	ListDecisionEntries(context.Context, string) ([]domain.DecisionEntry, error)
	ListDecisionCallsByOwner(context.Context, string, domain.SessionID, int) ([]domain.DecisionCall, error)
	ListDecisionCallsByState(context.Context, domain.DecisionCallState, int) ([]domain.DecisionCall, error)
	ListDecisionStreams(context.Context) ([]domain.DecisionStream, error)
	SearchDecisionCalls(context.Context, string, int) ([]domain.DecisionCall, error)
}

type Deps struct {
	Store        Store
	HostID       string
	MachineLabel func() string
	Now          func() time.Time
	NewID        func(prefix string) string
	OnAppended   func(domain.DecisionEntry)
}

type Service struct {
	store        Store
	hostID       string
	machineLabel func() string
	now          func() time.Time
	newID        func(string) string
	onAppended   func(domain.DecisionEntry)
}

func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewID == nil {
		d.NewID = func(prefix string) string { return prefix + uuid.NewString() }
	}
	if d.MachineLabel == nil {
		d.MachineLabel = func() string {
			label, err := os.Hostname()
			if err != nil || strings.TrimSpace(label) == "" {
				return d.HostID
			}
			return label
		}
	}
	return &Service{
		store: d.Store, hostID: d.HostID, machineLabel: d.MachineLabel,
		now: d.Now, newID: d.NewID, onAppended: d.OnAppended,
	}
}

// Actor is either a calling session or the person using one of AO's surfaces.
type Actor struct {
	Session domain.SessionID
	Surface domain.RequestSurface
}

type RaiseInput struct {
	Title     string
	Context   string
	Options   []domain.DecisionOption
	Links     []domain.DecisionLink
	Reference string
}

type Scope string

const (
	ScopeMine     Scope = "mine"
	ScopeNeedsYou Scope = "needs_you"
)

type Detail struct {
	Call    domain.DecisionCall
	Entries []domain.DecisionEntry
}

func (s *Service) Raise(ctx context.Context, actor Actor, input RaiseInput) (domain.DecisionCall, error) {
	if actor.Session == "" {
		return domain.DecisionCall{}, apierr.Invalid("DECISION_SESSION_REQUIRED", "This needs a session. Run it from inside a session.", nil)
	}
	if s.hostID == "" {
		return domain.DecisionCall{}, apierr.Unavailable("DECISION_HOST_UNKNOWN", "This machine has no identity yet, so it cannot record decisions.")
	}
	session, ok, err := s.store.GetSession(ctx, actor.Session)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	if !ok {
		return domain.DecisionCall{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}

	payload := domain.DecisionRaisedPayload{
		Title: input.Title, Context: input.Context, Options: input.Options, Links: input.Links,
		Reference: input.Reference, ProjectID: session.ProjectID,
		MachineLabel: s.machineLabel(), OwnerSessionID: session.ID, OwnerSessionName: sessionName(session),
	}
	if session.ProjectID != "" {
		project, found, err := s.store.GetProject(ctx, string(session.ProjectID))
		if err != nil {
			return domain.DecisionCall{}, err
		}
		payload.ProjectName = string(session.ProjectID)
		if found && project.DisplayName != "" {
			payload.ProjectName = project.DisplayName
		}
		sessions, err := s.store.ListSessions(ctx, session.ProjectID)
		if err != nil {
			return domain.DecisionCall{}, err
		}
		if stream, found := domain.DeriveDecisionStream(session.ID, sessions); found {
			payload.StreamSessionID = stream.ID
			payload.StreamName = sessionName(stream)
		}
	}
	if err := domain.ValidateDecisionRaise(payload); err != nil {
		return domain.DecisionCall{}, invalidDecision(err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	now := s.now().UTC()
	callID := s.newID("dec_")
	entry := domain.DecisionEntry{
		ID: s.newID("dent_"), Origin: s.hostID, CallID: callID,
		Kind: domain.DecisionKindRaised, Payload: raw, WrittenAt: now,
		AuthorKind: domain.DecisionAuthorSession, AuthorSession: session.ID,
	}
	stored, call, err := s.store.AppendDecisionEntry(ctx, entry, now)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	if s.onAppended != nil {
		s.onAppended(stored)
	}
	return call, nil
}

func (s *Service) Get(ctx context.Context, callID string) (Detail, error) {
	call, ok, err := s.store.GetDecisionCall(ctx, callID)
	if err != nil {
		return Detail{}, err
	}
	if !ok {
		return Detail{}, apierr.NotFound("DECISION_NOT_FOUND", "Unknown call")
	}
	entries, err := s.store.ListDecisionEntries(ctx, callID)
	if err != nil {
		return Detail{}, err
	}
	calls, err := s.withStreams(ctx, []domain.DecisionCall{call})
	if err != nil {
		return Detail{}, err
	}
	return Detail{Call: calls[0], Entries: entries}, nil
}

func (s *Service) List(ctx context.Context, actor Actor, scope Scope) ([]domain.DecisionCall, error) {
	var calls []domain.DecisionCall
	var err error
	switch scope {
	case ScopeMine:
		if actor.Session == "" {
			return nil, apierr.Invalid("DECISION_SESSION_REQUIRED", "This needs a session. Run it from inside a session.", nil)
		}
		calls, err = s.store.ListDecisionCallsByOwner(ctx, s.hostID, actor.Session, 50)
	case ScopeNeedsYou:
		calls, err = s.store.ListDecisionCallsByState(ctx, domain.DecisionStateNeedsYou, 200)
	default:
		return nil, apierr.Invalid("DECISION_SCOPE_INVALID", "Unknown scope.", nil)
	}
	if err != nil {
		return nil, err
	}
	return s.withStreams(ctx, calls)
}

func (s *Service) Search(ctx context.Context, actor Actor, query string) ([]domain.DecisionCall, error) {
	if strings.TrimSpace(query) == "" {
		return nil, apierr.Invalid("DECISION_QUERY_REQUIRED", "Say what to search for.", nil)
	}
	calls, err := s.store.SearchDecisionCalls(ctx, query, 20)
	if err != nil {
		return nil, err
	}
	calls, err = s.withStreams(ctx, calls)
	if err != nil {
		return nil, err
	}
	project, hasProject, err := s.actorProject(ctx, actor)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(calls, func(i, j int) bool {
		iSame := hasProject && calls[i].Raised.ProjectID == project
		jSame := hasProject && calls[j].Raised.ProjectID == project
		if iSame != jSame {
			return iSame
		}
		if calls[i].Retired != calls[j].Retired {
			return !calls[i].Retired
		}
		return false
	})
	return calls, nil
}

func (s *Service) withStreams(ctx context.Context, calls []domain.DecisionCall) ([]domain.DecisionCall, error) {
	streams, err := s.store.ListDecisionStreams(ctx)
	if err != nil {
		return nil, err
	}
	return domain.AssignDecisionStreams(calls, streams), nil
}

func (s *Service) actorProject(ctx context.Context, actor Actor) (domain.ProjectID, bool, error) {
	if actor.Session == "" {
		return "", false, nil
	}
	session, ok, err := s.store.GetSession(ctx, actor.Session)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return session.ProjectID, true, nil
}

func sessionName(session domain.SessionRecord) string {
	if session.DisplayName != "" {
		return session.DisplayName
	}
	return string(session.ID)
}

func invalidDecision(err error) error {
	var rule *domain.DecisionRuleError
	if errors.As(err, &rule) {
		return apierr.Invalid("DECISION_INVALID", rule.Message, map[string]any{"rule": rule.Rule})
	}
	return err
}
