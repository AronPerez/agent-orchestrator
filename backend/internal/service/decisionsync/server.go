package decisionsync

import (
	"context"
	"fmt"
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
}

// Server answers exchanges and pushes from linked peers. The caller has
// already authenticated the peer and passes its host id.
type Server struct {
	deps ServerDeps

	mu      sync.Mutex
	changed chan struct{}
}

// NewServer returns a Server over deps.
func NewServer(deps ServerDeps) *Server {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.MachineLabel == nil {
		deps.MachineLabel = func() string { return "" }
	}
	return &Server{deps: deps, changed: make(chan struct{})}
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

func (s *Server) checkPeer(peerHostID, bodyHostID string) error {
	if peerHostID == s.deps.HostID {
		return ErrSameIdentity
	}
	if bodyHostID != peerHostID {
		return ErrPeerIdentityMismatch
	}
	return nil
}

// Exchange returns one page of the entries the peer lacks and this ledger's
// position. It first compares the peer's heads with this ledger so a forked
// origin is recorded before anything else moves.
func (s *Server) Exchange(ctx context.Context, peerHostID string, req ExchangeRequest) (ExchangeResponse, error) {
	if err := s.checkPeer(peerHostID, req.HostID); err != nil {
		return ExchangeResponse{}, err
	}
	if _, err := s.deps.Ledger.CheckDecisionHeads(ctx, peerHostID, req.Heads, s.deps.Now()); err != nil {
		return ExchangeResponse{}, fmt.Errorf("check peer heads: %w", err)
	}
	return s.page(ctx, req.Have)
}

func (s *Server) page(ctx context.Context, have domain.DecisionVector) (ExchangeResponse, error) {
	entries, more, err := s.deps.Ledger.ListDecisionEntriesAfter(ctx, have, domain.MaxDecisionSyncPageEntries, domain.MaxDecisionSyncPageBytes)
	if err != nil {
		return ExchangeResponse{}, fmt.Errorf("read sync page: %w", err)
	}
	heads, err := s.deps.Ledger.DecisionHeads(ctx)
	if err != nil {
		return ExchangeResponse{}, fmt.Errorf("read heads: %w", err)
	}
	return ExchangeResponse{
		Protocol: domain.DecisionSyncProtocol, HostID: s.deps.HostID, MachineLabel: s.deps.MachineLabel(),
		Entries: entries, More: more, Have: heads.Vector(), Heads: heads, ServerTime: s.deps.Now(),
	}, nil
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
