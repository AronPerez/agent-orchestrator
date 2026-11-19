package decisionsync

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// EngineDeps configures the initiating side of the exchange.
type EngineDeps struct {
	Ledger       Ledger
	HostID       string
	MachineLabel func() string
	Now          func() time.Time
	// OnApplied runs after a pulled page stored at least one entry.
	OnApplied func(peerHostID string, result domain.DecisionApplyResult)
	// PageEntries caps the entries pushed per page; 0 means the protocol
	// maximum. Tests use small pages.
	PageEntries int
}

// Outcome is what one Sync did.
type Outcome struct {
	Pulled, Pushed int
	// InSync is true when both ledgers ended holding the same vector.
	InSync     bool
	PeerHostID string
	PeerLabel  string
	PeerVector domain.DecisionVector
	// ClockAhead is how far the newest received stamp was ahead of this
	// machine's clock, when that is over ClockAheadThreshold; otherwise 0.
	ClockAhead time.Duration
	Gaps       []domain.DecisionSyncGap
	Conflicts  []domain.DecisionSyncConflict
}

// Engine runs exchanges with peers on behalf of this machine.
type Engine struct {
	deps EngineDeps
}

// NewEngine returns an Engine over deps.
func NewEngine(deps EngineDeps) *Engine {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.MachineLabel == nil {
		deps.MachineLabel = func() string { return "" }
	}
	deps.PageEntries = pageEntries(deps.PageEntries)
	return &Engine{deps: deps}
}

// Sync pulls every page the peer has that this ledger lacks, then pushes every
// page this ledger has that the peer lacks. Pages already applied stay applied
// if the context ends or the peer fails part way; the next Sync resumes from
// the new vectors.
func (e *Engine) Sync(ctx context.Context, expectedPeer string, remote Remote) (Outcome, error) {
	out := Outcome{PeerHostID: expectedPeer}
	var newestWall int64
	peer, err := e.pull(ctx, expectedPeer, remote, &out, &newestWall)
	if err != nil {
		return out, err
	}
	peerHeads, err := e.push(ctx, remote, peer, &out)
	if err != nil {
		return out, err
	}
	mine, err := e.deps.Ledger.DecisionHeads(ctx)
	if err != nil {
		return out, err
	}
	// A stopped origin on either side means the two ledgers disagree about
	// that identity's history, whatever their vectors say.
	out.InSync = mine.Vector().Equal(out.PeerVector) && len(mine.Stopped()) == 0 && len(peerHeads.Stopped()) == 0
	if newestWall > 0 {
		if ahead := time.Duration(newestWall-e.deps.Now().UnixMilli()) * time.Millisecond; ahead > ClockAheadThreshold {
			out.ClockAhead = ahead
		}
	}
	return out, nil
}

// pull applies the peer's pages until it has nothing more, and returns its
// last response. A page with a gap gets one immediate re-pull from the new
// vector; a second gap ends the pull and stays in the outcome.
func (e *Engine) pull(ctx context.Context, peerID string, remote Remote, out *Outcome, newestWall *int64) (ExchangeResponse, error) {
	gaps := 0
	checkedHeads := false
	for {
		if err := ctx.Err(); err != nil {
			return ExchangeResponse{}, err
		}
		heads, err := e.deps.Ledger.DecisionHeads(ctx)
		if err != nil {
			return ExchangeResponse{}, err
		}
		resp, err := remote.Exchange(ctx, ExchangeRequest{
			Protocol: domain.DecisionSyncProtocol, HostID: e.deps.HostID, MachineLabel: e.deps.MachineLabel(),
			Have: skipStopped(heads.Vector(), heads), Heads: heads,
		})
		if err != nil {
			return ExchangeResponse{}, err
		}
		if resp.HostID != peerID {
			return ExchangeResponse{}, fmt.Errorf("%w: expected %s, got %s", ErrPeerIdentityChanged, peerID, resp.HostID)
		}
		out.PeerLabel = resp.MachineLabel
		out.PeerVector = resp.Have
		if !checkedHeads {
			checkedHeads = true
			conflicts, err := e.deps.Ledger.CheckDecisionHeads(ctx, peerID, resp.Heads, e.deps.Now())
			if err != nil {
				return ExchangeResponse{}, err
			}
			out.Conflicts = append(out.Conflicts, conflicts...)
		}
		var result domain.DecisionApplyResult
		if len(resp.Entries) > 0 {
			for _, entry := range resp.Entries {
				*newestWall = max(*newestWall, entry.Stamp.WallMS)
			}
			result, err = e.deps.Ledger.ApplyDecisionEntries(ctx, peerID, resp.Entries, e.deps.Now())
			if err != nil {
				return ExchangeResponse{}, err
			}
			out.Pulled += result.Applied
			out.Conflicts = append(out.Conflicts, result.Conflicts...)
			if result.Applied > 0 && e.deps.OnApplied != nil {
				e.deps.OnApplied(peerID, result)
			}
		}
		if len(result.Gaps) > 0 {
			gaps++
			if gaps > 1 {
				out.Gaps = append(out.Gaps, result.Gaps...)
				return resp, nil
			}
			continue
		}
		// A page that stored nothing new cannot be followed by a different
		// one; stop rather than ask for it again.
		if !resp.More || result.Applied == 0 {
			return resp, nil
		}
	}
}

// push sends pages the peer lacks until it holds everything this ledger
// serves, or a page makes no progress.
func (e *Engine) push(ctx context.Context, remote Remote, last ExchangeResponse, out *Outcome) (domain.DecisionHeads, error) {
	peerHave, peerHeads := last.Have, last.Heads
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entries, _, err := e.deps.Ledger.ListDecisionEntriesAfter(ctx, skipStopped(peerHave, peerHeads),
			e.deps.PageEntries, domain.MaxDecisionSyncPageBytes)
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return peerHeads, nil
		}
		heads, err := e.deps.Ledger.DecisionHeads(ctx)
		if err != nil {
			return nil, err
		}
		resp, err := remote.Push(ctx, PushRequest{
			Protocol: domain.DecisionSyncProtocol, HostID: e.deps.HostID, Entries: entries, Heads: heads,
		})
		if err != nil {
			return nil, err
		}
		out.Pushed += resp.Result.Applied
		out.PeerVector = resp.Have
		peerHave, peerHeads = resp.Have, resp.Heads
		if resp.Result.Applied == 0 {
			return peerHeads, nil
		}
	}
}
