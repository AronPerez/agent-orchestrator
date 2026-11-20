// Package decisionsync replicates the decision ledger between linked machines.
//
// The exchange is transport-agnostic: an initiating Engine talks to one peer
// through the Remote interface, and the peer answers through its Server. An
// exchange moves entries in both directions and relays every origin, so a
// machine nothing can reach still takes full part by initiating, and linking
// every machine to one always-on machine is enough for all to converge.
package decisionsync

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Ledger is the decision log as replication sees it. The SQLite store and the
// in-memory test ledger (ledgertest) both implement it, and the conformance
// suite in ledgertest holds them to the same behavior.
type Ledger interface {
	// DecisionVector returns the highest sequence held for every origin.
	DecisionVector(context.Context) (domain.DecisionVector, error)
	// DecisionHeads returns the newest entry held for every origin and marks
	// the origins this ledger stopped after recording a conflict.
	DecisionHeads(context.Context) (domain.DecisionHeads, error)
	// ListDecisionEntriesAfter returns one page of entries a holder of the
	// given vector lacks, and whether more are due.
	ListDecisionEntriesAfter(context.Context, domain.DecisionVector, int, int) ([]domain.DecisionEntry, bool, error)
	// ApplyDecisionEntries stores received entries verbatim under the sync
	// apply rules and recomputes the affected calls.
	ApplyDecisionEntries(context.Context, string, []domain.DecisionEntry, time.Time) (domain.DecisionApplyResult, error)
	// CheckDecisionHeads records a conflict for every peer head this ledger
	// holds under another entry id.
	CheckDecisionHeads(context.Context, string, domain.DecisionHeads, time.Time) ([]domain.DecisionSyncConflict, error)
}

// ExchangeRequest asks a peer for the entries the initiator lacks.
type ExchangeRequest struct {
	Protocol     int
	HostID       string
	MachineLabel string
	// Have is the initiator's vector. Origins the initiator stopped are sent
	// as math.MaxInt64 so the peer does not page them.
	Have domain.DecisionVector
	// Heads lets the peer detect a forked origin the vector cannot show.
	Heads domain.DecisionHeads
	// WaitSeconds, when positive, asks the peer to hold the request until it
	// has something to send or the wait ends (bounded, see Server).
	WaitSeconds int
}

// ExchangeResponse is one page of entries the initiator lacks plus the peer's
// own position.
type ExchangeResponse struct {
	Protocol     int
	HostID       string
	MachineLabel string
	Entries      []domain.DecisionEntry
	More         bool
	Have         domain.DecisionVector
	Heads        domain.DecisionHeads
	ServerTime   time.Time
}

// PushRequest sends the peer one page of entries it lacks.
type PushRequest struct {
	Protocol int
	HostID   string
	Entries  []domain.DecisionEntry
	Heads    domain.DecisionHeads
}

// PushResponse is what applying a pushed page did on the peer, and the peer's
// position afterwards.
type PushResponse struct {
	Result domain.DecisionApplyResult
	Have   domain.DecisionVector
	Heads  domain.DecisionHeads
}

// Remote is one peer as the initiator sees it. The HTTP client and the
// in-process test remote both implement it.
type Remote interface {
	Exchange(context.Context, ExchangeRequest) (ExchangeResponse, error)
	Push(context.Context, PushRequest) (PushResponse, error)
}

// Errors returned by the server and the engine.
var (
	// ErrPeerIdentityChanged reports a peer answering with another host id
	// than the one it was linked as.
	ErrPeerIdentityChanged = errors.New("decision sync peer identity changed")
	// ErrPeerIdentityMismatch reports a request whose body names another host
	// than its credential was issued to.
	ErrPeerIdentityMismatch = errors.New("decision sync host id does not match its credential")
	// ErrSameIdentity reports a peer claiming this machine's own host id, as a
	// copied data directory would.
	ErrSameIdentity = errors.New("decision sync peer shares this machine's identity")
	// ErrPageTooLarge reports a pushed page over the page limits.
	ErrPageTooLarge = errors.New("decision sync page too large")
)

// ClockAheadThreshold is how far ahead of this machine's clock a received
// stamp must be before the peer's status reports it.
const ClockAheadThreshold = 5 * time.Minute
