package domain

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TrackerProvider identifies an issue-tracker provider implementation.
type TrackerProvider string

// TrackerProviderGitHub and TrackerProviderGitLab are the supported issue-tracker
// providers.
const (
	TrackerProviderGitHub TrackerProvider = "github"
	TrackerProviderGitLab TrackerProvider = "gitlab"
)

// TrackerID identifies one issue. Native is the provider's own canonical form
// ("owner/repo#123" for GitHub, "group/project#123" for GitLab) and is
// parsed by the adapter.
//
// Host is the GitLab instance host (e.g. "gitlab.example.com"). The zero value
// "" means the default host gitlab.com, so all existing call sites that
// construct TrackerID without setting Host continue to work unchanged.
type TrackerID struct {
	Provider TrackerProvider `json:"provider"`
	Native   string          `json:"native"`
	// Host is the GitLab instance host; "" means gitlab.com.
	Host string `json:"host,omitempty"`
}

// NormalizedIssueState is the cross-provider issue-state vocabulary every
// adapter must implement. The closed list is intentional — adding a value
// here is a port-level decision because every adapter must map it.
type NormalizedIssueState string

// The normalized cross-provider issue states.
const (
	IssueOpen       NormalizedIssueState = "open"
	IssueInProgress NormalizedIssueState = "in_progress"
	IssueInReview   NormalizedIssueState = "review"
	IssueDone       NormalizedIssueState = "done"
	IssueCancelled  NormalizedIssueState = "cancelled"
)

// Issue is the minimum projection every tracker can produce. Provider-specific
// metadata stays inside provider-specific code paths.
type Issue struct {
	ID        TrackerID            `json:"id"`
	Title     string               `json:"title"`
	Body      string               `json:"body"`
	State     NormalizedIssueState `json:"state"`
	URL       string               `json:"url"`
	Labels    []string             `json:"labels,omitempty"`
	Assignees []string             `json:"assignees,omitempty"`
}

// TrackerRepo identifies a repository for cross-issue queries like Tracker.List.
// Native is the provider's canonical owner/project form, e.g. "owner/repo"
// for GitHub or "group/project" for GitLab.
//
// Host is the GitLab instance host (e.g. "gitlab.example.com"). The zero value
// "" means the default host gitlab.com, so all existing call sites that
// construct TrackerRepo without setting Host continue to work unchanged.
type TrackerRepo struct {
	Provider TrackerProvider `json:"provider"`
	Native   string          `json:"native"`
	// Host is the GitLab instance host; "" means gitlab.com.
	Host string `json:"host,omitempty"`
}

// ListStateFilter narrows Tracker.List results by the provider's coarse
// state (open vs closed). It is intentionally NOT the 5-value normalized
// enum — finer filtering (e.g. "only in-review issues") goes through the
// Labels field of ListFilter.
type ListStateFilter string

// Coarse list-state filters for Tracker.List.
const (
	// ListAll is the zero value and returns issues in any state.
	ListAll    ListStateFilter = ""
	ListOpen   ListStateFilter = "open"
	ListClosed ListStateFilter = "closed"
)

// ListFilter is the query the Session Manager passes to Tracker.List.
// Empty / zero values mean "no filter on this dimension".
//
// Limit is an optional total-result cap. Adapters choose their own provider
// page size.
type ListFilter struct {
	State    ListStateFilter `json:"state,omitempty"`
	Labels   []string        `json:"labels,omitempty"`
	Assignee string          `json:"assignee,omitempty"`
	Limit    int             `json:"limit,omitempty"`
}

// TrackerIntakeConfig controls issue-driven worker spawning for a project.
// Enabled requires an explicit assignee eligibility rule so turning intake on
// cannot accidentally drain an entire issue backlog.
type TrackerIntakeConfig struct {
	Enabled bool `json:"enabled,omitempty"`
	// Provider defaults to github when Enabled is true. Supported values:
	// "github" and "gitlab".
	Provider TrackerProvider `json:"provider,omitempty" enum:"github,gitlab"`
	// Repo is the provider-native repository key ("owner/repo" for GitHub,
	// "group/project" for GitLab). When empty, the intake loop derives it from
	// the project's repo origin URL.
	Repo string `json:"repo,omitempty"`
	// Assignee narrows eligible issues to one assignee. Provider-specific values
	// such as "*" are passed through unchanged.
	Assignee string `json:"assignee,omitempty"`
	// Limits bounds unattended session starts. Omitted fields use safe defaults;
	// zero never means unlimited.
	Limits AutomatedSessionLimits `json:"limits,omitempty"`
}

// WithDefaults leaves the provider empty when not explicitly set so the
// caller can infer it from the project's repo origin URL at use time (see
// InferTrackerProvider). Disabled intake leaves the zero value untouched so
// empty project configs still store as NULL.
func (c TrackerIntakeConfig) WithDefaults() TrackerIntakeConfig {
	if c.Enabled {
		c.Limits = c.Limits.WithDefaults()
	}
	return c
}

// InferTrackerProvider guesses the tracker provider from a repo origin URL.
// GitHub hosts (github.com, *.github.com, *.ghe.io) map to github; any other
// host maps to gitlab (self-managed GitLab uses arbitrary hostnames). The
// empty-string fallback is github for backward compatibility.
func InferTrackerProvider(repoURL string) TrackerProvider {
	raw := strings.TrimSpace(repoURL)
	if raw == "" {
		return TrackerProviderGitHub
	}
	// SSH form git@host:path → normalise to https://host/path
	if strings.HasPrefix(raw, "git@") {
		if _, rest, ok := strings.Cut(raw, "@"); ok {
			if host, path, ok := strings.Cut(rest, ":"); ok {
				raw = "https://" + host + "/" + path
			}
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return TrackerProviderGitHub
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if host == "github.com" || strings.HasSuffix(host, ".github.com") || strings.HasSuffix(host, ".ghe.io") {
		return TrackerProviderGitHub
	}
	return TrackerProviderGitLab
}

// Validate rejects accidental broad intake and unknown providers. An empty
// provider is accepted here and inferred from the repo URL at use time.
func (c TrackerIntakeConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Provider != "" && c.Provider != TrackerProviderGitHub && c.Provider != TrackerProviderGitLab {
		return fmt.Errorf("trackerIntake.provider: unsupported provider %q", c.Provider)
	}
	if err := validateNoWhitespaceField("trackerIntake.repo", c.Repo); err != nil {
		return err
	}
	if err := validateNoWhitespaceField("trackerIntake.assignee", c.Assignee); err != nil {
		return err
	}
	if strings.TrimSpace(c.Assignee) == "" {
		return fmt.Errorf("trackerIntake: assignee is required when enabled")
	}
	if err := c.Limits.Validate(); err != nil {
		return fmt.Errorf("trackerIntake.limits: %w", err)
	}
	return nil
}

// TrackerIntakeHandledRecord is durable proof that one project delivered a
// tracker item. It survives session termination and deletion so an open issue
// is not re-spawned forever. IssueID includes the provider and native identity.
type TrackerIntakeHandledRecord struct {
	ProjectID                    ProjectID
	IssueID                      IssueID
	SessionID                    SessionID
	HandledAt                    time.Time
	CapacityReleased             bool
	ReleasedRuntimeLaunchID      string
	ReleasedControllerGeneration string
}

// TrackerIntakeRecoveryReservation is the durable fence written before an
// unattended launch can create an external controller. It keeps the workspace
// and every opaque runtime identity needed to settle an interrupted launch
// without starting a second worker for the same tracker item.
type TrackerIntakeRecoveryReservation struct {
	Branch            string `json:"branch,omitempty"`
	WorkspacePath     string `json:"workspacePath,omitempty"`
	WorkspaceRepoPath string `json:"workspaceRepoPath,omitempty"`
	// WorkspaceCreationIntent records that WorkspacePath was checkpointed before
	// materialization. Recovery may remove it only when the workspace adapter can
	// prove the path is a registered worktree or is empty/absent.
	WorkspaceCreationIntent bool     `json:"workspaceCreationIntent,omitempty"`
	RuntimeLaunchID         string   `json:"runtimeLaunchId,omitempty"`
	RuntimeHandleIDs        []string `json:"runtimeHandleIds,omitempty"`
	ControllerPossible      bool     `json:"controllerPossible,omitempty"`
	// ControllerGeneration fences a Chat controller reserved by a user resume.
	// A matching session row proves that controller reached durable publication;
	// recovery must not stop it as residue from the original failed intake.
	ControllerGeneration string `json:"controllerGeneration,omitempty"`
	// RelaunchPublished distinguishes a fully completed user restore/resume from
	// an initial launch that merely published its runtime before after-start
	// prompt delivery. Recovery preserves the former live owner without claiming
	// that the tracker task itself crossed the delivery boundary.
	RelaunchPublished bool `json:"relaunchPublished,omitempty"`
	// DeliveryPossible means the initial task may already have crossed the
	// runtime/provider boundary. Recovery must preserve the worker and claim;
	// cleaning it up and retrying could duplicate arbitrary external effects.
	DeliveryPossible bool `json:"deliveryPossible,omitempty"`
	// CapacityReleased means lifecycle has proved that no runtime or controller
	// remains active for the released generation below. DeliveryPossible still
	// prevents replay, while this flag lets unrelated issues use the freed
	// project slot during workspace cleanup. A later generation counts again.
	CapacityReleased bool `json:"capacityReleased,omitempty"`
	// ReleasedRuntimeLaunchID, ReleasedRuntimeHandleIDs, and
	// ReleasedControllerGeneration identify the exact owner generation and
	// terminal handles proved stopped when CapacityReleased was recorded.
	ReleasedRuntimeLaunchID      string   `json:"releasedRuntimeLaunchId,omitempty"`
	ReleasedRuntimeHandleIDs     []string `json:"releasedRuntimeHandleIds,omitempty"`
	ReleasedControllerGeneration string   `json:"releasedControllerGeneration,omitempty"`
	CleanupConfirmed             bool     `json:"cleanupConfirmed,omitempty"`
}

// ReleasesCapacityFor reports whether the stopped-generation proof still
// applies to this session row. Resuming either controller rotates its identity,
// so a restored worker consumes capacity again without discarding the issue's
// no-replay fence.
func (r TrackerIntakeRecoveryReservation) ReleasesCapacityFor(rec SessionRecord) bool {
	return r.CapacityReleased &&
		strings.TrimSpace(r.ReleasedRuntimeLaunchID) == strings.TrimSpace(rec.Metadata.RuntimeLaunchID) &&
		strings.TrimSpace(r.ReleasedControllerGeneration) == strings.TrimSpace(rec.Metadata.ControllerGeneration)
}

// ReleasesCapacityFor reports whether this handled record's stopped-generation
// proof still applies to the supplied session row.
func (r TrackerIntakeHandledRecord) ReleasesCapacityFor(rec SessionRecord) bool {
	return TrackerIntakeRecoveryReservation{
		CapacityReleased:             r.CapacityReleased,
		ReleasedRuntimeLaunchID:      r.ReleasedRuntimeLaunchID,
		ReleasedControllerGeneration: r.ReleasedControllerGeneration,
	}.ReleasesCapacityFor(rec)
}

// TrackerIntakePendingClaim is the durable recovery worklist. It deliberately
// survives a missing or terminated session row so lifecycle recovery is driven
// by ownership facts rather than by the session list.
type TrackerIntakePendingClaim struct {
	ProjectID  ProjectID
	IssueID    IssueID
	SessionID  SessionID
	Recovery   TrackerIntakeRecoveryReservation
	RecordedAt time.Time
}
