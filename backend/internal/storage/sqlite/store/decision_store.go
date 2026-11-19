package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// AppendDecisionEntry assigns ordering, appends one immutable entry and
// recomputes its call's read model in one transaction.
func (s *Store) AppendDecisionEntry(ctx context.Context, entry domain.DecisionEntry, now time.Time) (domain.DecisionEntry, domain.DecisionCall, error) {
	stored, calls, err := s.appendDecisionEntries(ctx, []domain.DecisionEntry{entry}, now)
	if err != nil {
		return domain.DecisionEntry{}, domain.DecisionCall{}, fmt.Errorf("append decision entry %s: %w", entry.ID, err)
	}
	return stored[0], calls[0], nil
}

// AppendDecisionEntries assigns ordering and appends entries atomically in the
// supplied order, recomputing every affected call's read model.
func (s *Store) AppendDecisionEntries(ctx context.Context, entries []domain.DecisionEntry, now time.Time) ([]domain.DecisionEntry, []domain.DecisionCall, error) {
	stored, calls, err := s.appendDecisionEntries(ctx, entries, now)
	if err != nil {
		return nil, nil, fmt.Errorf("append decision entries: %w", err)
	}
	return stored, calls, nil
}

func (s *Store) appendDecisionEntries(ctx context.Context, entries []domain.DecisionEntry, now time.Time) ([]domain.DecisionEntry, []domain.DecisionCall, error) {
	if len(entries) == 0 {
		return nil, nil, fmt.Errorf("%w: at least one entry is required", domain.ErrDecisionEntryInvalid)
	}
	for _, entry := range entries {
		if entry.ID == "" || entry.Origin == "" || entry.CallID == "" || entry.Kind == "" || entry.AuthorKind == "" {
			return nil, nil, fmt.Errorf("%w: id, origin, call id, kind and author kind are required", domain.ErrDecisionEntryInvalid)
		}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	stored := make([]domain.DecisionEntry, len(entries))
	calls := make([]domain.DecisionCall, len(entries))
	err := s.inTx(ctx, "append decision entries", func(q *gen.Queries) error {
		for i, entry := range entries {
			var err error
			stored[i], calls[i], err = appendDecisionEntry(ctx, q, entry, now)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return stored, calls, nil
}

func appendDecisionEntry(ctx context.Context, q *gen.Queries, entry domain.DecisionEntry, now time.Time) (domain.DecisionEntry, domain.DecisionCall, error) {
	sequence, err := q.NextDecisionOriginSequence(ctx, entry.Origin)
	if err != nil {
		return domain.DecisionEntry{}, domain.DecisionCall{}, err
	}
	newest := domain.DecisionStamp{}
	stamp, err := q.NewestDecisionStamp(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return domain.DecisionEntry{}, domain.DecisionCall{}, err
	}
	if err == nil {
		newest = domain.DecisionStamp{WallMS: stamp.StampWall, Counter: stamp.StampCounter}
	}
	entry.OriginSeq = sequence
	entry.Stamp = newest.Next(now.UnixMilli())
	row, err := q.CreateDecisionEntry(ctx, gen.CreateDecisionEntryParams{
		ID: entry.ID, Origin: entry.Origin, OriginSeq: entry.OriginSeq,
		StampWall: entry.Stamp.WallMS, StampCounter: entry.Stamp.Counter,
		CallID: entry.CallID, Kind: string(entry.Kind), Payload: string(entry.Payload),
		WrittenAt: entry.WrittenAt, AuthorKind: string(entry.AuthorKind),
		AuthorSurface: string(entry.AuthorSurface), AuthorSession: string(entry.AuthorSession),
	})
	if err != nil {
		return domain.DecisionEntry{}, domain.DecisionCall{}, err
	}
	stored := decisionEntryFromGen(row)
	rows, err := q.ListDecisionEntries(ctx, entry.CallID)
	if err != nil {
		return domain.DecisionEntry{}, domain.DecisionCall{}, err
	}
	entries := decisionEntriesFromGen(rows)
	call, ok := domain.ReduceDecisionCall(entries)
	if !ok {
		return stored, domain.DecisionCall{}, nil
	}
	body, err := json.Marshal(call)
	if err != nil {
		return domain.DecisionEntry{}, domain.DecisionCall{}, fmt.Errorf("encode decision call: %w", err)
	}
	_, err = q.UpsertDecisionCall(ctx, gen.UpsertDecisionCallParams{
		CallID: call.ID, Origin: call.Origin, ProjectID: string(call.Raised.ProjectID),
		StreamSessionID: string(call.Raised.StreamSessionID), StreamName: call.Raised.StreamName,
		OwnerSessionID: string(call.Raised.OwnerSessionID), State: string(call.State),
		Delivery: string(call.Delivery), Retired: boolInt(call.Retired), RaisedAt: call.RaisedAt,
		LastWall: call.LastStamp.WallMS, LastCounter: call.LastStamp.Counter,
		Body: string(body), SearchText: call.SearchText(),
	})
	if err != nil {
		return domain.DecisionEntry{}, domain.DecisionCall{}, err
	}
	return stored, call, nil
}

// GetDecisionCall returns a call's materialized read model when it exists.
func (s *Store) GetDecisionCall(ctx context.Context, callID string) (domain.DecisionCall, bool, error) {
	row, err := s.qr.GetDecisionCall(ctx, callID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DecisionCall{}, false, nil
	}
	if err != nil {
		return domain.DecisionCall{}, false, fmt.Errorf("get decision call %s: %w", callID, err)
	}
	call, err := decisionCallFromGen(row)
	if err != nil {
		return domain.DecisionCall{}, false, fmt.Errorf("get decision call %s: %w", callID, err)
	}
	return call, true, nil
}

// ListDecisionEntries returns a call's entries in deterministic ledger order.
func (s *Store) ListDecisionEntries(ctx context.Context, callID string) ([]domain.DecisionEntry, error) {
	rows, err := s.qr.ListDecisionEntries(ctx, callID)
	if err != nil {
		return nil, fmt.Errorf("list decision entries for %s: %w", callID, err)
	}
	return decisionEntriesFromGen(rows), nil
}

// ListDecisionCallsForSession returns calls owned by or delivered to one local
// session, newest first.
func (s *Store) ListDecisionCallsForSession(ctx context.Context, origin string, session domain.SessionID, limit int) ([]domain.DecisionCall, error) {
	rows, err := s.qr.ListDecisionCallsForSession(ctx, gen.ListDecisionCallsForSessionParams{
		Origin: origin, SessionID: string(session), ResultLimit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list decision calls for session: %w", err)
	}
	return decisionCallsFromGen(rows)
}

// ListDecisionCallsByState returns calls in one state, oldest first.
func (s *Store) ListDecisionCallsByState(ctx context.Context, state domain.DecisionCallState, limit int) ([]domain.DecisionCall, error) {
	rows, err := s.qr.ListDecisionCallsByState(ctx, gen.ListDecisionCallsByStateParams{
		State: string(state), Limit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list decision calls by state: %w", err)
	}
	return decisionCallsFromGen(rows)
}

// ListDecisionCallsAwaitingDelivery returns one ledger snapshot for every
// undelivered answer or dismissal raised on origin.
func (s *Store) ListDecisionCallsAwaitingDelivery(ctx context.Context, origin string) ([]domain.DecisionCall, error) {
	rows, err := s.qr.ListDecisionEntriesAwaitingDelivery(ctx, origin)
	if err != nil {
		return nil, fmt.Errorf("list decisions awaiting delivery: %w", err)
	}
	calls := make([]domain.DecisionCall, 0, len(rows))
	for _, row := range rows {
		pending := decisionEntryFromGen(row)
		entryRows, err := s.qr.ListDecisionEntries(ctx, pending.CallID)
		if err != nil {
			return nil, fmt.Errorf("list decision entries for pending delivery %s: %w", pending.ID, err)
		}
		entries := make([]domain.DecisionEntry, 0, len(entryRows))
		for _, entryRow := range entryRows {
			entry := decisionEntryFromGen(entryRow)
			if entry.Kind == domain.DecisionKindDelivered || domain.CompareDecisionEntries(entry, pending) <= 0 {
				entries = append(entries, entry)
			}
		}
		call, ok := domain.ReduceDecisionCall(entries)
		if !ok || call.StandingEntryID != pending.ID {
			return nil, fmt.Errorf("reduce pending decision delivery %s", pending.ID)
		}
		calls = append(calls, call)
	}
	return calls, nil
}

// ListDecisionStreams returns the newest recorded name for every stream.
func (s *Store) ListDecisionStreams(ctx context.Context) ([]domain.DecisionStream, error) {
	rows, err := s.qr.ListDecisionStreams(ctx)
	if err != nil {
		return nil, fmt.Errorf("list decision streams: %w", err)
	}
	streams := make([]domain.DecisionStream, len(rows))
	for i, row := range rows {
		streams[i] = domain.DecisionStream{
			Origin: row.Origin, SessionID: domain.SessionID(row.StreamSessionID), Name: row.StreamName,
		}
	}
	return streams, nil
}

func decisionEntryFromGen(row gen.DecisionEntry) domain.DecisionEntry {
	return domain.DecisionEntry{
		ID: row.ID, Origin: row.Origin, OriginSeq: row.OriginSeq,
		Stamp:  domain.DecisionStamp{WallMS: row.StampWall, Counter: row.StampCounter},
		CallID: row.CallID, Kind: domain.DecisionEntryKind(row.Kind), Payload: json.RawMessage(row.Payload),
		WrittenAt: row.WrittenAt, AuthorKind: domain.DecisionAuthorKind(row.AuthorKind),
		AuthorSurface: domain.RequestSurface(row.AuthorSurface), AuthorSession: domain.SessionID(row.AuthorSession),
	}
}

func decisionEntriesFromGen(rows []gen.DecisionEntry) []domain.DecisionEntry {
	entries := make([]domain.DecisionEntry, len(rows))
	for i, row := range rows {
		entries[i] = decisionEntryFromGen(row)
	}
	return entries
}

func decisionCallFromGen(row gen.DecisionCall) (domain.DecisionCall, error) {
	var call domain.DecisionCall
	if err := json.Unmarshal([]byte(row.Body), &call); err != nil {
		return domain.DecisionCall{}, fmt.Errorf("decode decision call body: %w", err)
	}
	return call, nil
}

func decisionCallsFromGen(rows []gen.DecisionCall) ([]domain.DecisionCall, error) {
	calls := make([]domain.DecisionCall, len(rows))
	for i, row := range rows {
		call, err := decisionCallFromGen(row)
		if err != nil {
			return nil, err
		}
		calls[i] = call
	}
	return calls, nil
}
