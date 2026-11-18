package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
	"github.com/aoagents/agent-orchestrator/backend/internal/workspacewatch"
)

const (
	maxProjectOverlapNoticePaths          = 20
	maxProjectOverlapNoticePath           = 240
	defaultProjectOverlapNoticeRetryDelay = 15 * time.Second
	defaultProjectOverlapNoticeTimeout    = 30 * time.Second
)

type projectOverlapNoticeStore interface {
	ListProjectSessionOverlapNotices(context.Context, domain.ProjectID) ([]domain.SessionOverlapNotice, error)
	UpsertSessionOverlapNotice(context.Context, domain.SessionOverlapNotice) error
	DeleteSessionOverlapNotice(context.Context, domain.SessionID, domain.SessionID) error
}

type projectOverlapNoticeKey struct {
	projectID     domain.ProjectID
	sessionID     domain.SessionID
	peerSessionID domain.SessionID
}

type projectOverlapNoticeAttempt struct {
	deliveryID string
	message    string
	ambiguous  bool
}

type projectOverlapNoticeReconcileState struct {
	retry             bool
	coverageUncertain bool
}

type projectOverlapRadarWorker struct {
	cancel  context.CancelFunc
	refresh chan struct{}
}

type projectOverlapRadarTopology struct {
	eligible    int
	fingerprint string
}

func projectOverlapRadarRefreshProjects(
	changed map[domain.ProjectID]bool,
	signals []projectOverlapRadarSignal,
) map[domain.ProjectID]bool {
	refresh := make(map[domain.ProjectID]bool, len(changed)+len(signals))
	for projectID, topologyChanged := range changed {
		if topologyChanged {
			refresh[projectID] = true
		}
	}
	for _, signal := range signals {
		if signal.force {
			refresh[signal.projectID] = true
		}
	}
	return refresh
}

type projectOverlapRadarSignal struct {
	projectID domain.ProjectID
	force     bool
}

// ReconcileActiveProjectOverlapNotices detects and retries notices without an
// overlap reader. RunProjectOverlapNoticeRadar invokes it only for workspace or
// session events; collision detection does not poll worktrees on a timer.
func (s *Service) ReconcileActiveProjectOverlapNotices(ctx context.Context) error {
	if _, ok := s.store.(projectOverlapNoticeStore); !ok || s.overlapNudger == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectIDs, err := s.activeProjectOverlapIDs(ctx)
	if err != nil {
		return err
	}
	var (
		group           sync.WaitGroup
		limit           = make(chan struct{}, 4)
		reconcileMu     sync.Mutex
		reconcileErrors []error
	)
	for _, projectID := range projectIDs {
		group.Add(1)
		go func(projectID domain.ProjectID) {
			defer group.Done()
			select {
			case limit <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-limit }()
			if err := s.ReconcileCurrentProjectOverlapNotices(ctx, projectID); err != nil {
				reconcileMu.Lock()
				reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile collision notices for %s: %w", projectID, err))
				reconcileMu.Unlock()
			}
		}(projectID)
	}
	group.Wait()
	return errors.Join(reconcileErrors...)
}

func (s *Service) activeProjectOverlapIDs(ctx context.Context) ([]domain.ProjectID, error) {
	topology, err := s.projectOverlapRadarTopology(ctx)
	if err != nil {
		return nil, err
	}
	projectIDs := make([]domain.ProjectID, 0, len(topology))
	for projectID, current := range topology {
		if current.eligible >= 2 {
			projectIDs = append(projectIDs, projectID)
		}
	}
	sort.Slice(projectIDs, func(i, j int) bool { return projectIDs[i] < projectIDs[j] })
	return projectIDs, nil
}

func (s *Service) projectOverlapRadarTopology(ctx context.Context) (map[domain.ProjectID]projectOverlapRadarTopology, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	records, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sessions for collision radar topology: %w", err)
	}
	rows := make(map[domain.ProjectID][]string)
	for _, record := range records {
		if record.ProjectID == "" {
			continue
		}
		if _, ok := rows[record.ProjectID]; !ok {
			rows[record.ProjectID] = nil
		}
		if !eligibleProjectOverlapSession(record) {
			continue
		}
		rows[record.ProjectID] = append(rows[record.ProjectID], strings.Join([]string{
			string(record.ID),
			record.Metadata.WorkspacePath,
			record.Metadata.WorkspaceRepoPath,
			record.Metadata.DiffBaseSHA,
			record.Metadata.DiffBaseRef,
		}, "\x00"))
	}
	topology := make(map[domain.ProjectID]projectOverlapRadarTopology, len(rows))
	for projectID, members := range rows {
		sort.Strings(members)
		topology[projectID] = projectOverlapRadarTopology{eligible: len(members), fingerprint: strings.Join(members, "\x01")}
	}
	return topology, nil
}

// SignalProjectOverlapRadar coalesces refreshes without dropping project
// identity. Daemon session/config callbacks call it synchronously after their
// durable write; the radar drains the set in its own goroutine.
func (s *Service) SignalProjectOverlapRadar(projectID domain.ProjectID) {
	s.signalProjectOverlapRadar(projectID, true)
}

// SignalProjectOverlapRadarSession checks whether an eligibility, workspace,
// or comparison-base change requires rebuilding the project's worker. Generic
// chat and activity updates keep the same fingerprint and do no Git work.
func (s *Service) SignalProjectOverlapRadarSession(projectID domain.ProjectID) {
	s.signalProjectOverlapRadar(projectID, false)
}

func (s *Service) signalProjectOverlapRadar(projectID domain.ProjectID, force bool) {
	if projectID == "" {
		return
	}
	s.overlapRadarSignalMu.Lock()
	if s.overlapRadarPending == nil {
		s.overlapRadarPending = make(map[domain.ProjectID]bool)
	}
	s.overlapRadarPending[projectID] = s.overlapRadarPending[projectID] || force
	if s.overlapRadarWake == nil {
		s.overlapRadarWake = make(chan struct{}, 1)
	}
	wake := s.overlapRadarWake
	s.overlapRadarSignalMu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (s *Service) takeProjectOverlapRadarSignals() []projectOverlapRadarSignal {
	s.overlapRadarSignalMu.Lock()
	defer s.overlapRadarSignalMu.Unlock()
	signals := make([]projectOverlapRadarSignal, 0, len(s.overlapRadarPending))
	for projectID, force := range s.overlapRadarPending {
		signals = append(signals, projectOverlapRadarSignal{projectID: projectID, force: force})
		delete(s.overlapRadarPending, projectID)
	}
	sort.Slice(signals, func(i, j int) bool { return signals[i].projectID < signals[j].projectID })
	return signals
}

// RunProjectOverlapNoticeRadar owns one independent worker per active project.
// Workspace events therefore retain project identity, and a slow delivery in
// one project cannot delay detection in another.
func (s *Service) RunProjectOverlapNoticeRadar(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	workers := make(map[domain.ProjectID]projectOverlapRadarWorker)
	lastTopology := make(map[domain.ProjectID]string)
	var workerGroup sync.WaitGroup
	defer func() {
		for _, worker := range workers {
			worker.cancel()
		}
		workerGroup.Wait()
	}()
	syncWorkers := func() (map[domain.ProjectID]bool, error) {
		topology, err := s.projectOverlapRadarTopology(ctx)
		if err != nil {
			return nil, err
		}
		changed := make(map[domain.ProjectID]bool, len(topology)+len(lastTopology))
		for projectID, current := range topology {
			previous, existed := lastTopology[projectID]
			changed[projectID] = !existed || previous != current.fingerprint
		}
		for projectID := range lastTopology {
			if _, ok := topology[projectID]; !ok {
				changed[projectID] = true
			}
		}
		active := make(map[domain.ProjectID]struct{}, len(topology))
		for projectID, current := range topology {
			if current.eligible < 2 {
				continue
			}
			active[projectID] = struct{}{}
			if _, ok := workers[projectID]; ok {
				continue
			}
			workerCtx, cancel := context.WithCancel(ctx)
			refresh := make(chan struct{}, 1)
			workers[projectID] = projectOverlapRadarWorker{cancel: cancel, refresh: refresh}
			workerGroup.Add(1)
			go func() {
				defer workerGroup.Done()
				s.runProjectOverlapNoticeRadar(workerCtx, projectID, refresh)
			}()
		}
		for projectID, worker := range workers {
			if _, ok := active[projectID]; ok {
				continue
			}
			worker.cancel()
			delete(workers, projectID)
		}
		lastTopology = make(map[domain.ProjectID]string, len(topology))
		for projectID, current := range topology {
			lastTopology[projectID] = current.fingerprint
		}
		return changed, nil
	}
	applyTopologyChange := func(projectID domain.ProjectID, hadWorker bool) error {
		worker, ok := workers[projectID]
		if !ok {
			_, err := s.reconcileProjectOverlapNoticesLockedByProject(ctx, projectID)
			if err != nil {
				s.logProjectOverlapNoticeError(ctx, "clear inactive collision notices", projectID, err)
			}
			return err
		}
		if !hadWorker {
			return nil
		}
		select {
		case worker.refresh <- struct{}{}:
		default:
		}
		return nil
	}
	cleanupRetries := make(map[domain.ProjectID]struct{})
	forcedRetries := make(map[domain.ProjectID]struct{})
	applyProjects := func(projects map[domain.ProjectID]bool, hadWorker map[domain.ProjectID]bool) {
		for projectID := range projects {
			if err := applyTopologyChange(projectID, hadWorker[projectID]); err != nil {
				cleanupRetries[projectID] = struct{}{}
			} else {
				delete(cleanupRetries, projectID)
			}
		}
	}
	lastSyncFailed := false
	changed, err := syncWorkers()
	if err != nil {
		lastSyncFailed = true
		s.logProjectOverlapNoticeError(ctx, "list collision radar projects", "", err)
	} else {
		applyProjects(projectOverlapRadarRefreshProjects(changed, nil), nil)
	}
	for {
		var retry <-chan time.Time
		if lastSyncFailed || len(cleanupRetries) > 0 {
			retry = time.After(s.projectOverlapNoticeRetryDelay())
		}
		select {
		case <-ctx.Done():
			return
		case <-retry:
			hadWorker := make(map[domain.ProjectID]bool, len(workers))
			for projectID := range workers {
				hadWorker[projectID] = true
			}
			changed, err := syncWorkers()
			if err != nil {
				s.logProjectOverlapNoticeError(ctx, "list collision radar projects", "", err)
				lastSyncFailed = true
			} else {
				lastSyncFailed = false
				projects := projectOverlapRadarRefreshProjects(changed, nil)
				for projectID := range forcedRetries {
					projects[projectID] = true
				}
				for projectID := range cleanupRetries {
					projects[projectID] = true
				}
				applyProjects(projects, hadWorker)
				clear(forcedRetries)
			}
		case <-s.overlapRadarWake:
			signals := s.takeProjectOverlapRadarSignals()
			for _, signal := range signals {
				if signal.force {
					forcedRetries[signal.projectID] = struct{}{}
				}
			}
			hadWorker := make(map[domain.ProjectID]bool, len(workers))
			for projectID := range workers {
				hadWorker[projectID] = true
			}
			changed, err := syncWorkers()
			if err != nil {
				s.logProjectOverlapNoticeError(ctx, "list collision radar projects", "", err)
				lastSyncFailed = true
			} else {
				lastSyncFailed = false
			}
			if err != nil {
				continue
			}
			projects := projectOverlapRadarRefreshProjects(changed, signals)
			for projectID := range forcedRetries {
				projects[projectID] = true
			}
			for projectID := range cleanupRetries {
				projects[projectID] = true
			}
			applyProjects(projects, hadWorker)
			clear(forcedRetries)
		}
	}
}

func (s *Service) reconcileProjectOverlapNoticesLockedByProject(ctx context.Context, projectID domain.ProjectID) (projectOverlapNoticeReconcileState, error) {
	store, ok := s.store.(projectOverlapNoticeStore)
	if !ok || s.overlapNudger == nil || projectID == "" {
		return projectOverlapNoticeReconcileState{}, nil
	}
	unlock := s.lockProjectOverlapNotices(projectID)
	defer unlock()
	return s.reconcileProjectOverlapNoticesLocked(ctx, store, emptyProjectOverlapResult(projectID))
}

func (s *Service) runProjectOverlapNoticeRadar(ctx context.Context, projectID domain.ProjectID, refresh <-chan struct{}) {
	for {
		paths, pathErr := s.ProjectOverlapWatchPaths(ctx, projectID)
		if pathErr != nil {
			s.logProjectOverlapNoticeError(ctx, "list collision radar watch paths", projectID, pathErr)
		}
		watchCtx, cancelWatch := context.WithCancel(ctx)
		subscription := workspacewatch.Subscription{}
		watchFailed := pathErr != nil
		if !watchFailed && len(paths) > 0 {
			var watchErr error
			subscription, watchErr = s.overlapWatch(watchCtx, paths...)
			if watchErr != nil {
				watchFailed = true
				s.logProjectOverlapNoticeError(ctx, "watch collision radar workspaces", projectID, watchErr)
			}
		}
		// Install the watcher before the authoritative scan so an edit in the
		// setup gap always leaves a later edge.
		state := s.reconcileProjectOverlapNoticeRadar(ctx, projectID)
		coverageUncertain := watchFailed || state.coverageUncertain
		retryNeeded := coverageUncertain || state.retry
		retryTimer, retryC := s.resetProjectOverlapNoticeTimer(nil, retryNeeded)

	waitForProjectChange:
		for {
			select {
			case <-ctx.Done():
				cancelWatch()
				s.stopProjectOverlapNoticeTimer(retryTimer)
				return
			case <-refresh:
				cancelWatch()
				s.stopProjectOverlapNoticeTimer(retryTimer)
				break waitForProjectChange
			case _, ok := <-subscription.Changes:
				if !ok {
					subscription.Changes = nil
					coverageUncertain = true
					retryTimer, retryC = s.resetProjectOverlapNoticeTimer(retryTimer, true)
					continue
				}
				state = s.reconcileProjectOverlapNoticeRadar(ctx, projectID)
				retryTimer, retryC = s.resetProjectOverlapNoticeTimer(retryTimer, coverageUncertain || state.coverageUncertain || state.retry)
			case _, ok := <-subscription.Degraded:
				if !ok {
					subscription.Degraded = nil
					continue
				}
				coverageUncertain = true
				retryTimer, retryC = s.resetProjectOverlapNoticeTimer(retryTimer, true)
			case <-retryC:
				if coverageUncertain || state.coverageUncertain || watchFailed {
					cancelWatch()
					s.stopProjectOverlapNoticeTimer(retryTimer)
					break waitForProjectChange
				}
				state = s.reconcileProjectOverlapNoticeRadar(ctx, projectID)
				retryTimer, retryC = s.resetProjectOverlapNoticeTimer(retryTimer, state.coverageUncertain || state.retry)
			}
		}
	}
}

func (s *Service) reconcileProjectOverlapNoticeRadar(ctx context.Context, projectID domain.ProjectID) projectOverlapNoticeReconcileState {
	reconcileCtx, cancel := context.WithTimeout(ctx, s.projectOverlapNoticeTimeout())
	defer cancel()
	state, err := s.reconcileCurrentProjectOverlapNotices(reconcileCtx, projectID)
	if err != nil && ctx.Err() == nil {
		state.retry = true
		s.logProjectOverlapNoticeError(ctx, "reconcile collision notices", projectID, err)
	}
	return state
}

func (s *Service) projectOverlapNoticeRetryDelay() time.Duration {
	if s.overlapNoticeRetryDelay <= 0 {
		return defaultProjectOverlapNoticeRetryDelay
	}
	return s.overlapNoticeRetryDelay
}

func (s *Service) projectOverlapNoticeTimeout() time.Duration {
	if s.overlapNoticeTimeout <= 0 {
		return defaultProjectOverlapNoticeTimeout
	}
	return s.overlapNoticeTimeout
}

func (s *Service) resetProjectOverlapNoticeTimer(timer *time.Timer, enabled bool) (*time.Timer, <-chan time.Time) {
	s.stopProjectOverlapNoticeTimer(timer)
	if !enabled {
		return nil, nil
	}
	timer = time.NewTimer(s.projectOverlapNoticeRetryDelay())
	return timer, timer.C
}

func (s *Service) stopProjectOverlapNoticeTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func (s *Service) logProjectOverlapNoticeError(ctx context.Context, message string, projectID domain.ProjectID, err error) {
	if s.logger == nil || err == nil || ctx.Err() != nil {
		return
	}
	if projectID == "" {
		s.logger.WarnContext(ctx, message, "error", err)
		return
	}
	s.logger.WarnContext(ctx, message, "projectID", projectID, "error", err)
}

// ReconcileCurrentProjectOverlapNotices serializes the authoritative overlap
// read with delivery reconciliation, making completion order irrelevant.
func (s *Service) ReconcileCurrentProjectOverlapNotices(ctx context.Context, projectID domain.ProjectID) error {
	_, err := s.reconcileCurrentProjectOverlapNotices(ctx, projectID)
	return err
}

func (s *Service) reconcileCurrentProjectOverlapNotices(ctx context.Context, projectID domain.ProjectID) (projectOverlapNoticeReconcileState, error) {
	store, ok := s.store.(projectOverlapNoticeStore)
	if !ok || s.overlapNudger == nil || projectID == "" {
		return projectOverlapNoticeReconcileState{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	unlock := s.lockProjectOverlapNotices(projectID)
	defer unlock()
	result, err := s.ProjectOverlaps(ctx, projectID)
	if err != nil {
		return projectOverlapNoticeReconcileState{}, err
	}
	state, err := s.reconcileProjectOverlapNoticesLocked(ctx, store, result)
	state.coverageUncertain = len(result.UnavailableSessionIDs) > 0
	return state, err
}

// reconcileProjectOverlapNotices is the snapshot-level unit-test boundary.
// Production callers use ReconcileCurrentProjectOverlapNotices so the fresh
// read and delivery reconciliation stay under the same per-project lock.
func (s *Service) reconcileProjectOverlapNotices(ctx context.Context, result ProjectOverlapResult) error {
	store, ok := s.store.(projectOverlapNoticeStore)
	if !ok || s.overlapNudger == nil || result.ProjectID == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	unlock := s.lockProjectOverlapNotices(result.ProjectID)
	defer unlock()
	_, err := s.reconcileProjectOverlapNoticesLocked(ctx, store, result)
	return err
}

func (s *Service) reconcileProjectOverlapNoticesLocked(ctx context.Context, store projectOverlapNoticeStore, result ProjectOverlapResult) (projectOverlapNoticeReconcileState, error) {
	state := projectOverlapNoticeReconcileState{}
	persisted, err := store.ListProjectSessionOverlapNotices(ctx, result.ProjectID)
	if err != nil {
		return state, err
	}
	var reconcileErr error
	recordError := func(err error) {
		if reconcileErr == nil {
			reconcileErr = err
		}
	}
	active := make(map[projectOverlapNoticeKey]struct{}, len(result.Overlaps)*2)
	for _, overlap := range result.Overlaps {
		active[projectOverlapNoticeKey{projectID: result.ProjectID, sessionID: overlap.SessionA, peerSessionID: overlap.SessionB}] = struct{}{}
		active[projectOverlapNoticeKey{projectID: result.ProjectID, sessionID: overlap.SessionB, peerSessionID: overlap.SessionA}] = struct{}{}
	}
	incomplete := make(map[domain.SessionID]struct{}, len(result.TruncatedSessionIDs)+len(result.UnresolvedBaseSessionIDs)+len(result.UnavailableSessionIDs))
	for _, ids := range [][]domain.SessionID{result.TruncatedSessionIDs, result.UnresolvedBaseSessionIDs, result.UnavailableSessionIDs} {
		for _, id := range ids {
			incomplete[id] = struct{}{}
		}
	}

	delivered := make(map[projectOverlapNoticeKey]struct{}, len(persisted))
	for _, notice := range persisted {
		key := projectOverlapNoticeKey{projectID: result.ProjectID, sessionID: notice.SessionID, peerSessionID: notice.PeerSessionID}
		if _, stillActive := active[key]; stillActive {
			delivered[key] = struct{}{}
			s.deleteProjectOverlapNoticeAttempt(key)
			continue
		}
		_, recipientIncomplete := incomplete[key.sessionID]
		_, peerIncomplete := incomplete[key.peerSessionID]
		if recipientIncomplete || peerIncomplete {
			// An incomplete scan cannot prove this directed pair cleared. Keep
			// its delivery row so a transient read failure does not resend it.
			delivered[key] = struct{}{}
			s.deleteProjectOverlapNoticeAttempt(key)
			continue
		}
		if err := store.DeleteSessionOverlapNotice(ctx, notice.SessionID, notice.PeerSessionID); err != nil {
			state.retry = true
			recordError(err)
		} else {
			s.deleteProjectOverlapNoticeAttempt(key)
		}
	}
	for key, notice := range s.pendingProjectOverlapNotices(result.ProjectID) {
		_, stillActive := active[key]
		_, recipientIncomplete := incomplete[key.sessionID]
		_, peerIncomplete := incomplete[key.peerSessionID]
		if !stillActive && !recipientIncomplete && !peerIncomplete {
			s.deletePendingProjectOverlapNotice(key)
			s.deleteProjectOverlapNoticeAttempt(key)
			continue
		}
		delivered[key] = struct{}{}
		if err := store.UpsertSessionOverlapNotice(ctx, notice); err != nil {
			state.retry = true
			recordError(err)
		} else {
			s.deletePendingProjectOverlapNotice(key)
			s.deleteProjectOverlapNoticeAttempt(key)
		}
	}
	s.deleteInactiveProjectOverlapNoticeAttempts(result.ProjectID, active, incomplete)

	for _, overlap := range result.Overlaps {
		for _, key := range []projectOverlapNoticeKey{
			{projectID: result.ProjectID, sessionID: overlap.SessionA, peerSessionID: overlap.SessionB},
			{projectID: result.ProjectID, sessionID: overlap.SessionB, peerSessionID: overlap.SessionA},
		} {
			if _, alreadyDelivered := delivered[key]; alreadyDelivered {
				continue
			}
			peer, found, err := s.store.GetSession(ctx, key.peerSessionID)
			if err != nil {
				state.retry = true
				recordError(fmt.Errorf("load collision peer %s: %w", key.peerSessionID, err))
				continue
			}
			if !found || peer.IsTerminated {
				s.deleteProjectOverlapNoticeAttempt(key)
				continue
			}
			message := formatProjectOverlapNotice(peer.DisplayName, peer.ID, overlap.Paths)
			attempt := s.projectOverlapNoticeAttempt(key, message)
			message = attempt.message
			var outcome sessionguard.Outcome
			var nudgeErr error
			if attempt.ambiguous {
				outcome, nudgeErr = s.overlapNudger.ConfirmCoordination(ctx, key.sessionID, message, attempt.deliveryID)
			} else {
				outcome, nudgeErr = s.overlapNudger.NudgeCoordination(ctx, key.sessionID, message, attempt.deliveryID)
			}
			if outcome == sessionguard.Sent && nudgeErr == nil {
				delivered[key] = struct{}{}
				notice := domain.SessionOverlapNotice{SessionID: key.sessionID, PeerSessionID: key.peerSessionID, NotifiedAt: s.clock().UTC()}
				if err := store.UpsertSessionOverlapNotice(ctx, notice); err != nil {
					s.setPendingProjectOverlapNotice(key, notice)
					s.deleteProjectOverlapNoticeAttempt(key)
					state.retry = true
					recordError(err)
				} else {
					s.deletePendingProjectOverlapNotice(key)
					s.deleteProjectOverlapNoticeAttempt(key)
				}
			} else {
				state.retry = true
				if !attempt.ambiguous {
					if outcome != sessionguard.Sent || projectOverlapNoticeDefinitelyUnsent(nudgeErr) {
						s.deleteProjectOverlapNoticeAttempt(key)
					} else {
						s.markProjectOverlapNoticeAttemptAmbiguous(key)
					}
				}
			}
			if nudgeErr != nil {
				recordError(fmt.Errorf("nudge collision recipient %s: %w", key.sessionID, nudgeErr))
			}
		}
	}
	return state, reconcileErr
}

func projectOverlapNoticeDefinitelyUnsent(err error) bool {
	return errors.Is(err, sessionmanager.ErrComposerBusy) ||
		errors.Is(err, sessionmanager.ErrSemanticDeliveryRejected) ||
		errors.Is(err, sessionmanager.ErrSemanticDeliveryNotAttempted)
}

func (s *Service) deleteInactiveProjectOverlapNoticeAttempts(
	projectID domain.ProjectID,
	active map[projectOverlapNoticeKey]struct{},
	incomplete map[domain.SessionID]struct{},
) {
	s.overlapNoticePendingMu.Lock()
	defer s.overlapNoticePendingMu.Unlock()
	for key := range s.overlapNoticeAttempts {
		if key.projectID != projectID {
			continue
		}
		if _, stillActive := active[key]; stillActive {
			continue
		}
		_, recipientIncomplete := incomplete[key.sessionID]
		_, peerIncomplete := incomplete[key.peerSessionID]
		if !recipientIncomplete && !peerIncomplete {
			delete(s.overlapNoticeAttempts, key)
		}
	}
}

func (s *Service) projectOverlapNoticeAttempt(key projectOverlapNoticeKey, message string) projectOverlapNoticeAttempt {
	s.overlapNoticePendingMu.Lock()
	defer s.overlapNoticePendingMu.Unlock()
	if attempt := s.overlapNoticeAttempts[key]; attempt.deliveryID != "" {
		if attempt.message == "" {
			attempt.message = message
			s.overlapNoticeAttempts[key] = attempt
		}
		return attempt
	}
	if s.overlapNoticeAttempts == nil {
		s.overlapNoticeAttempts = make(map[projectOverlapNoticeKey]projectOverlapNoticeAttempt)
	}
	var token [12]byte
	if _, err := rand.Read(token[:]); err == nil {
		attempt := projectOverlapNoticeAttempt{deliveryID: "collision:" + hex.EncodeToString(token[:]), message: message}
		s.overlapNoticeAttempts[key] = attempt
		return attempt
	}
	s.overlapNoticeAttemptSeq++
	attempt := projectOverlapNoticeAttempt{deliveryID: fmt.Sprintf("collision:%x:%x", s.clock().UnixNano(), s.overlapNoticeAttemptSeq), message: message}
	s.overlapNoticeAttempts[key] = attempt
	return attempt
}

func (s *Service) markProjectOverlapNoticeAttemptAmbiguous(key projectOverlapNoticeKey) {
	s.overlapNoticePendingMu.Lock()
	defer s.overlapNoticePendingMu.Unlock()
	attempt, ok := s.overlapNoticeAttempts[key]
	if !ok {
		return
	}
	attempt.ambiguous = true
	s.overlapNoticeAttempts[key] = attempt
}

func (s *Service) deleteProjectOverlapNoticeAttempt(key projectOverlapNoticeKey) {
	s.overlapNoticePendingMu.Lock()
	defer s.overlapNoticePendingMu.Unlock()
	delete(s.overlapNoticeAttempts, key)
}

func (s *Service) pendingProjectOverlapNotices(projectID domain.ProjectID) map[projectOverlapNoticeKey]domain.SessionOverlapNotice {
	s.overlapNoticePendingMu.Lock()
	defer s.overlapNoticePendingMu.Unlock()
	pending := make(map[projectOverlapNoticeKey]domain.SessionOverlapNotice)
	for key, notice := range s.overlapNoticePending {
		if key.projectID == projectID {
			pending[key] = notice
		}
	}
	return pending
}

func (s *Service) setPendingProjectOverlapNotice(key projectOverlapNoticeKey, notice domain.SessionOverlapNotice) {
	s.overlapNoticePendingMu.Lock()
	defer s.overlapNoticePendingMu.Unlock()
	if s.overlapNoticePending == nil {
		s.overlapNoticePending = make(map[projectOverlapNoticeKey]domain.SessionOverlapNotice)
	}
	s.overlapNoticePending[key] = notice
}

func (s *Service) deletePendingProjectOverlapNotice(key projectOverlapNoticeKey) {
	s.overlapNoticePendingMu.Lock()
	defer s.overlapNoticePendingMu.Unlock()
	delete(s.overlapNoticePending, key)
}

func (s *Service) lockProjectOverlapNotices(projectID domain.ProjectID) func() {
	s.overlapNoticeLocksMu.Lock()
	if s.overlapNoticeLocks == nil {
		s.overlapNoticeLocks = make(map[domain.ProjectID]*sync.Mutex)
	}
	mu := s.overlapNoticeLocks[projectID]
	if mu == nil {
		mu = &sync.Mutex{}
		s.overlapNoticeLocks[projectID] = mu
	}
	s.overlapNoticeLocksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

func formatProjectOverlapNotice(peerName string, peerID domain.SessionID, paths []string) string {
	label := boundedProjectOverlapNoticeText(strings.TrimSpace(peerName), maxDisplayNameLen)
	if label == "" || label == string(peerID) {
		label = string(peerID)
	} else {
		label += " (" + string(peerID) + ")"
	}
	shown := min(len(paths), maxProjectOverlapNoticePaths)
	lines := make([]string, 0, shown+1)
	for _, changedPath := range paths[:shown] {
		lines = append(lines, "- "+boundedProjectOverlapNoticeText(changedPath, maxProjectOverlapNoticePath))
	}
	if remaining := len(paths) - shown; remaining > 0 {
		lines = append(lines, fmt.Sprintf("+ %d more files", remaining))
	}
	return fmt.Sprintf("Collision radar: session %s also changed these files:\n%s\n\nCoordinate before editing or merging overlapping work.", label, strings.Join(lines, "\n"))
}

func boundedProjectOverlapNoticeText(value string, limit int) string {
	value = domain.SanitizeControlChars(value)
	value = strings.NewReplacer("\\", "\\\\", "\r", "\\r", "\n", "\\n", "\t", "\\t").Replace(value)
	if bounded, truncated := truncateUTF8(value, limit); truncated {
		return bounded + "…"
	}
	return value
}
