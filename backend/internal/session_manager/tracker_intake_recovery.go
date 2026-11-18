package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const trackerIntakeWorkspaceCreationFence = "tracker-intake-creation-intent"

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
		if err := m.reconcileTrackerIntakeClaim(ctx, claim); err != nil {
			reconcileErr = errors.Join(reconcileErr, err)
		}
	}
	return reconcileErr
}

// ScheduleTrackerIntakeClaimReconciliation starts each pending claim as its own
// daemon-owned job. A slow pre-remove hook or inconclusive runtime probe keeps
// only that claim fenced; it cannot block startup or intake for other projects.
func (m *Manager) ScheduleTrackerIntakeClaimReconciliation(ctx context.Context) error {
	claims, err := m.store.ListPendingTrackerIntakeClaims(ctx)
	if err != nil {
		return err
	}
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !m.beginTrackerIntakeRecovery(claim.SessionID) {
			continue
		}
		m.runInBackground(func() {
			defer m.endTrackerIntakeRecovery(claim.SessionID)
			if err := m.reconcileTrackerIntakeClaim(m.backgroundContext, claim); err != nil {
				m.logger.Warn("tracker intake: claim cleanup deferred", "sessionID", claim.SessionID, "projectID", claim.ProjectID, "error", err)
			}
		})
	}
	return nil
}

func (m *Manager) beginTrackerIntakeRecovery(id domain.SessionID) bool {
	m.trackerIntakeRecoveryMu.Lock()
	defer m.trackerIntakeRecoveryMu.Unlock()
	if m.trackerIntakeRecovering == nil {
		m.trackerIntakeRecovering = make(map[domain.SessionID]struct{})
	}
	if _, exists := m.trackerIntakeRecovering[id]; exists {
		return false
	}
	m.trackerIntakeRecovering[id] = struct{}{}
	return true
}

func (m *Manager) endTrackerIntakeRecovery(id domain.SessionID) {
	m.trackerIntakeRecoveryMu.Lock()
	delete(m.trackerIntakeRecovering, id)
	m.trackerIntakeRecoveryMu.Unlock()
}

func (m *Manager) reconcileTrackerIntakeClaim(ctx context.Context, claim domain.TrackerIntakePendingClaim) error {
	rec, found, err := m.store.GetSession(ctx, claim.SessionID)
	if err != nil {
		return fmt.Errorf("recover tracker intake %s: load session: %w", claim.SessionID, err)
	}
	if !found {
		rec = domain.SessionRecord{
			ID: claim.SessionID, ProjectID: claim.ProjectID, IssueID: claim.IssueID,
			Kind: domain.KindWorker, Mode: domain.SessionModeTUI,
		}
		if claim.Recovery.ControllerPossible {
			rec.Mode = domain.SessionModeChat
		}
	}
	if claim.Recovery.DeliveryPossible && found && !rec.IsTerminated && !claim.Recovery.CapacityReleased {
		if _, err := m.adoptTrackerIntakeRuntime(ctx, rec, claim.Recovery); err != nil {
			return err
		}
		// A live row still owns a possibly delivered task. Adoption makes a
		// concrete terminal runtime controllable; all other ownership remains
		// fenced without replay or teardown.
		return nil
	}
	return m.reconcileInterruptedTrackerIntake(ctx, rec, claim.Recovery)
}

func (m *Manager) adoptTrackerIntakeRuntime(ctx context.Context, rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) (bool, error) {
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat || strings.TrimSpace(rec.Metadata.RuntimeHandleID) != "" {
		return false, nil
	}
	handles := uniqueTrackerIntakeHandles(reservation.RuntimeHandleIDs)
	launchID := strings.TrimSpace(reservation.RuntimeLaunchID)
	if len(handles) != 1 || launchID == "" {
		return false, nil
	}
	rec = applyTrackerIntakeRecovery(rec, reservation)
	metadata := rec.Metadata
	metadata.RuntimeHandleID = handles[0]
	metadata.RuntimeLaunchID = launchID
	if err := m.lcm.MarkSpawned(ctx, rec.ID, metadata); err != nil {
		return false, fmt.Errorf("recover tracker intake %s: adopt reserved runtime: %w", rec.ID, err)
	}
	return true, nil
}

func trackerIntakeHasRuntimeEvidence(rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) bool {
	return strings.TrimSpace(rec.Metadata.RuntimeHandleID) != "" ||
		strings.TrimSpace(rec.Metadata.RuntimeLaunchID) != "" ||
		len(uniqueTrackerIntakeHandles(reservation.RuntimeHandleIDs)) != 0 ||
		strings.TrimSpace(reservation.RuntimeLaunchID) != "" || reservation.DeliveryPossible
}

// stopTrackerIntakeRuntimeOwners proves every candidate from the durable
// reservation absent or stops its exact launch. A row can hold only the
// selected handle, so ignoring the other candidates would let Kill release the
// claim and workspace while a partially-created fallback remained alive.
func (m *Manager) stopTrackerIntakeRuntimeOwners(
	ctx context.Context,
	rec domain.SessionRecord,
	reservation domain.TrackerIntakeRecoveryReservation,
) error {
	launchID := strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
	if launchID == "" {
		launchID = strings.TrimSpace(reservation.RuntimeLaunchID)
	}
	handleIDs := append([]string(nil), reservation.RuntimeHandleIDs...)
	if handleID := strings.TrimSpace(rec.Metadata.RuntimeHandleID); handleID != "" {
		handleIDs = append([]string{handleID}, handleIDs...)
	}
	handleIDs = uniqueTrackerIntakeHandles(handleIDs)
	if len(handleIDs) == 0 {
		if reservation.DeliveryPossible || launchID != "" {
			return fmt.Errorf("pending tracker intake native identity has no runtime fence")
		}
		return nil
	}
	if launchID == "" {
		return fmt.Errorf("pending tracker intake runtime has no generation fence")
	}
	for _, handleID := range handleIDs {
		ref := ports.FencedRuntimeRef{
			Handle: ports.RuntimeHandle{ID: handleID}, SessionID: rec.ID,
			Generation: launchID, NativeIdentity: strings.TrimSpace(rec.Metadata.AgentSessionID),
		}
		probe := m.runtime.ProbeFencedRuntime(ctx, ref)
		switch probe.Liveness {
		case ports.FencedDead:
			continue
		case ports.FencedAlive:
			if err := m.stopSourceRuntime(ctx, ref); err != nil {
				return fmt.Errorf("stop pending tracker intake runtime %s: %w", handleID, err)
			}
		default:
			return fmt.Errorf("pending tracker intake runtime %s remains unresolved: %s", handleID, probe.Reason)
		}
	}
	return nil
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
	if reservation.CleanupConfirmed {
		m.rollbackSpawnSeedRow(ctx, rec.ID)
		return m.settleInterruptedTrackerIntakeClaim(ctx, rec.ID, reservation)
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if !reservation.CapacityReleased && (reservation.ControllerPossible || trackerIntakeHasChatEffect(rec)) {
			if m.chat == nil {
				return fmt.Errorf("check session %s: pending tracker intake chat controller cannot be fenced", rec.ID)
			}
			if err := m.chat.StopChat(ctx, rec.ID); err != nil {
				return fmt.Errorf("check session %s: stop pending tracker intake chat controller: %w", rec.ID, err)
			}
		}
		if !reservation.CapacityReleased {
			if err := m.markTrackerIntakeCapacityReleased(ctx, rec.ID); err != nil {
				return err
			}
			reservation.CapacityReleased = true
		}
		return m.releaseInterruptedTrackerIntake(ctx, rec, reservation)
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
	if !reservation.CapacityReleased && len(handleIDs) != 0 && launchID == "" {
		return fmt.Errorf("check session %s: pending tracker intake runtime has no generation fence", rec.ID)
	}
	if !reservation.CapacityReleased {
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
	}
	if !reservation.CapacityReleased && len(handleIDs) == 0 && (reservation.DeliveryPossible || trackerIntakeHasTerminalEffect(rec) || launchID != "") {
		return fmt.Errorf("check session %s: pending tracker intake native identity has no runtime fence", rec.ID)
	}
	if !reservation.CapacityReleased {
		if err := m.markTrackerIntakeCapacityReleased(ctx, rec.ID); err != nil {
			return err
		}
		reservation.CapacityReleased = true
	}

	return m.releaseInterruptedTrackerIntake(ctx, rec, reservation)
}

func (m *Manager) releaseInterruptedTrackerIntake(ctx context.Context, rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) error {
	if strings.TrimSpace(rec.Metadata.WorkspacePath) == "" {
		if err := m.confirmTrackerIntakeCleanup(ctx, rec.ID); err != nil {
			return err
		}
		m.rollbackSpawnSeedRow(ctx, rec.ID)
		return m.settleInterruptedTrackerIntakeClaim(ctx, rec.ID, reservation)
	}

	rows, isProject, err := m.trackerIntakeWorkspaceRows(ctx, rec)
	if err != nil {
		return fmt.Errorf("check session %s: load pending tracker intake workspace: %w", rec.ID, err)
	}
	var workspaceProject *ports.WorkspaceProjectInfo
	rootWorkspace := trackerIntakeWorkspaceInfo(rec, reservation)
	if isProject {
		workspaceProject = &ports.WorkspaceProjectInfo{Root: rootWorkspace, Worktrees: rows}
	}
	if !m.destroySpawnWorkspace(ctx, rootWorkspace, workspaceProject) {
		return fmt.Errorf("check session %s: pending tracker intake workspace cleanup remains unresolved", rec.ID)
	}
	m.cleanupAgentWorkspace(ctx, rec, rec.Metadata.WorkspacePath)
	if err := m.confirmTrackerIntakeCleanup(ctx, rec.ID); err != nil {
		return err
	}
	m.rollbackSpawnSeedRow(ctx, rec.ID)
	return m.settleInterruptedTrackerIntakeClaim(ctx, rec.ID, reservation)
}

func (m *Manager) trackerIntakeWorkspaceRows(ctx context.Context, rec domain.SessionRecord) ([]ports.WorkspaceRepoInfo, bool, error) {
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return nil, false, err
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return m.workspaceProjectRows(ctx, rec)
	}
	markers, err := m.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return nil, false, err
	}
	rows, err := m.workspaceProjectRestoreRowsFromMarkers(ctx, project, rec, markers)
	for i := range rows {
		if rows[i].BaseSHA == "" && rows[i].CreationSHA == "" {
			rows[i].BaseSHA = trackerIntakeWorkspaceCreationFence
		}
	}
	return rows, true, err
}

func (m *Manager) settleInterruptedTrackerIntakeClaim(ctx context.Context, id domain.SessionID, reservation domain.TrackerIntakeRecoveryReservation) error {
	if reservation.DeliveryPossible {
		if err := m.store.CommitTrackerIntakeSession(ctx, id, m.clock()); err != nil {
			return fmt.Errorf("check session %s: preserve delivered tracker intake claim: %w", id, err)
		}
		return nil
	}
	if err := m.store.ReleaseTrackerIntakeSession(ctx, id); err != nil {
		return fmt.Errorf("check session %s: release pending tracker intake claim: %w", id, err)
	}
	return nil
}

func trackerIntakeRecoveryForWorkspace(ws ports.WorkspaceInfo) domain.TrackerIntakeRecoveryReservation {
	return domain.TrackerIntakeRecoveryReservation{
		Branch:                  ws.Branch,
		WorkspacePath:           ws.Path,
		WorkspaceRepoPath:       ws.RepoPath,
		WorkspaceCreationIntent: strings.TrimSpace(ws.Path) != "",
	}
}

func trackerIntakeWorkspaceInfo(rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) ports.WorkspaceInfo {
	info := workspaceInfo(rec)
	if reservation.WorkspaceCreationIntent && info.BaseSHA == "" {
		info.BaseSHA = trackerIntakeWorkspaceCreationFence
	}
	return info
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
