package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// DecisionVector returns the highest origin sequence held for every origin.
func (s *Store) DecisionVector(ctx context.Context) (domain.DecisionVector, error) {
	rows, err := s.qr.DecisionVector(ctx)
	if err != nil {
		return nil, fmt.Errorf("decision vector: %w", err)
	}
	v := make(domain.DecisionVector, len(rows))
	for _, row := range rows {
		v[row.Origin] = row.MaxSeq
	}
	return v, nil
}

// DecisionHeads returns the newest entry held for every origin, marking the
// origins this machine stopped after recording a conflict.
func (s *Store) DecisionHeads(ctx context.Context) (domain.DecisionHeads, error) {
	rows, err := s.qr.DecisionHeads(ctx)
	if err != nil {
		return nil, fmt.Errorf("decision heads: %w", err)
	}
	conflicts, err := s.ListDecisionSyncConflicts(ctx)
	if err != nil {
		return nil, err
	}
	stopped := domain.LowestDecisionSyncConflicts(conflicts)
	heads := make(domain.DecisionHeads, len(rows))
	for _, row := range rows {
		_, isStopped := stopped[row.Origin]
		heads[row.Origin] = domain.DecisionHead{
			Seq: row.OriginSeq, ID: row.ID, WrittenAt: row.WrittenAt, Stopped: isStopped,
		}
	}
	return heads, nil
}

// DecisionOriginsLastHeard returns, per origin, when its newest held entry was
// written, whichever peer relayed it.
func (s *Store) DecisionOriginsLastHeard(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.qr.DecisionHeads(ctx)
	if err != nil {
		return nil, fmt.Errorf("decision origins last heard: %w", err)
	}
	out := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		out[row.Origin] = row.WrittenAt
	}
	return out, nil
}

// ListDecisionEntriesAfter returns one page of the entries a holder of have
// lacks, origin by origin in ascending order and each origin in sequence
// order. A page holds at most maxEntries entries and maxBytes of payload, and
// always at least one entry when any is due. An origin with a recorded
// conflict is not served at all (see domain.PageDecisionEntries).
// more reports whether anything due was left out.
func (s *Store) ListDecisionEntriesAfter(ctx context.Context, have domain.DecisionVector, maxEntries, maxBytes int) ([]domain.DecisionEntry, bool, error) {
	if maxEntries < 1 {
		maxEntries = 1
	}
	vector, err := s.DecisionVector(ctx)
	if err != nil {
		return nil, false, err
	}
	conflicts, err := s.ListDecisionSyncConflicts(ctx)
	if err != nil {
		return nil, false, err
	}
	return domain.PageDecisionEntries(vector, domain.LowestDecisionSyncConflicts(conflicts), have, maxEntries, maxBytes,
		func(origin string, after, before int64, limit int) ([]domain.DecisionEntry, error) {
			rows, err := s.qr.ListDecisionEntriesForOriginAfter(ctx, gen.ListDecisionEntriesForOriginAfterParams{
				Origin: origin, AfterSeq: after, BeforeSeq: before, ResultLimit: int64(limit),
			})
			if err != nil {
				return nil, fmt.Errorf("list decision entries for %s after %d: %w", origin, after, err)
			}
			return decisionEntriesFromGen(rows), nil
		})
}

// ApplyDecisionEntries stores entries received from peerHostID verbatim under
// the sync apply rules (domain.ApplyDecisionSyncBatch), in one transaction,
// then recomputes each affected call once in call id order. An invalid entry
// fails the whole batch before anything is written.
func (s *Store) ApplyDecisionEntries(ctx context.Context, peerHostID string, entries []domain.DecisionEntry, now time.Time) (domain.DecisionApplyResult, error) {
	if len(entries) == 0 {
		return domain.DecisionApplyResult{}, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.DecisionApplyResult
	err := s.inTx(ctx, "apply decision entries", func(q *gen.Queries) error {
		highest, stopped, err := decisionApplyState(ctx, q)
		if err != nil {
			return err
		}
		res, callIDs, err := domain.ApplyDecisionSyncBatch(&sqlDecisionApplyView{ctx: ctx, q: q}, peerHostID, entries, highest, stopped, now)
		if err != nil {
			return err
		}
		for _, callID := range callIDs {
			call, ok, err := reprojectOrDropDecisionCall(ctx, q, callID)
			if err != nil {
				return err
			}
			if ok {
				res.Calls = append(res.Calls, call)
			}
		}
		result = res
		return nil
	})
	if err != nil {
		return domain.DecisionApplyResult{}, fmt.Errorf("apply decision entries: %w", err)
	}
	return result, nil
}

// CheckDecisionHeads records a conflict for every peer head whose sequence
// this ledger holds under another entry id.
func (s *Store) CheckDecisionHeads(ctx context.Context, peerHostID string, heads domain.DecisionHeads, now time.Time) ([]domain.DecisionSyncConflict, error) {
	if len(heads) == 0 {
		return nil, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var out []domain.DecisionSyncConflict
	err := s.inTx(ctx, "check decision heads", func(q *gen.Queries) error {
		var err error
		out, err = domain.CheckDecisionSyncHeads(&sqlDecisionApplyView{ctx: ctx, q: q}, peerHostID, heads, now)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("check decision heads: %w", err)
	}
	return out, nil
}

// ListDecisionSyncConflicts returns every recorded forked identity.
func (s *Store) ListDecisionSyncConflicts(ctx context.Context) ([]domain.DecisionSyncConflict, error) {
	rows, err := s.qr.ListDecisionSyncConflicts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list decision sync conflicts: %w", err)
	}
	return decisionSyncConflictsFromGen(rows), nil
}

// AuditDecisionOrigins returns origins whose stored sequences are not exactly
// 1..highest. Have is how many entries are held and Got the highest sequence.
func (s *Store) AuditDecisionOrigins(ctx context.Context) ([]domain.DecisionSyncGap, error) {
	rows, err := s.qr.AuditDecisionOrigins(ctx)
	if err != nil {
		return nil, fmt.Errorf("audit decision origins: %w", err)
	}
	gaps := make([]domain.DecisionSyncGap, len(rows))
	for i, row := range rows {
		gaps[i] = domain.DecisionSyncGap{Origin: row.Origin, Have: row.Held, Got: row.MaxSeq}
	}
	return gaps, nil
}

func decisionApplyState(ctx context.Context, q *gen.Queries) (domain.DecisionVector, map[string]bool, error) {
	rows, err := q.DecisionVector(ctx)
	if err != nil {
		return nil, nil, err
	}
	highest := make(domain.DecisionVector, len(rows))
	for _, row := range rows {
		highest[row.Origin] = row.MaxSeq
	}
	conflicts, err := q.ListDecisionSyncConflicts(ctx)
	if err != nil {
		return nil, nil, err
	}
	stopped := map[string]bool{}
	for _, c := range conflicts {
		stopped[c.Origin] = true
	}
	return highest, stopped, nil
}

type sqlDecisionApplyView struct {
	ctx context.Context
	q   *gen.Queries
}

func (v *sqlDecisionApplyView) EntryByID(id string) (domain.DecisionEntry, bool, error) {
	row, err := v.q.GetDecisionEntryByID(v.ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DecisionEntry{}, false, nil
	}
	if err != nil {
		return domain.DecisionEntry{}, false, err
	}
	return decisionEntryFromGen(row), true, nil
}

func (v *sqlDecisionApplyView) EntryAt(origin string, seq int64) (domain.DecisionEntry, bool, error) {
	row, err := v.q.GetDecisionEntryByOriginSeq(v.ctx, gen.GetDecisionEntryByOriginSeqParams{Origin: origin, OriginSeq: seq})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DecisionEntry{}, false, nil
	}
	if err != nil {
		return domain.DecisionEntry{}, false, err
	}
	return decisionEntryFromGen(row), true, nil
}

func (v *sqlDecisionApplyView) Insert(e domain.DecisionEntry) error {
	_, err := insertDecisionEntry(v.ctx, v.q, e)
	return err
}

func (v *sqlDecisionApplyView) RecordConflict(c domain.DecisionSyncConflict) (bool, error) {
	n, err := v.q.InsertDecisionSyncConflict(v.ctx, gen.InsertDecisionSyncConflictParams{
		Origin: c.Origin, OriginSeq: c.OriginSeq, LocalEntryID: c.LocalEntryID,
		RemoteEntryID: c.RemoteEntryID, PeerHostID: c.PeerHostID, DetectedAt: c.DetectedAt.UTC(),
	})
	return n == 1, err
}

func decisionSyncConflictsFromGen(rows []gen.DecisionSyncConflict) []domain.DecisionSyncConflict {
	out := make([]domain.DecisionSyncConflict, len(rows))
	for i, row := range rows {
		out[i] = domain.DecisionSyncConflict{
			Origin: row.Origin, OriginSeq: row.OriginSeq, LocalEntryID: row.LocalEntryID,
			RemoteEntryID: row.RemoteEntryID, PeerHostID: row.PeerHostID, DetectedAt: row.DetectedAt,
		}
	}
	return out
}
