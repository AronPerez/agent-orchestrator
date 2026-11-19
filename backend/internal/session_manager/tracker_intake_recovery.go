package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

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
	m.syncTrackerIntakeReaperFences(claims)
	var reconcileErr error
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return errors.Join(reconcileErr, err)
		}
		m.setTrackerIntakeReaperFence(claim.SessionID)
		if err := m.reconcileTrackerIntakeClaim(ctx, claim); err != nil {
			reconcileErr = errors.Join(reconcileErr, err)
			continue
		}
		if err := m.refreshTrackerIntakeReaperFence(ctx, claim.SessionID); err != nil {
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
	m.syncTrackerIntakeReaperFences(claims)
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.setTrackerIntakeReaperFence(claim.SessionID)
		if !m.beginTrackerIntakeRecovery(claim.SessionID) {
			continue
		}
		m.runInBackground(func() {
			defer m.endTrackerIntakeRecovery(claim.SessionID)
			if err := m.reconcileTrackerIntakeClaim(m.backgroundContext, claim); err != nil {
				m.logger.Warn("tracker intake: claim cleanup deferred", "sessionID", claim.SessionID, "projectID", claim.ProjectID, "error", err)
				return
			}
			if err := m.refreshTrackerIntakeReaperFence(m.backgroundContext, claim.SessionID); err != nil {
				m.logger.Warn("tracker intake: reaper fence refresh deferred", "sessionID", claim.SessionID, "projectID", claim.ProjectID, "error", err)
			}
		})
	}
	return nil
}

// scheduleTrackerIntakeClaimInspection starts a read-only lookup for every
// pending claim before startup health runs. A blocked lookup stays isolated in
// its worker, while mutation-capable reconciliation is scheduled only after the
// startup live and reap passes have finished.
func (m *Manager) scheduleTrackerIntakeClaimInspection(ctx context.Context) error {
	claims, err := m.store.ListPendingTrackerIntakeClaims(ctx)
	if err != nil {
		return err
	}
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.runInBackground(func() {
			if err := m.inspectTrackerIntakeClaim(m.backgroundContext, claim); err != nil {
				m.logger.Warn("tracker intake: claim inspection deferred", "sessionID", claim.SessionID, "projectID", claim.ProjectID, "error", err)
			}
		})
	}
	return nil
}

// reconcileStartupTrackerIntakeTerminalClaims resolves exact terminal owners
// before the startup gate opens for the runtime reaper. Only a reservation
// matching the published row is eligible here: interrupted unpublished
// launches and workspace cleanup remain isolated in the per-claim jobs.
func (m *Manager) reconcileStartupTrackerIntakeTerminalClaims(ctx context.Context, recs []domain.SessionRecord) bool {
	claims, err := m.store.ListPendingTrackerIntakeClaims(ctx)
	if err != nil {
		for _, rec := range recs {
			if !rec.IsTerminated && domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
				m.setTrackerIntakeReaperFence(rec.ID)
			}
		}
		m.logger.Warn("reconcile: pending terminal tracker intake claims could not be listed", "error", err)
		return false
	}
	m.syncTrackerIntakeReaperFences(claims)
	byID := make(map[domain.SessionID]domain.SessionRecord, len(recs))
	for _, rec := range recs {
		byID[rec.ID] = rec
	}
	candidates := make([]domain.TrackerIntakePendingClaim, 0, len(claims))
	for _, claim := range claims {
		rec, found := byID[claim.SessionID]
		if !found || rec.IsTerminated || rec.ProjectID != claim.ProjectID || rec.IssueID != claim.IssueID ||
			domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat ||
			!trackerIntakeRuntimeNeedsStartupHealth(rec, claim.Recovery) {
			continue
		}
		candidates = append(candidates, claim)
	}
	if len(candidates) == 0 {
		return false
	}
	for _, claim := range candidates {
		m.setTrackerIntakeReaperFence(claim.SessionID)
	}

	workers := m.reconcileWorkers
	if workers > len(candidates) {
		workers = len(candidates)
	}
	jobs := make(chan domain.TrackerIntakePendingClaim)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for claim := range jobs {
				recoveryCtx, cancel := context.WithTimeout(ctx, m.statusVerificationLimit)
				err := m.reconcileStartupTrackerIntakeTerminalClaim(recoveryCtx, claim)
				cancel()
				if err != nil {
					m.logger.Warn("reconcile: pending terminal tracker intake owner remains fenced",
						"sessionID", claim.SessionID, "projectID", claim.ProjectID, "error", err)
					captureBackgroundFault(ctx, err, "reconcile", "tracker_intake_terminal")
					continue
				}
				if err := m.refreshTrackerIntakeReaperFence(ctx, claim.SessionID); err != nil {
					m.logger.Warn("reconcile: pending terminal tracker intake fence refresh deferred",
						"sessionID", claim.SessionID, "projectID", claim.ProjectID, "error", err)
				}
			}
		}()
	}
	for _, claim := range candidates {
		jobs <- claim
	}
	close(jobs)
	wg.Wait()
	return true
}

// reconcileStartupTrackerIntakeTerminalClaim performs only bounded owner
// health transitions. Workspace teardown remains in the per-claim background
// job because cleanup hooks are intentionally unbounded.
func (m *Manager) reconcileStartupTrackerIntakeTerminalClaim(ctx context.Context, claim domain.TrackerIntakePendingClaim) error {
	reservation, pending, err := m.store.GetPendingTrackerIntakeRecovery(ctx, claim.SessionID)
	if err != nil {
		return fmt.Errorf("recover tracker intake %s: inspect startup claim: %w", claim.SessionID, err)
	}
	if !pending {
		return nil
	}
	rec, found, err := m.store.GetSession(ctx, claim.SessionID)
	if err != nil {
		return fmt.Errorf("recover tracker intake %s: inspect startup session: %w", claim.SessionID, err)
	}
	if !found || rec.IsTerminated || rec.ProjectID != claim.ProjectID || rec.IssueID != claim.IssueID ||
		!trackerIntakeRuntimeNeedsStartupHealth(rec, reservation) {
		return nil
	}
	if reservation.CapacityReleased {
		if err := m.beginAgentOperation(ctx, claim.SessionID, agentOperationReconcile); err != nil {
			return fmt.Errorf("recover tracker intake %s: reserve startup release: %w", claim.SessionID, err)
		}
		defer m.endAgentOperation(claim.SessionID, agentOperationReconcile)
		reservation, pending, err = m.store.GetPendingTrackerIntakeRecovery(ctx, claim.SessionID)
		if err != nil {
			return fmt.Errorf("recover tracker intake %s: reload startup claim: %w", claim.SessionID, err)
		}
		rec, found, err = m.store.GetSession(ctx, claim.SessionID)
		if err != nil {
			return fmt.Errorf("recover tracker intake %s: reload startup session: %w", claim.SessionID, err)
		}
		if !pending || !found || rec.IsTerminated || rec.ProjectID != claim.ProjectID || rec.IssueID != claim.IssueID ||
			!reservation.CapacityReleased || !reservation.ReleasesCapacityFor(rec) ||
			(rec.RuntimeReleased() && rec.Activity.State == domain.ActivityExited) {
			return nil
		}
		return m.recordTerminalStoppedAtStartup(ctx, rec)
	}
	claim.Recovery = reservation
	return m.reconcileLiveTrackerIntakeRuntime(ctx, claim, rec, false)
}

func (m *Manager) setTrackerIntakeReaperFence(id domain.SessionID) {
	m.trackerIntakeRecoveryMu.Lock()
	defer m.trackerIntakeRecoveryMu.Unlock()
	if m.trackerIntakeReaperFenced == nil {
		m.trackerIntakeReaperFenced = make(map[domain.SessionID]struct{})
	}
	m.trackerIntakeReaperFenced[id] = struct{}{}
}

func (m *Manager) clearTrackerIntakeReaperFence(id domain.SessionID) {
	m.trackerIntakeRecoveryMu.Lock()
	delete(m.trackerIntakeReaperFenced, id)
	m.trackerIntakeRecoveryMu.Unlock()
}

func (m *Manager) trackerIntakeReaperFenceActive(id domain.SessionID) bool {
	m.trackerIntakeRecoveryMu.Lock()
	defer m.trackerIntakeRecoveryMu.Unlock()
	_, fenced := m.trackerIntakeReaperFenced[id]
	return fenced
}

// refreshTrackerIntakeReaperFence mirrors the durable pending state after a
// reconciliation attempt. A successful owner probe does not necessarily
// settle the claim: a live published owner, for example, must stay fenced from
// the ordinary reaper until a later pass proves that exact generation stopped.
func (m *Manager) refreshTrackerIntakeReaperFence(ctx context.Context, id domain.SessionID) error {
	_, pending, err := m.store.GetPendingTrackerIntakeRecovery(ctx, id)
	if err != nil {
		return fmt.Errorf("refresh tracker intake reaper fence %s: %w", id, err)
	}
	if pending {
		m.setTrackerIntakeReaperFence(id)
	} else {
		m.clearTrackerIntakeReaperFence(id)
	}
	return nil
}

// syncTrackerIntakeReaperFences atomically fences every pending owner and
// retires conservative fences once a successful durable claim listing proves
// that the session is no longer pending. Pending entries retain their fence
// until exact reconciliation succeeds.
func (m *Manager) syncTrackerIntakeReaperFences(claims []domain.TrackerIntakePendingClaim) {
	pending := make(map[domain.SessionID]struct{}, len(claims))
	for _, claim := range claims {
		pending[claim.SessionID] = struct{}{}
	}
	m.trackerIntakeRecoveryMu.Lock()
	defer m.trackerIntakeRecoveryMu.Unlock()
	if m.trackerIntakeReaperFenced == nil {
		m.trackerIntakeReaperFenced = make(map[domain.SessionID]struct{}, len(pending))
	}
	for id := range m.trackerIntakeReaperFenced {
		if _, found := pending[id]; !found {
			delete(m.trackerIntakeReaperFenced, id)
		}
	}
	for id := range pending {
		m.trackerIntakeReaperFenced[id] = struct{}{}
	}
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
		return m.reconcileLiveTrackerIntakeRuntime(ctx, claim, rec, true)
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
	if !found {
		rec = domain.SessionRecord{
			ID: claim.SessionID, ProjectID: claim.ProjectID, IssueID: claim.IssueID,
			Kind: domain.KindWorker, Mode: domain.SessionModeTUI,
		}
		if claim.Recovery.ControllerPossible {
			rec.Mode = domain.SessionModeChat
		}
	}
	if trackerIntakeReleasedUnpublishedRuntimeNeedsTeardown(rec, claim.Recovery) {
		// Builds predating exact host teardown could release capacity after the
		// tracked child exited while retaining an unpublished terminal host. The
		// release bit is not ownership proof for that host: settle it before any
		// resumed-owner shortcut or workspace cleanup can discard its fence.
		claim.Recovery, err = m.retireReleasedUnpublishedTrackerIntakeRuntime(ctx, rec, claim.Recovery)
		if err != nil {
			return fmt.Errorf("recover tracker intake %s: retire legacy runtime owner: %w", rec.ID, err)
		}
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

func (m *Manager) inspectTrackerIntakeClaim(ctx context.Context, claim domain.TrackerIntakePendingClaim) error {
	if _, _, err := m.store.GetPendingTrackerIntakeRecovery(ctx, claim.SessionID); err != nil {
		return fmt.Errorf("inspect tracker intake %s: %w", claim.SessionID, err)
	}
	return nil
}

func trackerIntakeRuntimeFenceProbeable(rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) bool {
	return domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat &&
		!reservation.CapacityReleased &&
		(reservation.DeliveryPossible || reservation.RelaunchPublished) &&
		strings.TrimSpace(reservation.RuntimeLaunchID) != "" &&
		len(uniqueTrackerIntakeHandles(reservation.RuntimeHandleIDs)) != 0
}

func trackerIntakeReleasedUnpublishedRuntimeNeedsTeardown(
	rec domain.SessionRecord,
	reservation domain.TrackerIntakeRecoveryReservation,
) bool {
	return reservation.CapacityReleased &&
		domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat &&
		strings.TrimSpace(reservation.RuntimeLaunchID) != "" &&
		len(trackerIntakeUnreleasedRuntimeHandles(reservation)) != 0 &&
		(rec.IsTerminated || !trackerIntakeRuntimeReservationMatchesSession(rec, reservation))
}

func trackerIntakeUnreleasedRuntimeHandles(reservation domain.TrackerIntakeRecoveryReservation) []string {
	handles := uniqueTrackerIntakeHandles(reservation.RuntimeHandleIDs)
	remaining := make([]string, 0, len(handles))
	for _, handleID := range handles {
		if !trackerIntakeRuntimeHandleReleased(reservation, handleID, reservation.RuntimeLaunchID) {
			remaining = append(remaining, handleID)
		}
	}
	return remaining
}

func trackerIntakeRuntimeHandleReleased(
	reservation domain.TrackerIntakeRecoveryReservation,
	handleID, launchID string,
) bool {
	return reservation.CapacityReleased &&
		strings.TrimSpace(launchID) != "" &&
		strings.TrimSpace(reservation.ReleasedRuntimeLaunchID) == strings.TrimSpace(launchID) &&
		trackerIntakeHandleReserved(reservation.ReleasedRuntimeHandleIDs, handleID)
}

func (m *Manager) retireReleasedUnpublishedTrackerIntakeRuntime(
	ctx context.Context,
	rec domain.SessionRecord,
	reservation domain.TrackerIntakeRecoveryReservation,
) (domain.TrackerIntakeRecoveryReservation, error) {
	owner := rec
	rowHandleID := strings.TrimSpace(rec.Metadata.RuntimeHandleID)
	rowLaunchID := strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
	rowHasRuntime := rowHandleID != "" || rowLaunchID != ""
	if !rec.IsTerminated && rowHasRuntime && !trackerIntakeRuntimeReservationMatchesSession(rec, reservation) {
		if rowHandleID == "" || rowLaunchID == "" {
			return reservation, fmt.Errorf("replacement runtime identity is incomplete: %w", ports.ErrRuntimeProbeInconclusive)
		}
		// The row describes a newer published owner. Probe and stop only the old
		// reservation; including the row here would destroy the replacement that
		// must keep its session and workspace.
		owner.Metadata.RuntimeHandleID = ""
		owner.Metadata.RuntimeLaunchID = ""
		owner.Metadata.RuntimeReleasedLaunchID = ""
		owner.Metadata.AgentSessionID = ""
	}
	if trackerIntakeRuntimeHandleReleased(reservation, rowHandleID, rowLaunchID) {
		// The row handle was conclusively destroyed before this cleanup retry.
		// Keep any other reserved candidates probeable without asking an absent
		// backend to rediscover the already-settled row owner.
		owner.Metadata.RuntimeHandleID = ""
		owner.Metadata.RuntimeLaunchID = ""
		owner.Metadata.RuntimeReleasedLaunchID = ""
	}
	retiring := reservation
	retiring.RuntimeHandleIDs = trackerIntakeUnreleasedRuntimeHandles(reservation)
	if err := m.stopTrackerIntakeRuntimeOwners(ctx, owner, retiring); err != nil {
		return reservation, err
	}
	reservation.ReleasedRuntimeHandleIDs = uniqueTrackerIntakeHandles(append(
		append([]string(nil), reservation.ReleasedRuntimeHandleIDs...),
		retiring.RuntimeHandleIDs...,
	))
	reservation.RuntimeLaunchID = ""
	reservation.RuntimeHandleIDs = nil
	if err := m.store.ReserveTrackerIntakeRecovery(ctx, rec.ID, reservation); err != nil {
		return reservation, fmt.Errorf("retire stopped legacy tracker intake runtime: %w", err)
	}
	return reservation, nil
}

func trackerIntakeRuntimeNeedsStartupHealth(rec domain.SessionRecord, reservation domain.TrackerIntakeRecoveryReservation) bool {
	if rec.IsTerminated || domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat ||
		!trackerIntakeRuntimeReservationMatchesSession(rec, reservation) {
		return false
	}
	if reservation.CapacityReleased {
		return reservation.ReleasesCapacityFor(rec) &&
			(!rec.RuntimeReleased() || rec.Activity.State != domain.ActivityExited)
	}
	return trackerIntakeRuntimeFenceProbeable(rec, reservation)
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
	// A matching terminal reservation is the only source that can prove the
	// exact published generation alive or absent. Ordinary startup health skips
	// pending terminals, so deferring here would let the boot reaper act on a
	// stale activity row before the claim owner is settled.
	if trackerIntakeRuntimeFenceProbeable(rec, reservation) &&
		trackerIntakeRuntimeReservationMatchesSession(rec, reservation) {
		return false
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

func (m *Manager) reconcileLiveTrackerIntakeRuntime(
	ctx context.Context,
	claim domain.TrackerIntakePendingClaim,
	rec domain.SessionRecord,
	allowCleanup bool,
) error {
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
			publishedExit := rec.Activity.State == domain.ActivityExited &&
				trackerIntakeRuntimeReservationMatchesSession(rec, reservation)
			trackedExit := probe.Reason == ports.FencedReasonExactExited
			if !publishedExit && !trackedExit {
				// FencedDead proves only the managed child absent; its live terminal
				// may still have a supervisor that can start that child.
				terminalAlive, err := m.runtime.IsAlive(ctx, ports.RuntimeHandle{ID: handleID})
				if err != nil {
					return fmt.Errorf("recover tracker intake %s: runtime %s terminal ownership remains unresolved: %w",
						rec.ID, handleID, errors.Join(ports.ErrRuntimeProbeInconclusive, err))
				}
				if terminalAlive {
					return fmt.Errorf("recover tracker intake %s: runtime %s supervisor can still launch: %w",
						rec.ID, handleID, ports.ErrRuntimeProbeInconclusive)
				}
			}
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
	if !allowCleanup && (!found || currentRec.IsTerminated ||
		domain.NormalizeSessionMode(currentRec.Mode) == domain.SessionModeChat ||
		!trackerIntakeRuntimeReservationMatchesSession(currentRec, current)) {
		// The startup pass may have queued this row before an explicit Kill or
		// another session mutation changed it. Leave all teardown to the
		// per-claim background job; startup gates must never wait on cleanup.
		return nil
	}
	claim.Recovery = current
	if len(alive) == 1 {
		_, err = m.adoptTrackerIntakeRuntime(ctx, claim, alive[0])
		return err
	}
	publishedOwner := found && !currentRec.IsTerminated &&
		domain.NormalizeSessionMode(currentRec.Mode) != domain.SessionModeChat &&
		trackerIntakeRuntimeReservationMatchesSession(currentRec, current)
	if publishedOwner {
		// The exact published owner ended while AO was down. Record an intentional
		// stop before releasing intake capacity so the boot reaper preserves the
		// session and workspace as resumable instead of classifying a crash.
		if err := m.recordTerminalStoppedAtStartup(ctx, currentRec); err != nil {
			return fmt.Errorf("recover tracker intake %s: record stopped published runtime: %w", claim.SessionID, err)
		}
		if err := m.markTrackerIntakeCapacityReleased(ctx, claim.SessionID); err != nil {
			return fmt.Errorf("recover tracker intake %s: release stopped runtime capacity: %w", claim.SessionID, err)
		}
		settled, err := m.settleExitedTrackerIntakeRuntime(ctx, claim)
		if err != nil || settled {
			return err
		}
		// A published owner is always preserved even if a concurrent durable-state
		// change made settlement inapplicable.
		return nil
	}
	if !allowCleanup {
		return nil
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

// ensureTrackerIntakePriorRuntimeStopped keeps a relaunch from replacing the
// only durable fence for a possibly-live earlier generation.
func (m *Manager) ensureTrackerIntakePriorRuntimeStopped(
	ctx context.Context,
	rec domain.SessionRecord,
	reservation domain.TrackerIntakeRecoveryReservation,
	controllerStopped bool,
	stoppedRuntimeHandleID, stoppedRuntimeLaunchID string,
) error {
	type candidate struct {
		handleID string
		launchID string
	}
	candidates := make([]candidate, 0, len(reservation.RuntimeHandleIDs)+1)
	seen := make(map[string]struct{}, len(reservation.RuntimeHandleIDs)+1)
	stoppedRuntimeHandleID = strings.TrimSpace(stoppedRuntimeHandleID)
	stoppedRuntimeLaunchID = strings.TrimSpace(stoppedRuntimeLaunchID)
	stoppedRuntimeMatched := false
	add := func(handleID, launchID string, released bool) error {
		handleID = strings.TrimSpace(handleID)
		launchID = strings.TrimSpace(launchID)
		if handleID != "" && launchID != "" &&
			handleID == stoppedRuntimeHandleID && launchID == stoppedRuntimeLaunchID {
			stoppedRuntimeMatched = true
			return nil
		}
		if released {
			return nil
		}
		if handleID == "" || launchID == "" {
			return fmt.Errorf("pending tracker intake runtime has incomplete ownership evidence: %w", ports.ErrRuntimeProbeInconclusive)
		}
		key := handleID + "\x00" + launchID
		if _, ok := seen[key]; ok {
			return nil
		}
		seen[key] = struct{}{}
		candidates = append(candidates, candidate{handleID: handleID, launchID: launchID})
		return nil
	}

	reservedLaunchID := strings.TrimSpace(reservation.RuntimeLaunchID)
	reservedRuntimeReleased := reservation.CapacityReleased && reservedLaunchID != "" &&
		strings.TrimSpace(reservation.ReleasedRuntimeLaunchID) == reservedLaunchID
	reservedControllerGeneration := strings.TrimSpace(reservation.ControllerGeneration)
	reservedControllerReleased := reservation.CapacityReleased && reservedControllerGeneration != "" &&
		strings.TrimSpace(reservation.ReleasedControllerGeneration) == reservedControllerGeneration
	reservedHandles := uniqueTrackerIntakeHandles(reservation.RuntimeHandleIDs)
	if len(reservedHandles) == 0 && reservedLaunchID != "" && !reservedRuntimeReleased {
		return fmt.Errorf("pending tracker intake runtime has no handle fence: %w", ports.ErrRuntimeProbeInconclusive)
	}
	for _, handleID := range reservedHandles {
		if err := add(handleID, reservedLaunchID, reservedRuntimeReleased); err != nil {
			return err
		}
	}

	rowHandleID := strings.TrimSpace(rec.Metadata.RuntimeHandleID)
	rowLaunchID := strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
	rowReleased := rec.RuntimeReleased() || (reservedRuntimeReleased && rowLaunchID == reservedLaunchID)
	if rowHandleID != "" || rowLaunchID != "" {
		// Spawn rollback clears the published handle after destroying it but keeps
		// its launch ID for history. When the reservation still owns handles for
		// that same generation, those candidates are the complete fence and must
		// all be probed; the handle-less row does not describe another owner.
		rowCoveredByReservation := rowHandleID == "" && rowLaunchID != "" &&
			rowLaunchID == reservedLaunchID && len(reservedHandles) != 0
		if !rowCoveredByReservation {
			if err := add(rowHandleID, rowLaunchID, rowReleased); err != nil {
				return err
			}
		}
	}
	chatOwnerStopped := controllerStopped && reservation.ControllerPossible
	if len(candidates) == 0 && ((!controllerStopped && reservation.ControllerPossible) ||
		(!chatOwnerStopped && !stoppedRuntimeMatched && (reservation.DeliveryPossible || reservation.RelaunchPublished))) &&
		!reservedRuntimeReleased && !reservedControllerReleased {
		return fmt.Errorf("pending tracker intake runtime has no probeable owner: %w", ports.ErrRuntimeProbeInconclusive)
	}

	for _, owner := range candidates {
		probe := m.runtime.ProbeFencedRuntime(ctx, ports.FencedRuntimeRef{
			Handle: ports.RuntimeHandle{ID: owner.handleID}, SessionID: rec.ID,
			Generation: owner.launchID, NativeIdentity: strings.TrimSpace(rec.Metadata.AgentSessionID),
		})
		switch probe.Liveness {
		case ports.FencedDead:
			continue
		case ports.FencedAlive:
			return fmt.Errorf("pending tracker intake runtime %s generation %s is still alive: %w",
				owner.handleID, owner.launchID, ErrAgentNotExited)
		default:
			return fmt.Errorf("pending tracker intake runtime %s generation %s remains unresolved (%s): %w",
				owner.handleID, owner.launchID, probe.Reason, ports.ErrRuntimeProbeInconclusive)
		}
	}
	return nil
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
	controllerGeneration := strings.TrimSpace(reservation.ControllerGeneration)
	controllerReleased := reservation.CapacityReleased && controllerGeneration != "" &&
		strings.TrimSpace(reservation.ReleasedControllerGeneration) == controllerGeneration
	if reservation.ControllerPossible && !controllerReleased {
		return fmt.Errorf("pending tracker intake Chat controller remains unresolved")
	}
	type candidate struct {
		handleID string
		launchID string
	}
	owners := make([]candidate, 0, len(reservation.RuntimeHandleIDs)+1)
	seen := make(map[string]struct{}, len(reservation.RuntimeHandleIDs)+1)
	add := func(handleID, launchID string) error {
		handleID = strings.TrimSpace(handleID)
		launchID = strings.TrimSpace(launchID)
		if handleID == "" || launchID == "" {
			return fmt.Errorf("pending tracker intake runtime has incomplete ownership evidence")
		}
		key := handleID + "\x00" + launchID
		if _, ok := seen[key]; ok {
			return nil
		}
		seen[key] = struct{}{}
		owners = append(owners, candidate{handleID: handleID, launchID: launchID})
		return nil
	}

	reservedLaunchID := strings.TrimSpace(reservation.RuntimeLaunchID)
	reservedHandles := uniqueTrackerIntakeHandles(reservation.RuntimeHandleIDs)
	if len(reservedHandles) == 0 && reservedLaunchID != "" {
		return fmt.Errorf("pending tracker intake runtime has no handle fence")
	}
	for _, handleID := range reservedHandles {
		if err := add(handleID, reservedLaunchID); err != nil {
			return err
		}
	}
	rowHandleID := strings.TrimSpace(rec.Metadata.RuntimeHandleID)
	rowLaunchID := strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
	if (rowHandleID != "" || rowLaunchID != "") && !rec.RuntimeReleased() {
		if err := add(rowHandleID, rowLaunchID); err != nil {
			return err
		}
	}
	if len(owners) == 0 {
		if reservation.DeliveryPossible || reservedLaunchID != "" {
			return fmt.Errorf("pending tracker intake native identity has no runtime fence")
		}
		return nil
	}
	for _, owner := range owners {
		ref := ports.FencedRuntimeRef{
			Handle: ports.RuntimeHandle{ID: owner.handleID}, SessionID: rec.ID,
			Generation: owner.launchID, NativeIdentity: strings.TrimSpace(rec.Metadata.AgentSessionID),
		}
		probe := m.runtime.ProbeFencedRuntime(ctx, ref)
		switch probe.Liveness {
		case ports.FencedDead, ports.FencedAlive:
			// FencedDead proves the exact managed child absent, not the terminal
			// host absent. Destroy is idempotent and must settle that outer owner
			// before Kill releases capacity or touches the workspace.
			if err := m.stopSourceRuntime(ctx, ref); err != nil {
				return fmt.Errorf("stop pending tracker intake runtime %s: %w", owner.handleID, err)
			}
		default:
			return fmt.Errorf("pending tracker intake runtime %s remains unresolved: %s", owner.handleID, probe.Reason)
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
	persistedOwner := rec
	if trackerIntakeReleasedUnpublishedRuntimeNeedsTeardown(persistedOwner, reservation) {
		var err error
		reservation, err = m.retireReleasedUnpublishedTrackerIntakeRuntime(ctx, persistedOwner, reservation)
		if err != nil {
			return fmt.Errorf("check session %s: retire legacy runtime owner: %w", rec.ID, err)
		}
	}
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
			if err := m.markTrackerIntakeCapacityReleasedAfterControllerStop(ctx, rec.ID); err != nil {
				return err
			}
			reservation.CapacityReleased = true
		}
		return m.releaseInterruptedTrackerIntake(ctx, rec, reservation)
	}

	if !reservation.CapacityReleased {
		if err := m.stopTrackerIntakeRuntimeOwners(ctx, persistedOwner, reservation); err != nil {
			return fmt.Errorf("check session %s: %w", rec.ID, err)
		}
		var err error
		reservation, err = m.recordTrackerIntakeStoppedOwners(ctx, persistedOwner, reservation)
		if err != nil {
			return err
		}
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

func trackerIntakeHasChatEffect(rec domain.SessionRecord) bool {
	return strings.TrimSpace(rec.Metadata.ProviderConversationID) != "" ||
		strings.TrimSpace(rec.Metadata.ControllerGeneration) != ""
}
