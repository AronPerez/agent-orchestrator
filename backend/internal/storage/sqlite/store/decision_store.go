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
	if entry.ID == "" || entry.Origin == "" || entry.CallID == "" || entry.Kind == "" || entry.AuthorKind == "" {
		return domain.DecisionEntry{}, domain.DecisionCall{}, fmt.Errorf("%w: id, origin, call id, kind and author kind are required", domain.ErrDecisionEntryInvalid)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var stored domain.DecisionEntry
	var call domain.DecisionCall
	err := s.inTx(ctx, "append decision entry", func(q *gen.Queries) error {
		sequence, err := q.NextDecisionOriginSequence(ctx, entry.Origin)
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
			return err
		}
		stored = decisionEntryFromGen(row)
		rows, err := q.ListDecisionEntries(ctx, entry.CallID)
		if err != nil {
			return err
		}
		entries := decisionEntriesFromGen(rows)
		var ok bool
		call, ok = domain.ReduceDecisionCall(entries)
		if !ok {
			return nil
		}
		body, err := json.Marshal(call)
		if err != nil {
			return fmt.Errorf("encode decision call: %w", err)
		}
		_, err = q.UpsertDecisionCall(ctx, gen.UpsertDecisionCallParams{
			CallID: call.ID, Origin: call.Origin, ProjectID: string(call.Raised.ProjectID),
			StreamSessionID: string(call.Raised.StreamSessionID), StreamName: call.Raised.StreamName,
			OwnerSessionID: string(call.Raised.OwnerSessionID), State: string(call.State),
			Delivery: string(call.Delivery), Retired: boolInt(call.Retired), RaisedAt: call.RaisedAt,
			LastWall: call.LastStamp.WallMS, LastCounter: call.LastStamp.Counter,
			Body: string(body), SearchText: call.SearchText(),
		})
		return err
	})
	if err != nil {
		return domain.DecisionEntry{}, domain.DecisionCall{}, fmt.Errorf("append decision entry %s: %w", entry.ID, err)
	}
	return stored, call, nil
}

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

// ListDecisionCallsByOwner returns one session's calls on one machine, newest first.
func (s *Store) ListDecisionCallsByOwner(ctx context.Context, origin string, owner domain.SessionID, limit int) ([]domain.DecisionCall, error) {
	rows, err := s.qr.ListDecisionCallsByOwner(ctx, gen.ListDecisionCallsByOwnerParams{
		Origin: origin, OwnerSessionID: string(owner), Limit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list decision calls by owner: %w", err)
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

// ListDecisionCallsAwaitingDelivery returns pending calls raised on origin.
func (s *Store) ListDecisionCallsAwaitingDelivery(ctx context.Context, origin string) ([]domain.DecisionCall, error) {
	rows, err := s.qr.ListDecisionCallsAwaitingDelivery(ctx, origin)
	if err != nil {
		return nil, fmt.Errorf("list decisions awaiting delivery: %w", err)
	}
	return decisionCallsFromGen(rows)
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
