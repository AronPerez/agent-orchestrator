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

type projectOverlapNoticeReconcileState struct {
	retry             bool
	coverageUncertain bool
}

type projectOverlapRadarWorker struct {
	cancel  context.CancelFunc
	refresh chan struct{}
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
	if ctx == nil {
		ctx = context.Background()
	}
	records, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sessions for collision notices: %w", err)
	}
	projectCounts := make(map[domain.ProjectID]int)
	for _, record := range records {
		if record.ProjectID != "" && eligibleProjectOverlapSession(record) {
			projectCounts[record.ProjectID]++
		}
	}
	projectIDs := make([]domain.ProjectID, 0, len(projectCounts))
	for projectID, count := range projectCounts {
		if count >= 2 {
			projectIDs = append(projectIDs, projectID)
		}
	}
	sort.Slice(projectIDs, func(i, j int) bool { return projectIDs[i] < projectIDs[j] })
	return projectIDs, nil
}

// SignalProjectOverlapRadar coalesces refreshes without dropping project
// identity. Daemon session/config callbacks call it synchronously after their
// durable write; the radar drains the set in its own goroutine.
func (s *Service) SignalProjectOverlapRadar(projectID domain.ProjectID) {
	if projectID == "" {
		return
	}
	s.overlapRadarSignalMu.Lock()
	if s.overlapRadarPending == nil {
		s.overlapRadarPending = make(map[domain.ProjectID]struct{})
	}
	s.overlapRadarPending[projectID] = struct{}{}
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

func (s *Service) takeProjectOverlapRadarSignals() []domain.ProjectID {
	s.overlapRadarSignalMu.Lock()
	defer s.overlapRadarSignalMu.Unlock()
	projectIDs := make([]domain.ProjectID, 0, len(s.overlapRadarPending))
	for projectID := range s.overlapRadarPending {
		projectIDs = append(projectIDs, projectID)
		delete(s.overlapRadarPending, projectID)
	}
	sort.Slice(projectIDs, func(i, j int) bool { return projectIDs[i] < projectIDs[j] })
	return projectIDs
}

// RunProjectOverlapNoticeRadar owns one independent worker per active project.
// Workspace events therefore retain project identity, and a slow delivery in
// one project cannot delay detection in another.
func (s *Service) RunProjectOverlapNoticeRadar(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	workers := make(map[domain.ProjectID]projectOverlapRadarWorker)
	var workerGroup sync.WaitGroup
	defer func() {
		for _, worker := range workers {
			worker.cancel()
		}
		workerGroup.Wait()
	}()
	syncWorkers := func() error {
		projectIDs, err := s.activeProjectOverlapIDs(ctx)
		if err != nil {
			return err
		}
		active := make(map[domain.ProjectID]struct{}, len(projectIDs))
		for _, projectID := range projectIDs {
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
		return nil
	}
	lastSyncFailed := false
	if err := syncWorkers(); err != nil {
		lastSyncFailed = true
		s.logProjectOverlapNoticeError(ctx, "list collision radar projects", "", err)
	}
	for {
		var retry <-chan time.Time
		if lastSyncFailed {
			retry = time.After(s.projectOverlapNoticeRetryDelay())
		}
		select {
		case <-ctx.Done():
			return
		case <-retry:
			if err := syncWorkers(); err != nil {
				s.logProjectOverlapNoticeError(ctx, "list collision radar projects", "", err)
				lastSyncFailed = true
			} else {
				lastSyncFailed = false
			}
		case <-s.overlapRadarWake:
			projectIDs := s.takeProjectOverlapRadarSignals()
			if err := syncWorkers(); err != nil {
				s.logProjectOverlapNoticeError(ctx, "list collision radar projects", "", err)
				lastSyncFailed = true
			} else {
				lastSyncFailed = false
			}
			for _, projectID := range projectIDs {
				worker, ok := workers[projectID]
				if !ok {
					continue
				}
				select {
				case worker.refresh <- struct{}{}:
				default:
				}
			}
		}
	}
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
			deliveryID := s.projectOverlapNoticeAttemptID(key)
			outcome, nudgeErr := s.overlapNudger.NudgeCoordination(ctx, key.sessionID, message, deliveryID)
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
			}
			if nudgeErr != nil {
				recordError(fmt.Errorf("nudge collision recipient %s: %w", key.sessionID, nudgeErr))
			}
		}
	}
	return state, reconcileErr
}

func (s *Service) projectOverlapNoticeAttemptID(key projectOverlapNoticeKey) string {
	s.overlapNoticePendingMu.Lock()
	defer s.overlapNoticePendingMu.Unlock()
	if deliveryID := s.overlapNoticeAttempts[key]; deliveryID != "" {
		return deliveryID
	}
	if s.overlapNoticeAttempts == nil {
		s.overlapNoticeAttempts = make(map[projectOverlapNoticeKey]string)
	}
	var token [12]byte
	if _, err := rand.Read(token[:]); err == nil {
		deliveryID := "collision:" + hex.EncodeToString(token[:])
		s.overlapNoticeAttempts[key] = deliveryID
		return deliveryID
	}
	s.overlapNoticeAttemptSeq++
	deliveryID := fmt.Sprintf("collision:%x:%x", s.clock().UnixNano(), s.overlapNoticeAttemptSeq)
	s.overlapNoticeAttempts[key] = deliveryID
	return deliveryID
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
