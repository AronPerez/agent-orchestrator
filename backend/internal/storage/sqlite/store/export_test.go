package store

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// HoldWriterForTest takes the single writer until the returned func runs.
func (s *Store) HoldWriterForTest() func() {
	s.writeMu.Lock()
	return s.writeMu.Unlock
}

// InsertRawTrackerIntakeHandledForTest bypasses write-side normalization to
// model rows created by the migration from legacy sessions.issue_id values.
func (s *Store) InsertRawTrackerIntakeHandledForTest(ctx context.Context, projectID, issueID, sessionID string, at time.Time) error {
	return s.qw.RecordTrackerIntakeHandled(ctx, gen.RecordTrackerIntakeHandledParams{
		ProjectID: projectID, IssueID: issueID, SessionID: sessionID, RecordedAt: at,
	})
}
