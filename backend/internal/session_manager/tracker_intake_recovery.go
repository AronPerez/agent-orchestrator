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
	reservation, pending, err := m.store.GetPendingTrackerIntakeRecovery(ctx, claim.SessionID)
	if err != nil {
		return fmt.Errorf("recover tracker intake %s: inspect claim: %w", claim.SessionID, err)
	}
	if !pending {
		return nil
	}
	claim.Recovery = reservation
	rec, found, err := m.store.GetSession(ctx, claim.SessionID)
	if err != nil {
		return fmt.Errorf("recover tracker intake %s: inspect session: %w", claim.SessionID, err)
	}
	if found && (rec.ProjectID != claim.ProjectID || rec.IssueID != claim.IssueID) {
		return nil
	}
	if found && trackerIntakeClaimDefersToSessionHealth(rec, reservation) {
		// This path performs no recovery mutation. In particular, do not reserve
		// the session operation: startup health needs that fence to reconnect a
		// surviving Chat controller and settle the delivery claim.
		return nil
	}
	if !found {
		rec = domain.SessionRecord{
			ID: claim.SessionID, ProjectID: claim.ProjectID, IssueID: claim.IssueID,
			Kind: domain.KindWorker, Mode: domain.SessionModeTUI,
		}
		if reservation.ControllerPossible {
			rec.Mode = domain.SessionModeChat
		}
	}
	if trackerIntakeRuntimeFenceProbeable(rec, reservation) {
		// Runtime probes may block on an unavailable backend. Probe before taking
		// the session-operation fence, then acquire it and revalidate the durable
		// generation before publishing or cleaning anything up. This lets an
		// explicit Kill win while recovery is observing a stale owner.
		return m.reconcileLiveTrackerIntakeRuntime(ctx, claim, rec)
	}

	if err := m.beginAgentOperation(ctx, claim.SessionID, agentOperationReconcile); err != nil {
		return fmt.Errorf("recover tracker intake %s: reserve reconciliation: %w", claim.SessionID, err)
	}
	defer m.endAgentOperation(claim.SessionID, agentOperationReconcile)

	reservation, pending, err = m.store.GetPendingTrackerIntakeRecovery(ctx, claim.SessionID)
	if err != nil {
		return fmt.Errorf("recover tracker intake %s: reload claim: %w", claim.SessionID, err)
	}
	if !pending {
		return nil
	}
	claim.Recovery = reservation
	rec, found, err = m.store.GetSession(ctx, claim.SessionID)
	if err != nil {
		return fmt.Errorf("recover tracker intake %s: load session: %w", claim.SessionID, err)
	}
	if found && (rec.ProjectID != claim.ProjectID || rec.IssueID != claim.IssueID) {
		return nil
	}
	if found && !rec.IsTerminated && claim.Recovery.CapacityReleased && !claim.Recovery.ReleasesCapacityFor(rec) {
		// Resume rotated the owner after an earlier generation was proved
		// stopped, so that proof cannot authorize cleanup of this workspace.
		// A possibly-delivered claim is already a no-replay fence and can become
		// handled immediately; an undelivered restored owner stays pending and
		// charged until the user stops it or lifecycle can fence it explicitly.
		if claim.Recovery.DeliveryPossible {
			return m.store.CommitTrackerIntakeSession(ctx, rec.ID, m.clock())
		}
		return fmt.Errorf("recover tracker intake %s: released generation was replaced by a live owner", rec.ID)
	}
	if found && !rec.IsTerminated && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat &&
		rec.Activity.State == domain.ActivityExited && trackerIntakeChatReservationMatchesSession(rec, claim.Recovery) {
		if claim.Recovery.CapacityReleased && claim.Recovery.ReleasesCapacityFor(rec) {
			if claim.Recovery.DeliveryPossible {
				return m.store.CommitStoppedTrackerIntakeSession(ctx, rec.ID, m.clock())
			}
			if claim.Recovery.RelaunchPublished {
				return nil
			}
		} else if !claim.Recovery.CapacityReleased {
			// ActivityExited can mean only that AO lost its attachment to a
			// persistent provider. Reconnect-only recovery is the ownership proof:
			// a live host is reattached, a missing host releases capacity, and an
			// inconclusive probe leaves the claim charged.
			recoveryCtx, cancel := context.WithTimeout(ctx, m.statusVerificationLimit)
			defer cancel()
			return m.checkSessionHealth(recoveryCtx, rec)
		}
	}
	if found && !rec.IsTerminated && rec.Activity.State == domain.ActivityExited &&
		trackerIntakeRuntimeReservationMatchesSession(rec, claim.Recovery) &&
		claim.Recovery.CapacityReleased && claim.Recovery.ReleasesCapacityFor(rec) {
		// An exited published session stays resumable whether the user invoked
		// Exit Agent or the agent ended naturally. A delivered task becomes handled
		// without consuming capacity; an undelivered native relaunch retains its
		// pending no-duplicate fence until the user resumes or terminates it.
		if claim.Recovery.DeliveryPossible {
			return m.store.CommitStoppedTrackerIntakeSession(ctx, rec.ID, m.clock())
		}
		if claim.Recovery.RelaunchPublished {
			return nil
		}
	}
	if found && !rec.IsTerminated && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat &&
		claim.Recovery.DeliveryPossible && claim.Recovery.CapacityReleased && rec.Activity.State == domain.ActivityExited {
		// A pre-atomic build may have persisted the released-capacity fact just
		// before crashing ahead of claim promotion. The exited generation is
		// already fenced, so finish the no-replay transition without retiring its
		// resumable workspace.
		return m.store.CommitStoppedTrackerIntakeSession(ctx, rec.ID, m.clock())
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
	if found && trackerIntakeClaimDefersToSessionHealth(rec, claim.Recovery) {
		return nil
	}
	if found && !rec.IsTerminated && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat &&
		claim.Recovery.DeliveryPossible && !claim.Recovery.CapacityReleased && rec.Activity.State == domain.ActivityExited {
		// The initial turn may have crossed the provider boundary, but this
		// controller exited after startup. Reconnect only to prove the provider
		// host present or absent; never replay the prompt. The per-claim job and
		// operation fence isolate a slow provider check from unrelated projects.
		recoveryCtx, cancel := context.WithTimeout(ctx, m.statusVerificationLimit)
		defer cancel()
		return m.checkSessionHealth(recoveryCtx, rec)
	}
	return m.reconcileInterruptedTrackerIntake(ctx, rec, claim.Recovery)
}

func trackerIntakeRuntimeFenceProbeable(rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) bool {
	return domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat &&
		!reservation.CapacityReleased &&
		(reservation.DeliveryPossible || reservation.RelaunchPublished) &&
		strings.TrimSpace(reservation.RuntimeLaunchID) != "" &&
		len(uniqueTrackerIntakeHandles(reservation.RuntimeHandleIDs)) != 0
}

func trackerIntakeRuntimeFenceMatches(left, right domain.TrackerIntakeRecoveryReservation) bool {
	if strings.TrimSpace(left.RuntimeLaunchID) != strings.TrimSpace(right.RuntimeLaunchID) {
		return false
	}
	leftHandles := uniqueTrackerIntakeHandles(left.RuntimeHandleIDs)
	rightHandles := uniqueTrackerIntakeHandles(right.RuntimeHandleIDs)
	if len(leftHandles) != len(rightHandles) {
		return false
	}
	for _, handle := range leftHandles {
		if !trackerIntakeHandleReserved(rightHandles, handle) {
			return false
		}
	}
	return true
}

func trackerIntakeClaimDefersToSessionHealth(rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) bool {
	if reservation.CapacityReleased || rec.IsTerminated {
		return false
	}
	if rec.Activity.State == domain.ActivityExited {
		return false
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if trackerIntakeChatReservationMatchesSession(rec, reservation) {
			return true
		}
		return reservation.DeliveryPossible && rec.Activity.State != domain.ActivityExited
	}
	if reservation.RelaunchPublished && trackerIntakeRuntimeReservationMatchesSession(rec, reservation) {
		return rec.Activity.State != domain.ActivityExited
	}
	if !reservation.DeliveryPossible {
		return false
	}
	// A reserved replacement generation takes precedence over stale row
	// metadata left behind by a crash before MarkSpawned. Do not defer that row
	// to ordinary health, which can inspect only the old runtime owner.
	if launchID := strings.TrimSpace(reservation.RuntimeLaunchID); launchID != "" {
		if launchID != strings.TrimSpace(rec.Metadata.RuntimeLaunchID) ||
			!trackerIntakeHandleReserved(reservation.RuntimeHandleIDs, rec.Metadata.RuntimeHandleID) {
			return false
		}
	}
	return strings.TrimSpace(rec.Metadata.RuntimeHandleID) != ""
}

func trackerIntakeClaimNeedsStartupHealth(rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) bool {
	if rec.IsTerminated || reservation.CapacityReleased || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return false
	}
	return reservation.DeliveryPossible || trackerIntakeChatReservationMatchesSession(rec, reservation)
}

func trackerIntakeRuntimeReservationMatchesSession(rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) bool {
	launchID := strings.TrimSpace(reservation.RuntimeLaunchID)
	handleID := strings.TrimSpace(rec.Metadata.RuntimeHandleID)
	return launchID != "" && handleID != "" &&
		launchID == strings.TrimSpace(rec.Metadata.RuntimeLaunchID) &&
		trackerIntakeHandleReserved(reservation.RuntimeHandleIDs, handleID)
}

func trackerIntakeChatReservationMatchesSession(rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) bool {
	generation := strings.TrimSpace(reservation.ControllerGeneration)
	return generation != "" && generation == strings.TrimSpace(rec.Metadata.ControllerGeneration)
}

func (m *Manager) reconcileLiveTrackerIntakeRuntime(ctx context.Context, claim domain.TrackerIntakePendingClaim, rec domain.SessionRecord) error {
	reservation := claim.Recovery
	handles := uniqueTrackerIntakeHandles(reservation.RuntimeHandleIDs)
	launchID := strings.TrimSpace(reservation.RuntimeLaunchID)
	if len(handles) == 0 || launchID == "" {
		return fmt.Errorf("recover tracker intake %s: possible delivery has no complete runtime fence", rec.ID)
	}
	alive := make([]string, 0, 1)
	for _, handleID := range handles {
		probe := m.runtime.ProbeFencedRuntime(ctx, ports.FencedRuntimeRef{
			Handle: ports.RuntimeHandle{ID: handleID}, SessionID: rec.ID,
			Generation: launchID, NativeIdentity: strings.TrimSpace(rec.Metadata.AgentSessionID),
		})
		switch probe.Liveness {
		case ports.FencedAlive:
			alive = append(alive, handleID)
		case ports.FencedDead:
		case ports.FencedUnknown:
			return fmt.Errorf("recover tracker intake %s: runtime %s remains unresolved: %s", rec.ID, handleID, probe.Reason)
		default:
			return fmt.Errorf("recover tracker intake %s: runtime %s returned invalid liveness %q", rec.ID, handleID, probe.Liveness)
		}
	}
	if len(alive) > 1 {
		return fmt.Errorf("recover tracker intake %s: multiple reserved runtimes remain alive", rec.ID)
	}
	if err := m.beginAgentOperation(ctx, claim.SessionID, agentOperationReconcile); err != nil {
		return fmt.Errorf("recover tracker intake %s: reserve reconciliation: %w", claim.SessionID, err)
	}
	defer m.endAgentOperation(claim.SessionID, agentOperationReconcile)

	current, pending, err := m.store.GetPendingTrackerIntakeRecovery(ctx, claim.SessionID)
	if err != nil {
		return fmt.Errorf("recover tracker intake %s: reload claim after runtime probe: %w", claim.SessionID, err)
	}
	if !pending || current.CapacityReleased ||
		(!current.DeliveryPossible && !current.RelaunchPublished) ||
		!trackerIntakeRuntimeFenceMatches(reservation, current) {
		return nil
	}
	currentRec, found, err := m.store.GetSession(ctx, claim.SessionID)
	if err != nil {
		return fmt.Errorf("recover tracker intake %s: reload session after runtime probe: %w", claim.SessionID, err)
	}
	if found && (currentRec.ProjectID != claim.ProjectID || currentRec.IssueID != claim.IssueID) {
		return nil
	}
	claim.Recovery = current
	if len(alive) == 1 {
		_, err = m.adoptTrackerIntakeRuntime(ctx, claim, alive[0])
		return err
	}
	// Every reserved candidate for this exact generation is absent. Persist the
	// stopped-generation proof before workspace cleanup so the cleanup path does
	// not probe the same owner again and unrelated issues can use the freed slot.
	if err := m.markTrackerIntakeCapacityReleased(ctx, claim.SessionID); err != nil {
		return fmt.Errorf("recover tracker intake %s: release stopped runtime capacity: %w", claim.SessionID, err)
	}
	settled, err := m.settleExitedTrackerIntakeRuntime(ctx, claim)
	if err != nil || settled {
		return err
	}
	return m.settleAbsentTrackerIntakeRuntime(ctx, claim)
}

// settleExitedTrackerIntakeRuntime preserves a published session after either
// Exit Agent or a natural agent exit. The caller has already proved every
// reserved runtime for this generation absent. Delivered work becomes a
// handled, capacity-released claim atomically; an undelivered published
// relaunch stays pending as an issue-level fence without occupying a slot.
func (m *Manager) settleExitedTrackerIntakeRuntime(ctx context.Context, claim domain.TrackerIntakePendingClaim) (bool, error) {
	rec, found, err := m.store.GetSession(ctx, claim.SessionID)
	if err != nil {
		return false, fmt.Errorf("recover tracker intake %s: reload stopped session: %w", claim.SessionID, err)
	}
	reservation, pending, err := m.store.GetPendingTrackerIntakeRecovery(ctx, claim.SessionID)
	if err != nil {
		return false, fmt.Errorf("recover tracker intake %s: reload stopped claim: %w", claim.SessionID, err)
	}
	if !found || !pending || rec.IsTerminated || rec.Activity.State != domain.ActivityExited ||
		rec.ProjectID != claim.ProjectID || rec.IssueID != claim.IssueID ||
		!trackerIntakeRuntimeReservationMatchesSession(rec, reservation) {
		return false, nil
	}
	if reservation.DeliveryPossible {
		if err := m.store.CommitStoppedTrackerIntakeSession(ctx, rec.ID, m.clock()); err != nil {
			return true, fmt.Errorf("recover tracker intake %s: commit stopped delivery: %w", rec.ID, err)
		}
		return true, nil
	}
	if !reservation.RelaunchPublished {
		return false, nil
	}
	if !reservation.CapacityReleased {
		if err := m.markTrackerIntakeCapacityReleased(ctx, rec.ID); err != nil {
			return true, fmt.Errorf("recover tracker intake %s: release stopped relaunch capacity: %w", rec.ID, err)
		}
	}
	return true, nil
}

func (m *Manager) adoptTrackerIntakeRuntime(ctx context.Context, claim domain.TrackerIntakePendingClaim, handleID string) (bool, error) {
	rec, found, err := m.store.GetSession(ctx, claim.SessionID)
	if err != nil {
		return false, fmt.Errorf("recover tracker intake %s: reload session: %w", claim.SessionID, err)
	}
	reservation, pending, err := m.store.GetPendingTrackerIntakeRecovery(ctx, claim.SessionID)
	if err != nil {
		return false, fmt.Errorf("recover tracker intake %s: reload claim: %w", claim.SessionID, err)
	}
	if !found || !pending || reservation.CapacityReleased ||
		(!reservation.DeliveryPossible && !reservation.RelaunchPublished) ||
		rec.ProjectID != claim.ProjectID || rec.IssueID != claim.IssueID {
		return false, nil
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		return false, nil
	}
	if trackerIntakeRuntimeReservationMatchesSession(rec, reservation) &&
		!rec.IsTerminated && rec.Activity.State != domain.ActivityExited {
		return false, nil
	}
	launchID := strings.TrimSpace(reservation.RuntimeLaunchID)
	if launchID == "" || !trackerIntakeHandleReserved(reservation.RuntimeHandleIDs, handleID) {
		return false, nil
	}
	rec = applyTrackerIntakeRecovery(rec, reservation)
	metadata := rec.Metadata
	metadata.RuntimeHandleID = handleID
	metadata.RuntimeLaunchID = launchID
	if err := m.lcm.MarkSpawned(ctx, rec.ID, metadata); err != nil {
		return false, fmt.Errorf("recover tracker intake %s: adopt reserved runtime: %w", rec.ID, err)
	}
	return true, nil
}

func (m *Manager) settleAbsentTrackerIntakeRuntime(ctx context.Context, claim domain.TrackerIntakePendingClaim) error {
	rec, found, err := m.store.GetSession(ctx, claim.SessionID)
	if err != nil {
		return fmt.Errorf("recover tracker intake %s: reload session: %w", claim.SessionID, err)
	}
	reservation, pending, err := m.store.GetPendingTrackerIntakeRecovery(ctx, claim.SessionID)
	if err != nil {
		return fmt.Errorf("recover tracker intake %s: reload claim: %w", claim.SessionID, err)
	}
	if !pending {
		return nil
	}
	if !found {
		rec = domain.SessionRecord{ID: claim.SessionID, ProjectID: claim.ProjectID, IssueID: claim.IssueID, Kind: domain.KindWorker, Mode: domain.SessionModeTUI}
	}
	return m.reconcileInterruptedTrackerIntake(ctx, rec, reservation)
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
	launchID := strings.TrimSpace(reservation.RuntimeLaunchID)
	if launchID == "" {
		launchID = strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
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

	launchID := strings.TrimSpace(reservation.RuntimeLaunchID)
	if launchID == "" {
		launchID = strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
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
		Branch:            ws.Branch,
		WorkspacePath:     ws.Path,
		WorkspaceRepoPath: ws.RepoPath,
	}
}

func trackerIntakeRecoveryForWorkspaceCreationIntent(ws ports.WorkspaceInfo) domain.TrackerIntakeRecoveryReservation {
	reservation := trackerIntakeRecoveryForWorkspace(ws)
	reservation.WorkspaceCreationIntent = strings.TrimSpace(ws.Path) != ""
	return reservation
}

func trackerIntakeWorkspaceInfo(rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) ports.WorkspaceInfo {
	info := workspaceInfo(rec)
	if reservation.WorkspaceCreationIntent && info.BaseSHA == "" {
		info.BaseSHA = trackerIntakeWorkspaceCreationFence
	}
	return info
}

func trackerIntakeCleanupHooksSafe(ws ports.WorkspaceInfo, project *ports.WorkspaceProjectInfo) bool {
	if ws.BaseSHA == trackerIntakeWorkspaceCreationFence {
		return false
	}
	if project == nil {
		return true
	}
	for _, repo := range project.Worktrees {
		if repo.BaseSHA == trackerIntakeWorkspaceCreationFence || repo.CreationSHA == trackerIntakeWorkspaceCreationFence {
			return false
		}
	}
	return true
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
	if reservation.RuntimeLaunchID != "" {
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

func trackerIntakeHandleReserved(handles []string, candidate string) bool {
	candidate = strings.TrimSpace(candidate)
	for _, handle := range handles {
		if strings.TrimSpace(handle) == candidate {
			return true
		}
	}
	return false
}

func trackerIntakeHasTerminalEffect(rec domain.SessionRecord) bool {
	return strings.TrimSpace(rec.Metadata.AgentSessionID) != "" ||
		strings.TrimSpace(rec.Metadata.NativeTranscriptPath) != ""
}

func trackerIntakeHasChatEffect(rec domain.SessionRecord) bool {
	return strings.TrimSpace(rec.Metadata.ProviderConversationID) != "" ||
		strings.TrimSpace(rec.Metadata.ControllerGeneration) != ""
}
