package store

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

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
