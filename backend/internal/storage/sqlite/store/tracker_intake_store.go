package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
	"github.com/aoagents/agent-orchestrator/backend/internal/trackerref"
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
		projectRow, err := q.GetProject(ctx, rec.ProjectID)
		if err != nil {
			return fmt.Errorf("load tracker intake project %s: %w", rec.ProjectID, err)
		}
		project := projectRowFromGen(projectRow)
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
		claims, err := q.ListTrackerIntakeClaims(ctx, string(rec.ProjectID))
		if err != nil {
			return fmt.Errorf("find tracker intake aliases %s/%s: %w", rec.ProjectID, rec.IssueID, err)
		}
		for _, claim := range claims {
			if !trackerref.Equivalent(domain.IssueID(claim.IssueID), rec.IssueID, project) {
				continue
			}
			if claim.State == "handled" {
				return fmt.Errorf("%w: %s/%s", ports.ErrTrackerIntakeHandled, rec.ProjectID, rec.IssueID)
			}
			return fmt.Errorf("%w: %s/%s", ports.ErrTrackerIntakeActive, rec.ProjectID, rec.IssueID)
		}

		issueRefs, err := q.ListLiveTrackerIssueReferences(ctx, optionalProjectID(rec.ProjectID))
		if err != nil {
			return fmt.Errorf("find live tracker issue session %s/%s: %w", rec.ProjectID, rec.IssueID, err)
		}
		for _, issueRef := range issueRefs {
			if trackerref.Equivalent(issueRef, rec.IssueID, project) {
				return fmt.Errorf("%w: %s/%s", ports.ErrTrackerIntakeActive, rec.ProjectID, rec.IssueID)
			}
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
// A missing or terminated session is released only when its recovery facts
// prove that no workspace, runtime, controller, or delivery may survive. Live
// rows remain pending for lifecycle reconciliation.
func (s *Store) ReconcileTrackerIntakeSession(ctx context.Context, id domain.SessionID, handledAt time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.reconcileTrackerIntakeSessionLocked(ctx, s.qw, id)
}

// ReconcileTrackerIntakeClaims settles every pending claim for a project before
// an observer poll chooses more work. It is the restart recovery path for a
// daemon that stopped between seed creation and launch completion.
func (s *Store) ReconcileTrackerIntakeClaims(ctx context.Context, projectID domain.ProjectID, _ time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	claims, err := s.qw.ListPendingTrackerIntakeClaims(ctx, string(projectID))
	if err != nil {
		return fmt.Errorf("list pending tracker intake claims for %s: %w", projectID, err)
	}
	for _, claim := range claims {
		if err := s.reconcileTrackerIntakeSessionLocked(ctx, s.qw, domain.SessionID(claim.SessionID)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) reconcileTrackerIntakeSessionLocked(ctx context.Context, q *gen.Queries, id domain.SessionID) error {
	claim, err := q.GetTrackerIntakeClaimBySession(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && claim.State == "handled") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reconcile tracker intake session %s: load claim: %w", id, err)
	}
	row, err := q.GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		reservation, pending, recoveryErr := getPendingTrackerIntakeRecovery(ctx, q, id)
		if recoveryErr != nil {
			return recoveryErr
		}
		if pending && !reservation.CleanupConfirmed && trackerIntakeRecoveryNeedsFence(reservation) {
			return nil
		}
		return s.deletePendingTrackerIntakeClaimLocked(ctx, q, id)
	}
	if err != nil {
		return fmt.Errorf("reconcile tracker intake session %s: load session: %w", id, err)
	}
	rec := rowToRecord(row)
	if rec.IsTerminated {
		reservation, pending, recoveryErr := getPendingTrackerIntakeRecovery(ctx, q, id)
		if recoveryErr != nil {
			return recoveryErr
		}
		if pending && reservation.CleanupConfirmed {
			return s.deletePendingTrackerIntakeClaimLocked(ctx, q, id)
		}
		// A terminated row can still carry a possible runtime/controller from an
		// uncertain cleanup. Only lifecycle code can prove that external owner
		// absent; metadata alone must never release or promote its claim. Runtime
		// candidates are deliberately conservative: a reservation is written
		// before Create, so lifecycle must probe it even when Create never returned.
		if trackerIntakeSessionHasExternalEffect(rec) || (pending && trackerIntakeRecoveryNeedsFence(reservation)) {
			return nil
		}
		return s.deletePendingTrackerIntakeClaimLocked(ctx, q, id)
	}
	// A live runtime or controller is ownership evidence, not proof that the
	// initial task reached the agent. Only CommitTrackerIntakeSession, called
	// after prompt delivery returns successfully, may mark this claim handled.
	return nil
}

func (s *Store) deletePendingTrackerIntakeClaimLocked(ctx context.Context, q *gen.Queries, id domain.SessionID) error {
	if _, err := q.DeletePendingTrackerIntakeClaim(ctx, string(id)); err != nil {
		return fmt.Errorf("release pending tracker intake session %s: %w", id, err)
	}
	return nil
}

func trackerIntakeSessionHasExternalEffect(rec domain.SessionRecord) bool {
	return rec.Metadata.WorkspacePath != "" ||
		rec.Metadata.RuntimeHandleID != "" ||
		rec.Metadata.AgentSessionID != "" ||
		rec.Metadata.ProviderConversationID != "" ||
		rec.Metadata.ControllerGeneration != "" ||
		rec.Metadata.NativeTranscriptPath != ""
}

// ReserveTrackerIntakeRecovery checkpoints the resource identities lifecycle
// code needs before an unattended launch can create an external controller.
// Rewriting the reservation is allowed as launch preparation moves from the
// workspace-only checkpoint to a concrete runtime/controller fence.
func (s *Store) ReserveTrackerIntakeRecovery(ctx context.Context, id domain.SessionID, reservation domain.TrackerIntakeRecoveryReservation) error {
	encoded, err := json.Marshal(normalizeTrackerIntakeRecovery(reservation))
	if err != nil {
		return fmt.Errorf("encode tracker intake recovery for %s: %w", id, err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetPendingTrackerIntakeRecovery(ctx, gen.SetPendingTrackerIntakeRecoveryParams{
		RecoveryState: string(encoded),
		SessionID:     string(id),
	})
	if err != nil {
		return fmt.Errorf("reserve tracker intake recovery for %s: %w", id, err)
	}
	if rows != 1 {
		return fmt.Errorf("reserve tracker intake recovery for %s: pending claim is missing", id)
	}
	return nil
}

// GetPendingTrackerIntakeRecovery returns the reservation only while id still
// owns a pending claim. A malformed record fails closed so startup cannot turn
// unreadable ownership evidence into permission to launch another worker.
func (s *Store) GetPendingTrackerIntakeRecovery(ctx context.Context, id domain.SessionID) (domain.TrackerIntakeRecoveryReservation, bool, error) {
	return getPendingTrackerIntakeRecovery(ctx, s.qr, id)
}

// ListPendingTrackerIntakeClaims returns every unresolved claim, including
// those whose session row is terminated or missing. Lifecycle recovery uses
// this durable worklist instead of inferring ownership from session liveness.
func (s *Store) ListPendingTrackerIntakeClaims(ctx context.Context) ([]domain.TrackerIntakePendingClaim, error) {
	rows, err := s.qr.ListAllPendingTrackerIntakeClaims(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending tracker intake claims: %w", err)
	}
	out := make([]domain.TrackerIntakePendingClaim, 0, len(rows))
	for _, row := range rows {
		var reservation domain.TrackerIntakeRecoveryReservation
		if err := json.Unmarshal([]byte(row.RecoveryState), &reservation); err != nil {
			return nil, fmt.Errorf("decode tracker intake recovery for %s: %w", row.SessionID, err)
		}
		out = append(out, domain.TrackerIntakePendingClaim{
			ProjectID:  domain.ProjectID(row.ProjectID),
			IssueID:    domain.IssueID(row.IssueID),
			SessionID:  domain.SessionID(row.SessionID),
			Recovery:   normalizeTrackerIntakeRecovery(reservation),
			RecordedAt: row.RecordedAt,
		})
	}
	return out, nil
}

func getPendingTrackerIntakeRecovery(ctx context.Context, q *gen.Queries, id domain.SessionID) (domain.TrackerIntakeRecoveryReservation, bool, error) {
	raw, err := q.GetPendingTrackerIntakeRecovery(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TrackerIntakeRecoveryReservation{}, false, nil
	}
	if err != nil {
		return domain.TrackerIntakeRecoveryReservation{}, false, fmt.Errorf("read tracker intake recovery for %s: %w", id, err)
	}
	var reservation domain.TrackerIntakeRecoveryReservation
	if err := json.Unmarshal([]byte(raw), &reservation); err != nil {
		return domain.TrackerIntakeRecoveryReservation{}, false, fmt.Errorf("decode tracker intake recovery for %s: %w", id, err)
	}
	return normalizeTrackerIntakeRecovery(reservation), true, nil
}

func trackerIntakeRecoveryNeedsFence(reservation domain.TrackerIntakeRecoveryReservation) bool {
	return reservation.DeliveryPossible || reservation.ControllerPossible ||
		strings.TrimSpace(reservation.WorkspacePath) != "" || len(reservation.RuntimeHandleIDs) != 0
}

func normalizeTrackerIntakeRecovery(reservation domain.TrackerIntakeRecoveryReservation) domain.TrackerIntakeRecoveryReservation {
	reservation.Branch = strings.TrimSpace(reservation.Branch)
	reservation.WorkspacePath = strings.TrimSpace(reservation.WorkspacePath)
	reservation.WorkspaceRepoPath = strings.TrimSpace(reservation.WorkspaceRepoPath)
	reservation.RuntimeLaunchID = strings.TrimSpace(reservation.RuntimeLaunchID)
	seen := make(map[string]struct{}, len(reservation.RuntimeHandleIDs))
	handles := make([]string, 0, len(reservation.RuntimeHandleIDs))
	for _, handle := range reservation.RuntimeHandleIDs {
		handle = strings.TrimSpace(handle)
		if handle == "" {
			continue
		}
		if _, ok := seen[handle]; ok {
			continue
		}
		seen[handle] = struct{}{}
		handles = append(handles, handle)
	}
	reservation.RuntimeHandleIDs = handles
	return reservation
}

// ReleaseTrackerIntakeSession removes id's pending claim after lifecycle code
// has proven that no worker/controller survives. Handled claims are immutable.
func (s *Store) ReleaseTrackerIntakeSession(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.deletePendingTrackerIntakeClaimLocked(ctx, s.qw, id)
}

// RecordTrackerIntakeHandled durably records one delivered source item. The
// primary key makes retries idempotent and preserves the first delivery facts.
func (s *Store) RecordTrackerIntakeHandled(ctx context.Context, record domain.TrackerIntakeHandledRecord) error {
	if project, ok, err := s.GetProject(ctx, string(record.ProjectID)); err != nil {
		return fmt.Errorf("record tracker intake handled %s/%s: load project: %w", record.ProjectID, record.IssueID, err)
	} else if ok {
		if canonical, resolved := trackerref.Resolve(record.IssueID, project); resolved {
			record.IssueID = canonical
		}
	}
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
	project, projectFound, projectErr := s.GetProject(ctx, string(projectID))
	if projectErr != nil {
		return nil, fmt.Errorf("list tracker intake handled for %s: load project: %w", projectID, projectErr)
	}
	out := make([]domain.TrackerIntakeHandledRecord, 0, len(rows))
	for _, row := range rows {
		issueID := domain.IssueID(row.IssueID)
		if projectFound {
			if canonical, resolved := trackerref.Resolve(issueID, project); resolved {
				issueID = canonical
			}
		}
		out = append(out, domain.TrackerIntakeHandledRecord{
			ProjectID: domain.ProjectID(row.ProjectID),
			IssueID:   issueID,
			SessionID: domain.SessionID(row.SessionID),
			HandledAt: row.RecordedAt,
		})
	}
	return out, nil
}
