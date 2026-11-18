package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ReconcileTrackerIntakeClaims retries cleanup from the durable claim ledger.
// Session rows are optional: a user may terminate or delete one while runtime
// or workspace ownership is still unresolved. Claims whose initial task may
// already have been delivered remain fenced and are never replayed.
func (m *Manager) ReconcileTrackerIntakeClaims(ctx context.Context) error {
	claims, err := m.store.ListPendingTrackerIntakeClaims(ctx)
	if err != nil {
		return err
	}
	var reconcileErr error
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return errors.Join(reconcileErr, err)
		}
		if claim.Recovery.DeliveryPossible {
			continue
		}
		rec, found, err := m.store.GetSession(ctx, claim.SessionID)
		if err != nil {
			reconcileErr = errors.Join(reconcileErr, fmt.Errorf("recover tracker intake %s: load session: %w", claim.SessionID, err))
			continue
		}
		if !found {
			rec = domain.SessionRecord{
				ID:        claim.SessionID,
				ProjectID: claim.ProjectID,
				IssueID:   claim.IssueID,
				Kind:      domain.KindWorker,
				Mode:      domain.SessionModeTUI,
			}
			if claim.Recovery.ControllerPossible {
				rec.Mode = domain.SessionModeChat
			}
		}
		err = m.reconcileInterruptedTrackerIntake(ctx, rec, claim.Recovery)
		if err != nil {
			reconcileErr = errors.Join(reconcileErr, err)
		}
	}
	return reconcileErr
}

// reconcileInterruptedTrackerIntake settles a launch whose claim never reached
// the explicit post-prompt commit. It never infers delivery from a controller
// handle. Instead it proves the possible controller absent, tears down the
// workspace, and only then lets the still-open tracker item retry.
func (m *Manager) reconcileInterruptedTrackerIntake(
	ctx context.Context,
	rec domain.SessionRecord,
	reservation domain.TrackerIntakeRecoveryReservation,
) error {
	rec = applyTrackerIntakeRecovery(rec, reservation)
	if reservation.DeliveryPossible {
		return nil
	}
	if reservation.CleanupConfirmed {
		return m.releaseInterruptedTrackerIntake(ctx, rec)
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if reservation.ControllerPossible || trackerIntakeHasChatEffect(rec) {
			if m.chat == nil {
				return fmt.Errorf("check session %s: pending tracker intake chat controller cannot be fenced", rec.ID)
			}
			if err := m.chat.StopChat(ctx, rec.ID); err != nil {
				return fmt.Errorf("check session %s: stop pending tracker intake chat controller: %w", rec.ID, err)
			}
		}
		return m.releaseInterruptedTrackerIntake(ctx, rec)
	}

	launchID := strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
	if launchID == "" {
		launchID = strings.TrimSpace(reservation.RuntimeLaunchID)
	}
	handleIDs := append([]string(nil), reservation.RuntimeHandleIDs...)
	if handleID := strings.TrimSpace(rec.Metadata.RuntimeHandleID); handleID != "" {
		handleIDs = append([]string{handleID}, handleIDs...)
	}
	handleIDs = uniqueTrackerIntakeHandles(handleIDs)
	if len(handleIDs) != 0 && launchID == "" {
		return fmt.Errorf("check session %s: pending tracker intake runtime has no generation fence", rec.ID)
	}
	for _, handleID := range handleIDs {
		ref := ports.FencedRuntimeRef{
			Handle:         ports.RuntimeHandle{ID: handleID},
			SessionID:      rec.ID,
			Generation:     launchID,
			NativeIdentity: strings.TrimSpace(rec.Metadata.AgentSessionID),
		}
		probe := m.runtime.ProbeFencedRuntime(ctx, ref)
		switch probe.Liveness {
		case ports.FencedDead:
			// Exact absence is sufficient proof that no duplicate worker survives.
		case ports.FencedAlive:
			if err := m.stopSourceRuntime(ctx, ref); err != nil {
				return fmt.Errorf("check session %s: stop pending tracker intake runtime: %w", rec.ID, err)
			}
		default:
			return fmt.Errorf("check session %s: pending tracker intake runtime remains unresolved: %s", rec.ID, probe.Reason)
		}
	}
	if len(handleIDs) == 0 && (trackerIntakeHasTerminalEffect(rec) || launchID != "") {
		return fmt.Errorf("check session %s: pending tracker intake native identity has no runtime fence", rec.ID)
	}

	return m.releaseInterruptedTrackerIntake(ctx, rec)
}

func (m *Manager) releaseInterruptedTrackerIntake(ctx context.Context, rec domain.SessionRecord) error {
	if strings.TrimSpace(rec.Metadata.WorkspacePath) == "" {
		if err := m.confirmTrackerIntakeCleanup(ctx, rec.ID); err != nil {
			return err
		}
		m.rollbackSpawnSeedRow(ctx, rec.ID)
		return m.store.ReleaseTrackerIntakeSession(ctx, rec.ID)
	}

	rows, isProject, err := m.workspaceProjectRows(ctx, rec)
	if err != nil {
		return fmt.Errorf("check session %s: load pending tracker intake workspace: %w", rec.ID, err)
	}
	var workspaceProject *ports.WorkspaceProjectInfo
	if isProject {
		workspaceProject = &ports.WorkspaceProjectInfo{Root: workspaceInfo(rec), Worktrees: rows}
	}
	if !m.destroySpawnWorkspace(ctx, workspaceInfo(rec), workspaceProject) {
		return fmt.Errorf("check session %s: pending tracker intake workspace cleanup remains unresolved", rec.ID)
	}
	m.cleanupAgentWorkspace(ctx, rec, rec.Metadata.WorkspacePath)
	if err := m.confirmTrackerIntakeCleanup(ctx, rec.ID); err != nil {
		return err
	}
	m.rollbackSpawnSeedRow(ctx, rec.ID)
	if err := m.store.ReleaseTrackerIntakeSession(ctx, rec.ID); err != nil {
		return fmt.Errorf("check session %s: release pending tracker intake claim: %w", rec.ID, err)
	}
	return nil
}

func trackerIntakeRecoveryForWorkspace(ws ports.WorkspaceInfo) domain.TrackerIntakeRecoveryReservation {
	return domain.TrackerIntakeRecoveryReservation{
		Branch:            ws.Branch,
		WorkspacePath:     ws.Path,
		WorkspaceRepoPath: ws.RepoPath,
	}
}

func applyTrackerIntakeRecovery(rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) domain.SessionRecord {
	if rec.Metadata.Branch == "" {
		rec.Metadata.Branch = reservation.Branch
	}
	if rec.Metadata.WorkspacePath == "" {
		rec.Metadata.WorkspacePath = reservation.WorkspacePath
	}
	if rec.Metadata.WorkspaceRepoPath == "" {
		rec.Metadata.WorkspaceRepoPath = reservation.WorkspaceRepoPath
	}
	if rec.Metadata.RuntimeLaunchID == "" {
		rec.Metadata.RuntimeLaunchID = reservation.RuntimeLaunchID
	}
	return rec
}

func uniqueTrackerIntakeHandles(handles []string) []string {
	seen := make(map[string]struct{}, len(handles))
	unique := make([]string, 0, len(handles))
	for _, handle := range handles {
		handle = strings.TrimSpace(handle)
		if handle == "" {
			continue
		}
		if _, exists := seen[handle]; exists {
			continue
		}
		seen[handle] = struct{}{}
		unique = append(unique, handle)
	}
	return unique
}

func trackerIntakeHasTerminalEffect(rec domain.SessionRecord) bool {
	return strings.TrimSpace(rec.Metadata.AgentSessionID) != "" ||
		strings.TrimSpace(rec.Metadata.NativeTranscriptPath) != ""
}

func trackerIntakeHasChatEffect(rec domain.SessionRecord) bool {
	return strings.TrimSpace(rec.Metadata.ProviderConversationID) != "" ||
		strings.TrimSpace(rec.Metadata.ControllerGeneration) != ""
}
