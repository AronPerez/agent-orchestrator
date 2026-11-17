package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CreateTrackerIntakeSession atomically admits one tracker item, creates its
// seed session, and writes a pending claim. All session writes share writeMu,
// so neither the same-issue check nor capacity count can race another session
// create or termination. The manager promotes the claim after launch succeeds.
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
		claim, err := q.GetTrackerIntakeClaim(ctx, gen.GetTrackerIntakeClaimParams{
			ProjectID: string(rec.ProjectID),
			IssueID:   string(rec.IssueID),
		})
		switch {
		case err == nil:
			if claim.State == "handled" {
				return fmt.Errorf("%w: %s/%s", ports.ErrTrackerIntakeHandled, rec.ProjectID, rec.IssueID)
			}
			return fmt.Errorf("%w: %s/%s", ports.ErrTrackerIntakeActive, rec.ProjectID, rec.IssueID)
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("find tracker intake claim %s/%s: %w", rec.ProjectID, rec.IssueID, err)
		}

		activeIssue, err := q.HasLiveSessionForTrackerIssue(ctx, gen.HasLiveSessionForTrackerIssueParams{
			ProjectID: optionalProjectID(rec.ProjectID),
			IssueID:   rec.IssueID,
		})
		if err != nil {
			return fmt.Errorf("find live tracker issue session %s/%s: %w", rec.ProjectID, rec.IssueID, err)
		}
		if activeIssue {
			return fmt.Errorf("%w: %s/%s", ports.ErrTrackerIntakeActive, rec.ProjectID, rec.IssueID)
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
		if err := q.InsertTrackerIntakeClaim(ctx, gen.InsertTrackerIntakeClaimParams{
			ProjectID:  string(created.ProjectID),
			IssueID:    string(created.IssueID),
			SessionID:  string(created.ID),
			RecordedAt: created.CreatedAt,
		}); err != nil {
			return fmt.Errorf("record tracker intake claim %s/%s: %w", created.ProjectID, created.IssueID, err)
		}
		return nil
	})
	if err != nil {
		return domain.SessionRecord{}, err
	}
	return created, nil
}

// CommitTrackerIntakeSession promotes a pending claim only after the worker
// launch has completed. It is idempotent for an already-promoted session.
func (s *Store) CommitTrackerIntakeSession(ctx context.Context, id domain.SessionID, handledAt time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.commitTrackerIntakeSessionLocked(ctx, s.qw, id, handledAt)
}

func (s *Store) commitTrackerIntakeSessionLocked(ctx context.Context, q *gen.Queries, id domain.SessionID, handledAt time.Time) error {
	rows, err := q.CommitTrackerIntakeSession(ctx, gen.CommitTrackerIntakeSessionParams{
		RecordedAt: handledAt,
		SessionID:  string(id),
	})
	if err != nil {
		return fmt.Errorf("commit tracker intake session %s: %w", id, err)
	}
	if rows == 1 {
		return nil
	}
	claim, err := q.GetTrackerIntakeClaimBySession(ctx, string(id))
	if err == nil && claim.State == "handled" {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("commit tracker intake session %s: claim is missing", id)
	}
	if err != nil {
		return fmt.Errorf("commit tracker intake session %s: reload claim: %w", id, err)
	}
	return fmt.Errorf("commit tracker intake session %s: claim state is %q", id, claim.State)
}

// ReconcileTrackerIntakeSession settles one claim after an uncertain launch.
// Missing or terminated sessions are confirmed failures and release pending
// claims. A surviving session with durable controller evidence is promoted.
// A live seed or workspace-only row remains pending until normal session
// startup reconciliation removes it or a controller advances it.
func (s *Store) ReconcileTrackerIntakeSession(ctx context.Context, id domain.SessionID, handledAt time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.reconcileTrackerIntakeSessionLocked(ctx, s.qw, id, handledAt)
}

// ReconcileTrackerIntakeClaims settles every pending claim for a project before
// an observer poll chooses more work. It is the restart recovery path for a
// daemon that stopped between seed creation and launch completion.
func (s *Store) ReconcileTrackerIntakeClaims(ctx context.Context, projectID domain.ProjectID, handledAt time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	claims, err := s.qw.ListPendingTrackerIntakeClaims(ctx, string(projectID))
	if err != nil {
		return fmt.Errorf("list pending tracker intake claims for %s: %w", projectID, err)
	}
	for _, claim := range claims {
		if err := s.reconcileTrackerIntakeSessionLocked(ctx, s.qw, domain.SessionID(claim.SessionID), handledAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) reconcileTrackerIntakeSessionLocked(ctx context.Context, q *gen.Queries, id domain.SessionID, handledAt time.Time) error {
	claim, err := q.GetTrackerIntakeClaimBySession(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && claim.State == "handled") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reconcile tracker intake session %s: load claim: %w", id, err)
	}
	row, err := q.GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return s.deletePendingTrackerIntakeClaimLocked(ctx, q, id)
	}
	if err != nil {
		return fmt.Errorf("reconcile tracker intake session %s: load session: %w", id, err)
	}
	rec := rowToRecord(row)
	if rec.IsTerminated {
		return s.deletePendingTrackerIntakeClaimLocked(ctx, q, id)
	}
	if trackerIntakeSessionProgressed(rec) {
		return s.commitTrackerIntakeSessionLocked(ctx, q, id, handledAt)
	}
	return nil
}

func (s *Store) deletePendingTrackerIntakeClaimLocked(ctx context.Context, q *gen.Queries, id domain.SessionID) error {
	if _, err := q.DeletePendingTrackerIntakeClaim(ctx, string(id)); err != nil {
		return fmt.Errorf("release pending tracker intake session %s: %w", id, err)
	}
	return nil
}

// trackerIntakeSessionProgressed requires controller evidence, not merely a
// worktree or prepared prompt. Workspace setup happens before runtime creation,
// so treating it as delivery would strand an issue after a pre-launch crash.
func trackerIntakeSessionProgressed(rec domain.SessionRecord) bool {
	return rec.Metadata.RuntimeHandleID != "" ||
		rec.Metadata.AgentSessionID != "" ||
		rec.Metadata.ProviderConversationID != "" ||
		rec.Metadata.ControllerGeneration != "" ||
		rec.Metadata.NativeTranscriptPath != ""
}

// RecordTrackerIntakeHandled durably records one delivered source item. The
// primary key makes retries idempotent and preserves the first delivery facts.
func (s *Store) RecordTrackerIntakeHandled(ctx context.Context, record domain.TrackerIntakeHandledRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.RecordTrackerIntakeHandled(ctx, gen.RecordTrackerIntakeHandledParams{
		ProjectID:  string(record.ProjectID),
		IssueID:    string(record.IssueID),
		SessionID:  string(record.SessionID),
		RecordedAt: record.HandledAt,
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
			HandledAt: row.RecordedAt,
		})
	}
	return out, nil
}
