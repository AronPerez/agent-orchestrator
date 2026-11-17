package session

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
	"github.com/aoagents/agent-orchestrator/backend/internal/workspacewatch"
)

const (
	maxProjectOverlapNoticePaths = 20
	maxProjectOverlapNoticePath  = 240
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

// ReconcileActiveProjectOverlapNotices detects and retries notices without an
// overlap reader. RunProjectOverlapNoticeRadar invokes it only for workspace or
// session events; collision detection does not poll worktrees on a timer.
func (s *Service) ReconcileActiveProjectOverlapNotices(ctx context.Context) error {
	if _, ok := s.store.(projectOverlapNoticeStore); !ok || s.overlapNudger == nil {
		return nil
	}
	projectIDs, err := s.activeProjectOverlapIDs(ctx)
	if err != nil {
		return err
	}
	var reconcileErrors []error
	for _, projectID := range projectIDs {
		if err := s.ReconcileCurrentProjectOverlapNotices(ctx, projectID); err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("reconcile collision notices for %s: %w", projectID, err))
		}
	}
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
	projectSet := make(map[domain.ProjectID]struct{})
	for _, record := range records {
		if record.ProjectID != "" && eligibleProjectOverlapSession(record) {
			projectSet[record.ProjectID] = struct{}{}
		}
	}
	projectIDs := make([]domain.ProjectID, 0, len(projectSet))
	for projectID := range projectSet {
		projectIDs = append(projectIDs, projectID)
	}
	sort.Slice(projectIDs, func(i, j int) bool { return projectIDs[i] < projectIDs[j] })
	return projectIDs, nil
}

func (s *Service) activeProjectOverlapWatchPaths(ctx context.Context) ([]string, error) {
	projectIDs, err := s.activeProjectOverlapIDs(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var paths []string
	for _, projectID := range projectIDs {
		projectPaths, err := s.ProjectOverlapWatchPaths(ctx, projectID)
		if err != nil {
			return nil, err
		}
		for _, workspacePath := range projectPaths {
			if _, ok := seen[workspacePath]; ok {
				continue
			}
			seen[workspacePath] = struct{}{}
			paths = append(paths, workspacePath)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// RunProjectOverlapNoticeRadar owns one aggregate filesystem watcher for all
// active project worktrees. Session CDC edges rebuild its roots and also retry
// recipients whose last delivery was suppressed by activity state.
func (s *Service) RunProjectOverlapNoticeRadar(ctx context.Context, sessionChanges <-chan struct{}) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		paths, pathErr := s.activeProjectOverlapWatchPaths(ctx)
		if pathErr != nil && s.logger != nil && ctx.Err() == nil {
			s.logger.Warn("list collision radar watch paths", "error", pathErr)
		}
		watchCtx, cancelWatch := context.WithCancel(ctx)
		var workspaceChanges <-chan struct{}
		if pathErr == nil && len(paths) > 0 {
			var watchErr error
			workspaceChanges, watchErr = workspacewatch.Watch(watchCtx, paths...)
			if watchErr != nil && s.logger != nil && ctx.Err() == nil {
				s.logger.Warn("watch collision radar workspaces", "error", watchErr)
			}
		}
		// Install the watcher before the authoritative scan so an edit in the
		// setup gap always leaves a later edge.
		if err := s.ReconcileActiveProjectOverlapNotices(ctx); err != nil && s.logger != nil && ctx.Err() == nil {
			s.logger.Warn("reconcile collision notices", "error", err)
		}

	waitForChange:
		for {
			select {
			case <-ctx.Done():
				cancelWatch()
				return
			case _, ok := <-sessionChanges:
				cancelWatch()
				if !ok {
					sessionChanges = nil
				}
				break waitForChange
			case _, ok := <-workspaceChanges:
				if !ok {
					cancelWatch()
					break waitForChange
				}
				if err := s.ReconcileActiveProjectOverlapNotices(ctx); err != nil && s.logger != nil && ctx.Err() == nil {
					s.logger.Warn("reconcile collision notices", "error", err)
				}
			}
		}
	}
}

// ReconcileCurrentProjectOverlapNotices serializes the authoritative overlap
// read with delivery reconciliation, making completion order irrelevant.
func (s *Service) ReconcileCurrentProjectOverlapNotices(ctx context.Context, projectID domain.ProjectID) error {
	store, ok := s.store.(projectOverlapNoticeStore)
	if !ok || s.overlapNudger == nil || projectID == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	unlock := s.lockProjectOverlapNotices(projectID)
	defer unlock()
	result, err := s.ProjectOverlaps(ctx, projectID)
	if err != nil {
		return err
	}
	return s.reconcileProjectOverlapNoticesLocked(ctx, store, result)
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
	return s.reconcileProjectOverlapNoticesLocked(ctx, store, result)
}

func (s *Service) reconcileProjectOverlapNoticesLocked(ctx context.Context, store projectOverlapNoticeStore, result ProjectOverlapResult) error {
	persisted, err := store.ListProjectSessionOverlapNotices(ctx, result.ProjectID)
	if err != nil {
		return err
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
			continue
		}
		_, recipientIncomplete := incomplete[key.sessionID]
		_, peerIncomplete := incomplete[key.peerSessionID]
		if recipientIncomplete || peerIncomplete {
			// An incomplete scan cannot prove this directed pair cleared. Keep
			// its delivery row so a transient read failure does not resend it.
			delivered[key] = struct{}{}
			continue
		}
		if err := store.DeleteSessionOverlapNotice(ctx, notice.SessionID, notice.PeerSessionID); err != nil {
			recordError(err)
		}
	}
	for key, notice := range s.pendingProjectOverlapNotices(result.ProjectID) {
		_, stillActive := active[key]
		_, recipientIncomplete := incomplete[key.sessionID]
		_, peerIncomplete := incomplete[key.peerSessionID]
		if !stillActive && !recipientIncomplete && !peerIncomplete {
			s.deletePendingProjectOverlapNotice(key)
			continue
		}
		delivered[key] = struct{}{}
		if err := store.UpsertSessionOverlapNotice(ctx, notice); err != nil {
			recordError(err)
		} else {
			s.deletePendingProjectOverlapNotice(key)
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
				recordError(fmt.Errorf("load collision peer %s: %w", key.peerSessionID, err))
				continue
			}
			if !found || peer.IsTerminated {
				continue
			}
			message := formatProjectOverlapNotice(peer.DisplayName, peer.ID, overlap.Paths)
			outcome, nudgeErr := s.overlapNudger.NudgeCoordination(ctx, key.sessionID, message)
			if outcome == sessionguard.Sent && nudgeErr == nil {
				delivered[key] = struct{}{}
				notice := domain.SessionOverlapNotice{SessionID: key.sessionID, PeerSessionID: key.peerSessionID, NotifiedAt: s.clock().UTC()}
				if err := store.UpsertSessionOverlapNotice(ctx, notice); err != nil {
					s.setPendingProjectOverlapNotice(key, notice)
					recordError(err)
				} else {
					s.deletePendingProjectOverlapNotice(key)
				}
			}
			if nudgeErr != nil {
				recordError(fmt.Errorf("nudge collision recipient %s: %w", key.sessionID, nudgeErr))
			}
		}
	}
	return reconcileErr
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
