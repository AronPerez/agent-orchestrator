package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CreateTrackerIntakeSession atomically admits one tracker item, creates its
// seed session, and writes delivery proof. All session writes share writeMu,
// so the capacity count cannot race another session create or termination.
func (s *Store) CreateTrackerIntakeSession(ctx context.Context, rec domain.SessionRecord, limits domain.AutomatedSessionLimits) (domain.SessionRecord, error) {
	if rec.ProjectID == "" {
		return domain.SessionRecord{}, fmt.Errorf("tracker intake project id is required")
	}
	if rec.IssueID == "" {
		return domain.SessionRecord{}, fmt.Errorf("tracker intake issue id is required")
	}
	limits = limits.WithDefaults()
	if err := limits.Validate(); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("tracker intake limits: %w", err)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var created domain.SessionRecord
	err := s.inTx(ctx, "create tracker intake session", func(q *gen.Queries) error {
		_, err := q.GetTrackerIntakeHandled(ctx, gen.GetTrackerIntakeHandledParams{
			ProjectID: string(rec.ProjectID),
			IssueID:   string(rec.IssueID),
		})
		switch {
		case err == nil:
			return fmt.Errorf("%w: %s/%s", ports.ErrTrackerIntakeHandled, rec.ProjectID, rec.IssueID)
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("find tracker intake handled %s/%s: %w", rec.ProjectID, rec.IssueID, err)
		}

		active, err := q.CountLiveIssueLinkedSessions(ctx, optionalProjectID(rec.ProjectID))
		if err != nil {
			return fmt.Errorf("count live issue sessions for %s: %w", rec.ProjectID, err)
		}
		if active >= int64(limits.MaxConcurrent) {
			return fmt.Errorf("%w: project %s has %d live issue sessions (limit %d)", ports.ErrAutomatedSessionCapacity, rec.ProjectID, active, limits.MaxConcurrent)
		}

		created, _, err = s.createSessionLocked(ctx, q, rec)
		if err != nil {
			return err
		}
		if err := q.InsertTrackerIntakeHandled(ctx, gen.InsertTrackerIntakeHandledParams{
			ProjectID: string(created.ProjectID),
			IssueID:   string(created.IssueID),
			SessionID: string(created.ID),
			HandledAt: created.CreatedAt,
		}); err != nil {
			return fmt.Errorf("record tracker intake handled %s/%s: %w", created.ProjectID, created.IssueID, err)
		}
		return nil
	})
	if err != nil {
		return domain.SessionRecord{}, err
	}
	return created, nil
}

// RecordTrackerIntakeHandled durably records one delivered source item. The
// primary key makes retries idempotent and preserves the first delivery facts.
func (s *Store) RecordTrackerIntakeHandled(ctx context.Context, record domain.TrackerIntakeHandledRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.RecordTrackerIntakeHandled(ctx, gen.RecordTrackerIntakeHandledParams{
		ProjectID: string(record.ProjectID),
		IssueID:   string(record.IssueID),
		SessionID: string(record.SessionID),
		HandledAt: record.HandledAt,
	}); err != nil {
		return fmt.Errorf("record tracker intake handled %s/%s: %w", record.ProjectID, record.IssueID, err)
	}
	return nil
}

// ListTrackerIntakeHandled returns durable delivery facts for one project.
func (s *Store) ListTrackerIntakeHandled(ctx context.Context, projectID domain.ProjectID) ([]domain.TrackerIntakeHandledRecord, error) {
	rows, err := s.qr.ListTrackerIntakeHandled(ctx, string(projectID))
	if err != nil {
		return nil, fmt.Errorf("list tracker intake handled for %s: %w", projectID, err)
	}
	out := make([]domain.TrackerIntakeHandledRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.TrackerIntakeHandledRecord{
			ProjectID: domain.ProjectID(row.ProjectID),
			IssueID:   domain.IssueID(row.IssueID),
			SessionID: domain.SessionID(row.SessionID),
			HandledAt: row.HandledAt,
		})
	}
	return out, nil
}
