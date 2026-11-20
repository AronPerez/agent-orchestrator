package decisionsync

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ServerDeps configures the serving side of the exchange.
type ServerDeps struct {
	Ledger       Ledger
	HostID       string
	MachineLabel func() string
	Now          func() time.Time
	// OnApplied runs after a pushed page stored at least one entry.
	OnApplied func(peerHostID string, result domain.DecisionApplyResult)
	// PageEntries caps the entries served per page; 0 means the protocol
	// maximum. Tests use small pages.
	PageEntries int
	// Peers, when set, records each peer that exchanges with this machine.
	Peers InboundRecorder
	// StatusSource, when set, backs Status.
	StatusSource StatusSource
	// After is the wait timer; tests replace it. Defaults to time.After.
	After func(time.Duration) <-chan time.Time
	Log   *slog.Logger
}

// InboundRecorder records a peer that initiates exchanges with this machine.
type InboundRecorder interface {
	RecordInboundDecisionSyncPeer(ctx context.Context, hostID, label string, protocol int, at time.Time) error
}

// MaxWaitSeconds caps how long an exchange may wait for new entries. It stays
// under common tunnel and proxy idle cuts.
const MaxWaitSeconds = 25

// inboundRecordInterval limits how often a busy peer's row is rewritten, so a
// waiting exchange every 25 seconds does not churn the status table.
const inboundRecordInterval = time.Minute

// Server answers exchanges and pushes from linked peers. The caller has
// already authenticated the peer and passes its host id.
type Server struct {
	deps ServerDeps

	mu       sync.Mutex
	changed  chan struct{}
	recorded map[string]inboundMark

	stopOnce sync.Once
	stop     chan struct{}
}

type inboundMark struct {
	label string
	at    time.Time
}

// NewServer returns a Server over deps.
func NewServer(deps ServerDeps) *Server {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.MachineLabel == nil {
		deps.MachineLabel = func() string { return "" }
	}
	deps.PageEntries = pageEntries(deps.PageEntries)
	if deps.After == nil {
		deps.After = time.After
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Server{deps: deps, changed: make(chan struct{}), recorded: map[string]inboundMark{}, stop: make(chan struct{})}
}

// Stop ends every waiting exchange, which then answers with what it has. Call
// it when the daemon shuts down.
func (s *Server) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// Changed wakes every exchange waiting for new entries.
func (s *Server) Changed() {
	s.mu.Lock()
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

// changes returns a channel closed by the next Changed.
func (s *Server) changes() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
}

// recordInbound notes the peer in the status table, at most once a minute
// unless its label changed. A failure is logged: it never fails the exchange.
func (s *Server) recordInbound(ctx context.Context, peerHostID, label string, protocol int) {
	if s.deps.Peers == nil {
		return
	}
	now := s.deps.Now()
	s.mu.Lock()
	last, seen := s.recorded[peerHostID]
	due := !seen || last.label != label || now.Sub(last.at) >= inboundRecordInterval
	if due {
		s.recorded[peerHostID] = inboundMark{label: label, at: now}
	}
	s.mu.Unlock()
	if !due {
		return
	}
	if err := s.deps.Peers.RecordInboundDecisionSyncPeer(ctx, peerHostID, label, protocol, now); err != nil {
		s.deps.Log.Warn("decision sync: record inbound peer", "peer", peerHostID, "err", err)
	}
}

func (s *Server) checkPeer(peerHostID, bodyHostID string) error {
	if peerHostID == s.deps.HostID {
		return ErrSameIdentity
	}
	if bodyHostID != peerHostID {
		return ErrPeerIdentityMismatch
	}
	return nil
}

// OriginIDs returns which entry this ledger holds at each sequence of an
// origin, a page at a time. It serves a quarantined origin too: ids carry no
// payload, and they are how a repairing machine finds where it forked.
func (s *Server) OriginIDs(ctx context.Context, peerHostID string, req OriginIDsRequest) (OriginIDsResponse, error) {
	if err := s.checkPeer(peerHostID, req.HostID); err != nil {
		return OriginIDsResponse{}, err
	}
	if req.Origin == "" || req.From < 1 {
		return OriginIDsResponse{}, ErrOriginIDsInvalid
	}
	limit := min(max(req.Limit, 1), MaxOriginIDsPage)
	ids, err := s.deps.Ledger.DecisionOriginIDs(ctx, req.Origin, req.From, limit+1)
	if err != nil {
		return OriginIDsResponse{}, err
	}
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	return OriginIDsResponse{HostID: s.deps.HostID, IDs: ids, More: more}, nil
}

// Exchange returns one page of the entries the peer lacks and this ledger's
// position.
func (s *Server) Exchange(ctx context.Context, peerHostID string, req ExchangeRequest) (ExchangeResponse, error) {
	if err := s.checkPeer(peerHostID, req.HostID); err != nil {
		return ExchangeResponse{}, err
	}
	s.recordInbound(ctx, peerHostID, req.MachineLabel, req.Protocol)
	wait := min(max(req.WaitSeconds, 0), MaxWaitSeconds)
	var timeout <-chan time.Time
	if wait > 0 {
		timeout = s.deps.After(time.Duration(wait) * time.Second)
	}
	for {
		// Take the change signal before reading, so an entry stored between
		// the read and the wait still wakes this exchange.
		changed := s.changes()
		resp, err := s.page(ctx, peerHostID, req)
		if err != nil || wait == 0 || len(resp.Entries) > 0 {
			return resp, err
		}
		select {
		case <-changed:
		case <-timeout:
			return resp, nil
		case <-s.stop:
			return resp, nil
		case <-ctx.Done():
			return ExchangeResponse{}, ctx.Err()
		}
	}
}

// page reads one page for the peer, then compares the peer's heads with this
// ledger. The order matters: an origin is paged from have[o]+1 only when this
// ledger already holds have[o], and stored entries never change, so comparing
// after the read checks exactly the entry the page builds on, even if this
// machine appended more in between. An origin found forked is dropped from
// the page before it is sent.
func (s *Server) page(ctx context.Context, peerHostID string, req ExchangeRequest) (ExchangeResponse, error) {
	entries, more, err := s.deps.Ledger.ListDecisionEntriesAfter(ctx, req.Have, s.deps.PageEntries, domain.MaxDecisionSyncPageBytes)
	if err != nil {
		return ExchangeResponse{}, fmt.Errorf("read sync page: %w", err)
	}
	if _, err := s.deps.Ledger.CheckDecisionHeads(ctx, peerHostID, req.Heads, s.deps.Now()); err != nil {
		return ExchangeResponse{}, fmt.Errorf("check peer heads: %w", err)
	}
	heads, err := s.deps.Ledger.DecisionHeads(ctx)
	if err != nil {
		return ExchangeResponse{}, fmt.Errorf("read heads: %w", err)
	}
	entries = withoutStopped(entries, heads)
	return ExchangeResponse{
		Protocol: domain.DecisionSyncProtocol, HostID: s.deps.HostID, MachineLabel: s.deps.MachineLabel(),
		Entries: entries, More: more, Have: heads.Vector(), Heads: heads, ServerTime: s.deps.Now(),
	}, nil
}

// withoutStopped drops entries of origins the heads mark as stopped.
func withoutStopped(entries []domain.DecisionEntry, heads domain.DecisionHeads) []domain.DecisionEntry {
	kept := entries[:0:0]
	for _, e := range entries {
		if !heads[e.Origin].Stopped {
			kept = append(kept, e)
		}
	}
	return kept
}

// Push applies one page of entries from the peer and returns this ledger's
// position afterwards.
func (s *Server) Push(ctx context.Context, peerHostID string, req PushRequest) (PushResponse, error) {
	if err := s.checkPeer(peerHostID, req.HostID); err != nil {
		return PushResponse{}, err
	}
	if err := checkPageSize(req.Entries); err != nil {
		return PushResponse{}, err
	}
	now := s.deps.Now()
	if _, err := s.deps.Ledger.CheckDecisionHeads(ctx, peerHostID, req.Heads, now); err != nil {
		return PushResponse{}, fmt.Errorf("check peer heads: %w", err)
	}
	var result domain.DecisionApplyResult
	if len(req.Entries) > 0 {
		var err error
		result, err = s.deps.Ledger.ApplyDecisionEntries(ctx, peerHostID, req.Entries, now)
		if err != nil {
			return PushResponse{}, err
		}
		if result.Applied > 0 && s.deps.OnApplied != nil {
			s.deps.OnApplied(peerHostID, result)
		}
	}
	heads, err := s.deps.Ledger.DecisionHeads(ctx)
	if err != nil {
		return PushResponse{}, fmt.Errorf("read heads: %w", err)
	}
	return PushResponse{Result: result, Have: heads.Vector(), Heads: heads}, nil
}

func checkPageSize(entries []domain.DecisionEntry) error {
	if len(entries) > domain.MaxDecisionSyncPageEntries {
		return fmt.Errorf("%w: %d entries, over %d", ErrPageTooLarge, len(entries), domain.MaxDecisionSyncPageEntries)
	}
	total := 0
	for _, e := range entries {
		total += len(e.Payload)
	}
	// One entry may exceed the byte cap on its own; only multi-entry pages
	// are held to it, matching the page reader.
	if len(entries) > 1 && total > domain.MaxDecisionSyncPageBytes {
		return fmt.Errorf("%w: %d payload bytes, over %d", ErrPageTooLarge, total, domain.MaxDecisionSyncPageBytes)
	}
	return nil
}

// skipStopped returns have with every origin the heads mark as stopped set
// to math.MaxInt64, so a page reader sends nothing for it.
func skipStopped(have domain.DecisionVector, heads domain.DecisionHeads) domain.DecisionVector {
	out := have.Clone()
	for _, origin := range heads.Stopped() {
		out[origin] = math.MaxInt64
	}
	return out
}

func pageEntries(n int) int {
	if n <= 0 || n > domain.MaxDecisionSyncPageEntries {
		return domain.MaxDecisionSyncPageEntries
	}
	return n
}

// Status returns this machine's sync freshness.
func (s *Server) Status(ctx context.Context) (Status, error) {
	if s.deps.StatusSource == nil {
		return Status{HostID: s.deps.HostID, MachineLabel: s.deps.MachineLabel()}, nil
	}
	return ReadStatus(ctx, s.deps.StatusSource, s.deps.HostID, s.deps.MachineLabel())
}
