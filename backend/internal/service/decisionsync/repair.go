package decisionsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Identity repair: a machine that shares its identity with a copy of itself
// gives it up. It finds where the two histories parted, stages the repair in
// repair.json, and finishes it on the daemon's next start, before anything is
// served or synced: switch identity, re-issue its own entries from the fork
// on under the new identity, move the originals out of the log, and remove
// its outbound links, whose tokens were minted for the old identity.

// RepairState is a staged repair's progress. Each state is saved before the
// next step runs, so a restart continues from the last one saved.
type RepairState string

// Repair states, in order.
const (
	RepairStaged           RepairState = "staged"
	RepairIdentitySwitched RepairState = "identity_switched"
	RepairReissued         RepairState = "reissued"
	RepairArchived         RepairState = "archived"
	RepairDone             RepairState = "done"
)

// RepairRecord is repair.json.
type RepairRecord struct {
	State     RepairState               `json:"state"`
	Plan      domain.DecisionRepairPlan `json:"plan"`
	StagedAt  time.Time                 `json:"stagedAt"`
	UpdatedAt time.Time                 `json:"updatedAt"`
}

// SyncHeld reports whether the repair still has steps to run. Sync stays
// off until it is done.
func (r RepairRecord) SyncHeld() bool { return r.State != "" && r.State != RepairDone }

// RepairPath is where a staged repair is kept.
func RepairPath(dataDir string) string {
	return filepath.Join(dataDir, "decision-sync", "repair.json")
}

// LoadRepair reads repair.json; found is false when there is none.
func LoadRepair(path string) (RepairRecord, bool, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- path is under the daemon's own data directory.
	if errors.Is(err, os.ErrNotExist) {
		return RepairRecord{}, false, nil
	}
	if err != nil {
		return RepairRecord{}, false, fmt.Errorf("read decision repair: %w", err)
	}
	var rec RepairRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return RepairRecord{}, false, fmt.Errorf("decode decision repair: %w", err)
	}
	return rec, true, nil
}

func saveRepair(path string, rec RepairRecord) error {
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return writeOwnerOnlyFile(path, raw)
}

// RepairStore is the ledger as identity repair sees it.
type RepairStore interface {
	DecisionOriginIDs(ctx context.Context, origin string, from int64, limit int) ([]domain.DecisionOriginID, error)
	DecisionEntriesFrom(ctx context.Context, origin string, from int64) ([]domain.DecisionEntry, error)
	ListDecisionSyncConflicts(ctx context.Context) ([]domain.DecisionSyncConflict, error)
	ReissueDecisionFork(ctx context.Context, plan domain.DecisionRepairPlan, newID func() string, now time.Time) (bool, error)
	ArchiveDecisionFork(ctx context.Context, repairID string, now time.Time) (bool, error)
}

// Repair refusals.
var (
	ErrRepairNothingToDo = errors.New("no fork of this machine's identity is recorded, so there is nothing to repair")
	ErrRepairNeedsPeer   = errors.New("no linked machine holds a different history of this machine's identity: " +
		"either this is the original, or link a machine that has synced with the original and try again")
	ErrRepairPending = errors.New("a repair is already staged; restart Orchestrator to finish it")
	ErrRepairChanged = errors.New("the repair changed since it was shown; review it again")
)

// RepairProposal is what a repair would do, shown before it is staged.
type RepairProposal struct {
	Origin  string
	FromSeq int64
	// ForkIDs are this machine's own entries under Origin from FromSeq on,
	// which a repair re-issues and moves out of the log.
	ForkIDs []string
	// CallIDs are the calls those entries belong to.
	CallIDs []string
	// OutboundLinks are the links a repair removes.
	OutboundLinks []StoredPeer
	PeersAsked    int
	PeersFailed   int
}

// RepairerDeps configures identity repair on a running daemon.
type RepairerDeps struct {
	Store  RepairStore
	HostID string
	Path   string
	Links  LinkSource
	Remote func(StoredPeer) Remote
	// NewID returns prefix plus a fresh unique id.
	NewID func(prefix string) string
	Now   func() time.Time
	// Pause stops this machine's sync once a repair is staged, so no peer
	// sees the old identity's fork again before the restart.
	Pause func()
}

// Repairer proposes and stages identity repairs.
type Repairer struct {
	deps RepairerDeps
}

// NewRepairer returns a repairer over deps.
func NewRepairer(deps RepairerDeps) *Repairer {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Repairer{deps: deps}
}

// Status returns the staged or finished repair, if any.
func (r *Repairer) Status() (RepairRecord, bool, error) {
	return LoadRepair(r.deps.Path)
}

// Propose finds where this machine's history of its own identity parted
// from another machine's, by comparing entry ids with every linked machine.
// It changes nothing.
func (r *Repairer) Propose(ctx context.Context) (RepairProposal, error) {
	if rec, found, err := LoadRepair(r.deps.Path); err != nil {
		return RepairProposal{}, err
	} else if found && rec.SyncHeld() {
		return RepairProposal{}, ErrRepairPending
	}
	origin := r.deps.HostID
	conflicts, err := r.deps.Store.ListDecisionSyncConflicts(ctx)
	if err != nil {
		return RepairProposal{}, err
	}
	from := int64(math.MaxInt64)
	for _, c := range conflicts {
		if c.Origin == origin {
			from = min(from, c.OriginSeq)
		}
	}
	if from == math.MaxInt64 {
		return RepairProposal{}, ErrRepairNothingToDo
	}
	local, err := r.deps.Store.DecisionOriginIDs(ctx, origin, 1, math.MaxInt32)
	if err != nil {
		return RepairProposal{}, err
	}
	out := RepairProposal{Origin: origin, OutboundLinks: r.deps.Links.Linked()}
	// A machine whose history differs from this one's has seen the other
	// copy's: an id it holds at the same sequence is shared history, never
	// this machine's own, however the histories interleave. A machine whose
	// history matches has seen only this copy, so it vouches for nothing.
	sharedAt := map[int64]map[string]bool{}
	differs := false
	for _, peer := range out.OutboundLinks {
		out.PeersAsked++
		ids, err := r.peerOriginIDs(ctx, peer, origin)
		if err != nil {
			out.PeersFailed++
			continue
		}
		seq, found := domain.DecisionForkPoint(local, ids)
		if !found {
			continue
		}
		differs = true
		from = min(from, seq)
		for _, id := range ids {
			if sharedAt[id.Seq] == nil {
				sharedAt[id.Seq] = map[string]bool{}
			}
			sharedAt[id.Seq][id.ID] = true
		}
	}
	if !differs {
		return RepairProposal{}, ErrRepairNeedsPeer
	}
	out.FromSeq = from
	for _, id := range local {
		if id.Seq >= from && !sharedAt[id.Seq][id.ID] {
			out.ForkIDs = append(out.ForkIDs, id.ID)
		}
	}
	if len(out.ForkIDs) == 0 {
		return RepairProposal{}, ErrRepairNeedsPeer
	}
	entries, err := r.deps.Store.DecisionEntriesFrom(ctx, origin, from)
	if err != nil {
		return RepairProposal{}, err
	}
	for _, e := range entries {
		if slices.Contains(out.ForkIDs, e.ID) && !slices.Contains(out.CallIDs, e.CallID) {
			out.CallIDs = append(out.CallIDs, e.CallID)
		}
	}
	slices.Sort(out.CallIDs)
	return out, nil
}

func (r *Repairer) peerOriginIDs(ctx context.Context, peer StoredPeer, origin string) ([]domain.DecisionOriginID, error) {
	remote := r.deps.Remote(peer)
	var out []domain.DecisionOriginID
	from := int64(1)
	for {
		resp, err := remote.OriginIDs(ctx, OriginIDsRequest{
			Protocol: domain.DecisionSyncProtocol, HostID: r.deps.HostID, Origin: origin, From: from, Limit: MaxOriginIDsPage,
		})
		if err != nil {
			return nil, err
		}
		if resp.HostID != peer.HostID {
			return nil, ErrPeerIdentityChanged
		}
		out = append(out, resp.IDs...)
		if !resp.More || len(resp.IDs) == 0 {
			return out, nil
		}
		from = resp.IDs[len(resp.IDs)-1].Seq + 1
	}
}

// Stage proposes again and, when the result still matches what the person
// confirmed (the same fork point and entries), writes repair.json and pauses
// sync. The repair finishes on the daemon's next start.
func (r *Repairer) Stage(ctx context.Context, fromSeq int64, forkIDs []string) (RepairRecord, error) {
	proposal, err := r.Propose(ctx)
	if err != nil {
		return RepairRecord{}, err
	}
	if proposal.FromSeq != fromSeq || !slices.Equal(proposal.ForkIDs, forkIDs) {
		return RepairRecord{}, ErrRepairChanged
	}
	now := r.deps.Now().UTC()
	rec := RepairRecord{
		State: RepairStaged,
		Plan: domain.DecisionRepairPlan{
			RepairID: r.deps.NewID("rep_"), Origin: proposal.Origin, FromSeq: proposal.FromSeq,
			NewOrigin: r.deps.NewID("h_"), ForkIDs: proposal.ForkIDs,
		},
		StagedAt: now, UpdatedAt: now,
	}
	if err := saveRepair(r.deps.Path, rec); err != nil {
		return RepairRecord{}, err
	}
	if r.deps.Pause != nil {
		r.deps.Pause()
	}
	return rec, nil
}

// ResumeDeps configures finishing a staged repair at daemon start.
type ResumeDeps struct {
	Path        string
	Store       RepairStore
	ReadHostID  func() (string, error)
	WriteHostID func(string) error
	NewID       func() string
	Now         func() time.Time
	// AfterStep runs after each state is saved. Tests stop the run there to
	// stand in for a crash.
	AfterStep func(RepairState) error
}

// ErrRepairIdentityMoved reports a staged repair whose host identity is
// neither the old one nor the new one: something else changed it.
var ErrRepairIdentityMoved = errors.New("the host identity changed since the repair was staged")

// ResumeRepair runs a staged repair up to archived: switch the host
// identity, re-issue the fork under it, move the originals out of the log.
// Every step can run again after a crash: the identity is compared before it
// is written, the re-issue writes nothing once its entries exist, and only
// rows still in the log move. Removing the old links needs the link manager
// and runs later (FinishRepairLinks).
func ResumeRepair(ctx context.Context, deps ResumeDeps) (RepairRecord, bool, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	rec, found, err := LoadRepair(deps.Path)
	if err != nil || !found {
		return rec, found, err
	}
	advance := func(next RepairState) error {
		rec.State, rec.UpdatedAt = next, deps.Now().UTC()
		if err := saveRepair(deps.Path, rec); err != nil {
			return err
		}
		if deps.AfterStep != nil {
			return deps.AfterStep(next)
		}
		return nil
	}
	plan := rec.Plan
	for {
		switch rec.State {
		case RepairStaged:
			current, err := deps.ReadHostID()
			if err != nil {
				return rec, true, err
			}
			switch current {
			case plan.NewOrigin:
			case plan.Origin:
				if err := deps.WriteHostID(plan.NewOrigin); err != nil {
					return rec, true, err
				}
			default:
				return rec, true, ErrRepairIdentityMoved
			}
			if err := advance(RepairIdentitySwitched); err != nil {
				return rec, true, err
			}
		case RepairIdentitySwitched:
			if _, err := deps.Store.ReissueDecisionFork(ctx, plan, deps.NewID, deps.Now().UTC()); err != nil {
				return rec, true, err
			}
			if err := advance(RepairReissued); err != nil {
				return rec, true, err
			}
		case RepairReissued:
			complete, err := deps.Store.ArchiveDecisionFork(ctx, plan.RepairID, deps.Now().UTC())
			if err != nil {
				return rec, true, err
			}
			if !complete {
				return rec, true, fmt.Errorf("decision repair %s: its entries are missing", plan.RepairID)
			}
			if err := advance(RepairArchived); err != nil {
				return rec, true, err
			}
		default:
			return rec, true, nil
		}
	}
}

// RepairLinks is the link manager as finishing a repair sees it.
type RepairLinks interface {
	Linked() []StoredPeer
	Unlink(ctx context.Context, hostID string) error
}

// FinishRepairLinks removes every outbound link of an archived repair (each
// one's token was minted for the old identity) and marks the repair done.
// A link already gone counts as removed, so a restart finishes the rest.
func FinishRepairLinks(ctx context.Context, path string, links RepairLinks, now time.Time) (RepairRecord, error) {
	rec, found, err := LoadRepair(path)
	if err != nil || !found || rec.State != RepairArchived {
		return rec, err
	}
	for _, peer := range links.Linked() {
		if err := links.Unlink(ctx, peer.HostID); err != nil && !errors.Is(err, ErrPeerNotLinked) {
			return rec, err
		}
	}
	rec.State, rec.UpdatedAt = RepairDone, now.UTC()
	return rec, saveRepair(path, rec)
}
