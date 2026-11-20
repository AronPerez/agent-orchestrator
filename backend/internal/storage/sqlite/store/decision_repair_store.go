package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// DecisionOriginIDs returns up to limit (sequence, id) pairs of origin from
// sequence from on, in sequence order. Identity repair compares them with a
// peer's to find where two histories of one identity parted.
func (s *Store) DecisionOriginIDs(ctx context.Context, origin string, from int64, limit int) ([]domain.DecisionOriginID, error) {
	rows, err := s.qr.ListDecisionOriginIDs(ctx, gen.ListDecisionOriginIDsParams{
		Origin: origin, FromSeq: from, ResultLimit: int64(max(limit, 1)),
	})
	if err != nil {
		return nil, fmt.Errorf("decision origin ids for %s: %w", origin, err)
	}
	out := make([]domain.DecisionOriginID, len(rows))
	for i, row := range rows {
		out[i] = domain.DecisionOriginID{Seq: row.OriginSeq, ID: row.ID}
	}
	return out, nil
}

// DecisionEntriesFrom returns origin's entries from sequence from on, in
// sequence order.
func (s *Store) DecisionEntriesFrom(ctx context.Context, origin string, from int64) ([]domain.DecisionEntry, error) {
	rows, err := s.qr.ListDecisionEntriesForOriginAfter(ctx, gen.ListDecisionEntriesForOriginAfterParams{
		Origin: origin, AfterSeq: from - 1, BeforeSeq: math.MaxInt64, ResultLimit: math.MaxInt32,
	})
	if err != nil {
		return nil, fmt.Errorf("decision entries for %s from %d: %w", origin, from, err)
	}
	return decisionEntriesFromGen(rows), nil
}

// ReissueDecisionFork appends a staged repair's entries under its new
// identity in one transaction: the identity_repaired parts, then a copy of
// each fork entry (domain.BuildDecisionRepairEntries). It writes nothing and
// returns false when this repair's entries already exist, so a restart after
// a crash never re-issues twice.
func (s *Store) ReissueDecisionFork(ctx context.Context, plan domain.DecisionRepairPlan, newID func() string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	wrote := false
	err := s.inTx(ctx, "reissue decision fork", func(q *gen.Queries) error {
		done, err := q.ListDecisionEntries(ctx, domain.DecisionRepairCallID(plan.RepairID))
		if err != nil {
			return err
		}
		if len(done) > 0 {
			return nil
		}
		fork := make([]domain.DecisionEntry, 0, len(plan.ForkIDs))
		for _, id := range plan.ForkIDs {
			row, err := q.GetDecisionEntryByID(ctx, id)
			if err != nil {
				return fmt.Errorf("fork entry %s: %w", id, err)
			}
			fork = append(fork, decisionEntryFromGen(row))
		}
		later, err := q.ListDecisionEntriesForOriginAfter(ctx, gen.ListDecisionEntriesForOriginAfterParams{
			Origin: plan.Origin, AfterSeq: plan.ForkThrough(fork), BeforeSeq: math.MaxInt64, ResultLimit: math.MaxInt32,
		})
		if err != nil {
			return err
		}
		fork = append(fork, decisionEntriesFromGen(later)...)
		slices.SortFunc(fork, func(a, b domain.DecisionEntry) int { return cmp.Compare(a.OriginSeq, b.OriginSeq) })
		start, err := q.NextDecisionOriginSequence(ctx, plan.NewOrigin)
		if err != nil {
			return err
		}
		newest := domain.DecisionStamp{}
		stamp, err := q.NewestDecisionStamp(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			newest = domain.DecisionStamp{WallMS: stamp.StampWall, Counter: stamp.StampCounter}
		}
		entries, err := domain.BuildDecisionRepairEntries(domain.DecisionRepairInput{
			RepairID: plan.RepairID, Origin: plan.Origin, FromSeq: plan.FromSeq, NewOrigin: plan.NewOrigin,
			StartSeq: start, Fork: fork, Stamp: newest.Next(now.UnixMilli()), Now: now, NewID: newID,
		})
		if err != nil {
			return err
		}
		callIDs := make([]string, 0, len(entries))
		for _, e := range entries {
			if _, err := insertDecisionEntry(ctx, q, e); err != nil {
				return err
			}
			callIDs = append(callIDs, e.CallID)
		}
		for _, id := range sortedUnique(callIDs) {
			if _, _, err := reprojectOrDropDecisionCall(ctx, q, id); err != nil {
				return err
			}
		}
		wrote = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("reissue decision fork: %w", err)
	}
	return wrote, nil
}

// ArchiveDecisionFork finishes a repair on this machine in one transaction:
// its originals move into decision_entries_forked and, once none is left in
// the log, the repaired origin's conflicts clear (domain.FinishDecisionRepair).
// complete is false while some part of the repair is not held yet.
func (s *Store) ArchiveDecisionFork(ctx context.Context, repairID string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	complete := false
	err := s.inTx(ctx, "archive decision fork", func(q *gen.Queries) error {
		callIDs, done, err := domain.FinishDecisionRepair(&sqlDecisionApplyView{ctx: ctx, q: q}, repairID, now)
		if err != nil {
			return err
		}
		for _, id := range sortedUnique(callIDs) {
			if _, _, err := reprojectOrDropDecisionCall(ctx, q, id); err != nil {
				return err
			}
		}
		complete = done
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("archive decision fork: %w", err)
	}
	return complete, nil
}

// ArchivedDecisionEntry is one row an identity repair moved out of the log.
type ArchivedDecisionEntry struct {
	Entry      domain.DecisionEntry
	ArchivedAt time.Time
	RepairID   string
}

// ListArchivedDecisionEntries returns the rows repairID moved out of the log.
func (s *Store) ListArchivedDecisionEntries(ctx context.Context, repairID string) ([]ArchivedDecisionEntry, error) {
	rows, err := s.qr.ListArchivedDecisionEntries(ctx, repairID)
	if err != nil {
		return nil, fmt.Errorf("list archived decision entries: %w", err)
	}
	out := make([]ArchivedDecisionEntry, len(rows))
	for i, row := range rows {
		out[i] = ArchivedDecisionEntry{
			Entry: decisionEntryFromGen(gen.DecisionEntry{
				ID: row.ID, Origin: row.Origin, OriginSeq: row.OriginSeq, StampWall: row.StampWall,
				StampCounter: row.StampCounter, CallID: row.CallID, Kind: row.Kind, Payload: row.Payload,
				WrittenAt: row.WrittenAt, AuthorKind: row.AuthorKind, AuthorSurface: row.AuthorSurface,
				AuthorSession: row.AuthorSession,
			}),
			ArchivedAt: row.ArchivedAt, RepairID: row.RepairID,
		}
	}
	return out, nil
}

// reprojectOrDropDecisionCall recomputes a call, and removes its read model
// when its entries no longer reduce to a call: a repair moved its raised
// entry out of the log and the copy has not arrived yet.
func reprojectOrDropDecisionCall(ctx context.Context, q *gen.Queries, callID string) (domain.DecisionCall, bool, error) {
	call, ok, err := reprojectDecisionCall(ctx, q, callID)
	if err != nil {
		return domain.DecisionCall{}, false, fmt.Errorf("reproject decision call %s: %w", callID, err)
	}
	if !ok {
		if _, err := q.DeleteDecisionCall(ctx, callID); err != nil {
			return domain.DecisionCall{}, false, fmt.Errorf("drop decision call %s: %w", callID, err)
		}
	}
	return call, ok, nil
}

func sortedUnique(ids []string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}

func (v *sqlDecisionApplyView) RepairEntries() ([]domain.DecisionEntry, error) {
	rows, err := v.q.ListDecisionRepairEntries(v.ctx)
	if err != nil {
		return nil, err
	}
	return decisionEntriesFromGen(rows), nil
}

func (v *sqlDecisionApplyView) ArchiveForked(repairID string, ids []string, now time.Time) ([]domain.DecisionEntry, error) {
	var moved []domain.DecisionEntry
	for _, id := range ids {
		row, err := v.q.GetDecisionEntryByID(v.ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, err := v.q.ArchiveDecisionEntry(v.ctx, gen.ArchiveDecisionEntryParams{ID: id, ArchivedAt: now.UTC(), RepairID: repairID}); err != nil {
			return nil, fmt.Errorf("archive decision entry %s: %w", id, err)
		}
		n, err := v.q.RemoveArchivedDecisionEntry(v.ctx, id)
		if err != nil {
			return nil, fmt.Errorf("remove archived decision entry %s: %w", id, err)
		}
		if n == 1 {
			moved = append(moved, decisionEntryFromGen(row))
		}
	}
	return moved, nil
}

func (v *sqlDecisionApplyView) OriginIDsFrom(origin string, from int64) ([]domain.DecisionOriginID, error) {
	rows, err := v.q.ListDecisionOriginIDs(v.ctx, gen.ListDecisionOriginIDsParams{Origin: origin, FromSeq: from, ResultLimit: math.MaxInt32})
	if err != nil {
		return nil, err
	}
	out := make([]domain.DecisionOriginID, len(rows))
	for i, row := range rows {
		out[i] = domain.DecisionOriginID{Seq: row.OriginSeq, ID: row.ID}
	}
	return out, nil
}

func (v *sqlDecisionApplyView) ClearConflicts(origin string) error {
	_, err := v.q.DeleteDecisionSyncConflictsForOrigin(v.ctx, origin)
	return err
}
