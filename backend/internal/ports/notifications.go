package ports

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// NotificationIntent is the lifecycle-to-notification-producer contract. It is
// not an HTTP DTO; lifecycle fills it from facts it already has after the
// underlying session/PR state write succeeds.
type NotificationIntent struct {
	Type      domain.NotificationType
	SessionID domain.SessionID
	ProjectID domain.ProjectID
	PRURL     string
	CreatedAt time.Time
	// SourceKey makes one terminal fact idempotent across retries and restarts.
	SourceKey string

	// Enrichment hints. These avoid storage reads on the hot path.
	SessionDisplayName string
	PRNumber           int
	PRTitle            string
	PRSourceBranch     string
	PRTargetBranch     string
	Provider           string
	Repo               string

	// Decision identity and recorded labels for decision_needed. The labels
	// come from the call's immutable raised entry, so a remote call needs no
	// local session or project lookup. Context, options, and notes are never
	// part of this contract: only the title may reach a lock screen.
	DecisionCallID       string
	DecisionOriginHostID string
	DecisionProjectName  string
	DecisionStreamName   string
	DecisionTitle        string

	// InitiallyRead stores a new row as already read and announces nothing:
	// no live "created" event, so no OS toast and no unread badge. It is for
	// decisions this machine learns of by sync long after they were raised.
	InitiallyRead bool
}

// NotificationResolution is the lifecycle-to-notification-producer contract for
// closing notifications whose underlying issue went away. Lifecycle fills it
// after the durable fact that resolves the issue is written — the session left
// the needs-input family, the PR stopped waiting on a merge.
//
// DecisionCallID targets one decision call and is exclusive: it cannot be
// combined with a session or PR. Otherwise PRURL wins when set (a PR-scoped
// notification can outlive its session's activity churn), and the resolution
// is scoped to SessionID.
type NotificationResolution struct {
	Type           domain.NotificationType
	SessionID      domain.SessionID
	PRURL          string
	DecisionCallID string
	ResolvedAt     time.Time
}
