package session

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
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
	sessionID     domain.SessionID
	peerSessionID domain.SessionID
}

// ScheduleProjectOverlapNotices reconciles delivery without holding up the
// overlap read or its SSE invalidation edge.
func (s *Service) ScheduleProjectOverlapNotices(result ProjectOverlapResult) {
	work := func() {
		if err := s.ReconcileProjectOverlapNotices(s.backgroundContext, result); err != nil && s.logger != nil {
			s.logger.Warn("reconcile collision notices", "projectID", result.ProjectID, "error", err)
		}
	}
	if s.runBackground != nil {
		s.runBackground(work)
		return
	}
	go work()
}

// ReconcileProjectOverlapNotices delivers one message per directed pair while
// the pair has any shared path. A clean read deletes its rows and re-arms a
// later collision. Suppressed writes remain unrecorded and can be retried.
func (s *Service) ReconcileProjectOverlapNotices(ctx context.Context, result ProjectOverlapResult) error {
	store, ok := s.store.(projectOverlapNoticeStore)
	if !ok || s.overlapNudger == nil || result.ProjectID == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	unlock := s.lockProjectOverlapNotices(result.ProjectID)
	defer unlock()

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
		active[projectOverlapNoticeKey{sessionID: overlap.SessionA, peerSessionID: overlap.SessionB}] = struct{}{}
		active[projectOverlapNoticeKey{sessionID: overlap.SessionB, peerSessionID: overlap.SessionA}] = struct{}{}
	}
	incomplete := make(map[domain.SessionID]struct{}, len(result.TruncatedSessionIDs)+len(result.UnresolvedBaseSessionIDs)+len(result.UnavailableSessionIDs))
	for _, ids := range [][]domain.SessionID{result.TruncatedSessionIDs, result.UnresolvedBaseSessionIDs, result.UnavailableSessionIDs} {
		for _, id := range ids {
			incomplete[id] = struct{}{}
		}
	}

	delivered := make(map[projectOverlapNoticeKey]struct{}, len(persisted))
	for _, notice := range persisted {
		key := projectOverlapNoticeKey{sessionID: notice.SessionID, peerSessionID: notice.PeerSessionID}
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

	for _, overlap := range result.Overlaps {
		for _, key := range []projectOverlapNoticeKey{
			{sessionID: overlap.SessionA, peerSessionID: overlap.SessionB},
			{sessionID: overlap.SessionB, peerSessionID: overlap.SessionA},
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
			if outcome == sessionguard.Sent {
				delivered[key] = struct{}{}
				notice := domain.SessionOverlapNotice{SessionID: key.sessionID, PeerSessionID: key.peerSessionID, NotifiedAt: s.clock().UTC()}
				if err := store.UpsertSessionOverlapNotice(ctx, notice); err != nil {
					recordError(err)
				}
			}
			if nudgeErr != nil {
				recordError(fmt.Errorf("nudge collision recipient %s: %w", key.sessionID, nudgeErr))
			}
		}
	}
	return reconcileErr
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
