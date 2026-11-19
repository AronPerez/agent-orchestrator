package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNotificationStore_InsertListAndDedupe(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.NotificationRecord{
		ID:        "ntf_1",
		SessionID: sess.ID,
		ProjectID: sess.ProjectID,
		Type:      domain.NotificationNeedsInput,
		Title:     "checkout-flow needs input",
		Status:    domain.NotificationUnread,
		CreatedAt: now,
	}
	created, inserted, err := s.CreateNotification(ctx, rec)
	if err != nil || !inserted {
		t.Fatalf("CreateNotification inserted=%v err=%v", inserted, err)
	}
	if created.ID != rec.ID || created.Title != rec.Title {
		t.Fatalf("created = %+v", created)
	}
	dup := rec
	dup.ID = "ntf_2"
	_, inserted, err = s.CreateNotification(ctx, dup)
	if err != nil || inserted {
		t.Fatalf("duplicate inserted=%v err=%v, want false nil", inserted, err)
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListUnread, time.Time{}, "", 10)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "ntf_1" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestNotificationStore_SessionStalledRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.NotificationRecord{
		ID: "ntf_stalled", SessionID: sess.ID, ProjectID: sess.ProjectID,
		Type: domain.NotificationSessionStalled, Title: "checkout-flow stalled",
		Status: domain.NotificationUnread, CreatedAt: now,
		SourceKey: "session-contract-stall:" + string(sess.ID) + ":1",
	}
	created, inserted, err := s.CreateNotification(ctx, rec)
	if err != nil || !inserted || created.Type != domain.NotificationSessionStalled {
		t.Fatalf("created=%+v inserted=%v err=%v", created, inserted, err)
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListAll, time.Time{}, "", 10)
	if err != nil || len(rows) != 1 || rows[0].ID != rec.ID || rows[0].Type != domain.NotificationSessionStalled {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestNotificationStore_SourceKeyDedupesTerminalReviewResult(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rec := domain.NotificationRecord{
		ID: "ntf_1", SessionID: sess.ID, ProjectID: sess.ProjectID,
		PRURL: "https://github.com/acme/app/pull/42", Type: domain.NotificationReviewCompleted,
		Title: "Review complete", Status: domain.NotificationUnread,
		CreatedAt: time.Now().UTC().Truncate(time.Second), SourceKey: "review_run:run-1",
	}
	if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
		t.Fatalf("first result inserted=%v err=%v", inserted, err)
	}
	if _, ok, err := s.MarkNotificationRead(ctx, rec.ID); err != nil || !ok {
		t.Fatalf("mark read ok=%v err=%v", ok, err)
	}
	dup := rec
	dup.ID = "ntf_retry"
	if got, inserted, err := s.CreateNotification(ctx, dup); err != nil || inserted || got.ID != rec.ID {
		t.Fatalf("retry result = %+v inserted=%v err=%v", got, inserted, err)
	}
	next := rec
	next.ID = "ntf_2"
	next.SourceKey = "review_run:run-2"
	if _, inserted, err := s.CreateNotification(ctx, next); err != nil || !inserted {
		t.Fatalf("distinct review result inserted=%v err=%v", inserted, err)
	}
}

// Seeing a notification is not the same as fixing what it reported. Dedupe is
// keyed on the issue being open, so the same session cannot be re-notified
// about a pause it is still sitting in — only resolving it reopens dedupe.
func TestNotificationStore_ResolutionNotReadReopensDedupe(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.NotificationRecord{
		ID:        "ntf_1",
		SessionID: sess.ID,
		ProjectID: sess.ProjectID,
		Type:      domain.NotificationNeedsInput,
		Title:     "checkout-flow needs input",
		Status:    domain.NotificationUnread,
		CreatedAt: now,
	}
	if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
		t.Fatalf("CreateNotification inserted=%v err=%v", inserted, err)
	}
	read, ok, err := s.MarkNotificationRead(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("MarkNotificationRead ok=%v err=%v", ok, err)
	}
	if read.Status != domain.NotificationRead {
		t.Fatalf("status = %q, want read", read.Status)
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListUnread, time.Time{}, "", 10)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}
	again := rec
	again.ID = "ntf_2"
	again.CreatedAt = now.Add(time.Minute)
	if _, inserted, err := s.CreateNotification(ctx, again); err != nil || inserted {
		t.Fatalf("CreateNotification while unresolved inserted=%v err=%v, want false", inserted, err)
	}

	resolvedAt := now.Add(2 * time.Minute)
	resolved, err := s.ResolveSessionNotifications(ctx, sess.ID, domain.NotificationNeedsInput, resolvedAt)
	if err != nil {
		t.Fatalf("ResolveSessionNotifications: %v", err)
	}
	if len(resolved) != 1 || resolved[0].ID != rec.ID || !resolved[0].ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("resolved = %+v", resolved)
	}
	if _, inserted, err := s.CreateNotification(ctx, again); err != nil || !inserted {
		t.Fatalf("CreateNotification after resolve inserted=%v err=%v, want true", inserted, err)
	}
}

// The unresolved list is independent of the seen state and excludes terminal
// facts: a merged PR is worth seeing once, never worth resolving.
func TestNotificationStore_ListUnresolvedIgnoresSeenAndTerminalTypes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	seed := func(id string, typ domain.NotificationType, prURL string) domain.NotificationRecord {
		rec := domain.NotificationRecord{
			ID: id, SessionID: sess.ID, ProjectID: sess.ProjectID, PRURL: prURL, Type: typ,
			Title: id, Status: domain.NotificationUnread, CreatedAt: now,
		}
		if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
			t.Fatalf("CreateNotification %s inserted=%v err=%v", id, inserted, err)
		}
		return rec
	}
	seed("ntf_input", domain.NotificationNeedsInput, "")
	seed("ntf_ready", domain.NotificationReadyToMerge, "https://github.com/o/r/pull/1")
	seed("ntf_merged", domain.NotificationPRMerged, "https://github.com/o/r/pull/2")
	if _, err := s.MarkAllNotificationsRead(ctx); err != nil {
		t.Fatalf("MarkAllNotificationsRead: %v", err)
	}

	rows, err := s.ListNotifications(ctx, domain.NotificationListUnresolved, time.Time{}, "", 10)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	if len(ids) != 2 || ids[0] == "ntf_merged" || ids[1] == "ntf_merged" {
		t.Fatalf("unresolved ids = %v, want the two open issues", ids)
	}
	count, err := s.CountUnresolvedNotifications(ctx)
	if err != nil || count != 2 {
		t.Fatalf("CountUnresolvedNotifications = %d err=%v, want 2", count, err)
	}

	// Resolving by PR follows the PR, not the session.
	resolved, err := s.ResolvePRNotifications(ctx, "https://github.com/o/r/pull/1", domain.NotificationReadyToMerge, now)
	if err != nil || len(resolved) != 1 || resolved[0].ID != "ntf_ready" {
		t.Fatalf("ResolvePRNotifications = %+v err=%v", resolved, err)
	}
	if count, err := s.CountUnresolvedNotifications(ctx); err != nil || count != 1 {
		t.Fatalf("CountUnresolvedNotifications = %d err=%v, want 1", count, err)
	}
}

func TestNotificationStore_MarkReadMissing(t *testing.T) {
	s := newTestStore(t)
	_, ok, err := s.MarkNotificationRead(context.Background(), "missing")
	if err != nil || ok {
		t.Fatalf("MarkNotificationRead ok=%v err=%v, want false nil", ok, err)
	}
}

func TestNotificationStore_MarkAllRead(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	for _, rec := range []domain.NotificationRecord{
		{ID: "ntf_1", SessionID: sess.ID, ProjectID: sess.ProjectID, Type: domain.NotificationNeedsInput, Title: "one", Status: domain.NotificationUnread, CreatedAt: base},
		{ID: "ntf_2", SessionID: sess.ID, ProjectID: sess.ProjectID, PRURL: "https://github.com/o/r/pull/1", Type: domain.NotificationReadyToMerge, Title: "two", Status: domain.NotificationUnread, CreatedAt: base.Add(time.Minute)},
	} {
		if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
			t.Fatalf("insert %s inserted=%v err=%v", rec.ID, inserted, err)
		}
	}
	updated, err := s.MarkAllNotificationsRead(ctx)
	if err != nil {
		t.Fatalf("MarkAllNotificationsRead: %v", err)
	}
	if updated != 2 {
		t.Fatalf("updated = %d, want 2", updated)
	}
	updated, err = s.MarkAllNotificationsRead(ctx)
	if err != nil || updated != 0 {
		t.Fatalf("second mark-all updated=%d err=%v, want 0 nil", updated, err)
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListUnread, time.Time{}, "", 10)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("unread rows = %+v, want none", rows)
	}
}

func TestNotificationStore_ClearAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, _ := s.CreateSession(ctx, sampleRecord("mer"))
	for _, id := range []string{"ntf_1", "ntf_2"} {
		rec := domain.NotificationRecord{ID: id, SessionID: sess.ID, ProjectID: sess.ProjectID, Type: domain.NotificationNeedsInput, Title: id, Status: domain.NotificationUnread, CreatedAt: time.Now()}
		if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
			t.Fatalf("CreateNotification %s inserted=%v err=%v", id, inserted, err)
		}
		_, _ = s.ResolveSessionNotifications(ctx, sess.ID, domain.NotificationNeedsInput, time.Now())
		_, _, _ = s.MarkNotificationRead(ctx, id)
	}
	count, err := s.ClearAllNotifications(ctx)
	if err != nil || count != 2 {
		t.Fatalf("ClearAllNotifications count=%d err=%v", count, err)
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListAll, time.Time{}, "", 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if count, err := s.ClearAllNotifications(ctx); err != nil || count != 0 {
		t.Fatalf("second ClearAllNotifications count=%d err=%v, want 0 nil", count, err)
	}
}

// A repeated ready SCM observation is not a new transition. Clear-all must
// retain the open dedupe fact until the PR stops being ready, then allow a
// later ready transition to create a fresh notification.
func TestNotificationStore_ClearAllRetainsDedupeUntilResolution(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	prURL := "https://github.com/o/r/pull/1"
	rec := domain.NotificationRecord{
		ID: "ntf_1", SessionID: sess.ID, ProjectID: sess.ProjectID, PRURL: prURL,
		Type: domain.NotificationReadyToMerge, Title: "ready", Status: domain.NotificationUnread, CreatedAt: now,
	}
	if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
		t.Fatalf("initial CreateNotification inserted=%v err=%v", inserted, err)
	}
	if count, err := s.ClearAllNotifications(ctx); err != nil || count != 1 {
		t.Fatalf("ClearAllNotifications count=%d err=%v, want 1 nil", count, err)
	}

	repeated := rec
	repeated.ID = "ntf_repeat"
	repeated.CreatedAt = now.Add(time.Minute)
	if _, inserted, err := s.CreateNotification(ctx, repeated); err != nil || inserted {
		t.Fatalf("repeated ready observation inserted=%v err=%v, want false nil", inserted, err)
	}
	for _, status := range []domain.NotificationListStatus{
		domain.NotificationListUnread,
		domain.NotificationListUnresolved,
		domain.NotificationListAll,
	} {
		rows, err := s.ListNotifications(ctx, status, time.Time{}, "", 10)
		if err != nil || len(rows) != 0 {
			t.Fatalf("ListNotifications(%s) rows=%+v err=%v, want none", status, rows, err)
		}
	}
	if unread, err := s.CountUnreadNotifications(ctx); err != nil || unread != 0 {
		t.Fatalf("CountUnreadNotifications=%d err=%v, want 0 nil", unread, err)
	}
	if unresolved, err := s.CountUnresolvedNotifications(ctx); err != nil || unresolved != 0 {
		t.Fatalf("CountUnresolvedNotifications=%d err=%v, want 0 nil", unresolved, err)
	}

	resolvedAt := now.Add(2 * time.Minute)
	resolved, err := s.ResolvePRNotifications(ctx, prURL, domain.NotificationReadyToMerge, resolvedAt)
	if err != nil || len(resolved) != 0 {
		t.Fatalf("ResolvePRNotifications=%+v err=%v, want no visible resolved event", resolved, err)
	}
	fresh := rec
	fresh.ID = "ntf_fresh"
	fresh.CreatedAt = now.Add(3 * time.Minute)
	if _, inserted, err := s.CreateNotification(ctx, fresh); err != nil || !inserted {
		t.Fatalf("ready observation after resolution inserted=%v err=%v, want true nil", inserted, err)
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListAll, time.Time{}, "", 10)
	if err != nil || len(rows) != 1 || rows[0].ID != fresh.ID {
		t.Fatalf("visible rows=%+v err=%v, want fresh notification", rows, err)
	}
}

func TestNotificationStore_Delete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, _ := s.CreateSession(ctx, sampleRecord("mer"))
	rec := domain.NotificationRecord{
		ID: "ntf_1", SessionID: sess.ID, ProjectID: sess.ProjectID, Type: domain.NotificationNeedsInput,
		Title: "one", Status: domain.NotificationUnread, CreatedAt: time.Now(),
	}
	if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
		t.Fatalf("CreateNotification inserted=%v err=%v", inserted, err)
	}

	deleted, ok, err := s.DeleteNotification(ctx, rec.ID)
	if err != nil || !ok || deleted.ID != rec.ID || deleted.ProjectID != rec.ProjectID {
		t.Fatalf("DeleteNotification deleted=%+v ok=%v err=%v", deleted, ok, err)
	}
	if _, ok, err := s.DeleteNotification(ctx, rec.ID); err != nil || ok {
		t.Fatalf("second DeleteNotification ok=%v err=%v, want false nil", ok, err)
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListAll, time.Time{}, "", 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

// A repeated ready SCM observation is not a new transition. Per-item delete
// must retain the open dedupe fact until the PR stops being ready, then allow a
// later ready transition to create a fresh notification.
func TestNotificationStore_DeleteRetainsDedupeUntilResolution(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	prURL := "https://github.com/o/r/pull/1"
	rec := domain.NotificationRecord{
		ID: "ntf_1", SessionID: sess.ID, ProjectID: sess.ProjectID, PRURL: prURL,
		Type: domain.NotificationReadyToMerge, Title: "ready", Status: domain.NotificationUnread, CreatedAt: now,
	}
	if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
		t.Fatalf("initial CreateNotification inserted=%v err=%v", inserted, err)
	}
	deleted, ok, err := s.DeleteNotification(ctx, rec.ID)
	if err != nil || !ok || deleted.ID != rec.ID || deleted.Status != domain.NotificationUnread {
		t.Fatalf("DeleteNotification deleted=%+v ok=%v err=%v", deleted, ok, err)
	}

	repeated := rec
	repeated.ID = "ntf_repeat"
	repeated.CreatedAt = now.Add(time.Minute)
	if _, inserted, err := s.CreateNotification(ctx, repeated); err != nil || inserted {
		t.Fatalf("repeated ready observation inserted=%v err=%v, want false nil", inserted, err)
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListAll, time.Time{}, "", 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("visible rows=%+v err=%v, want none", rows, err)
	}
	if unread, err := s.CountUnreadNotifications(ctx); err != nil || unread != 0 {
		t.Fatalf("CountUnreadNotifications=%d err=%v, want 0 nil", unread, err)
	}
	if unresolved, err := s.CountUnresolvedNotifications(ctx); err != nil || unresolved != 0 {
		t.Fatalf("CountUnresolvedNotifications=%d err=%v, want 0 nil", unresolved, err)
	}

	resolvedAt := now.Add(2 * time.Minute)
	resolved, err := s.ResolvePRNotifications(ctx, prURL, domain.NotificationReadyToMerge, resolvedAt)
	if err != nil || len(resolved) != 0 {
		t.Fatalf("ResolvePRNotifications=%+v err=%v, want no visible resolved event", resolved, err)
	}
	fresh := rec
	fresh.ID = "ntf_fresh"
	fresh.CreatedAt = now.Add(3 * time.Minute)
	if _, inserted, err := s.CreateNotification(ctx, fresh); err != nil || !inserted {
		t.Fatalf("ready observation after resolution inserted=%v err=%v, want true nil", inserted, err)
	}
	rows, err = s.ListNotifications(ctx, domain.NotificationListAll, time.Time{}, "", 10)
	if err != nil || len(rows) != 1 || rows[0].ID != fresh.ID {
		t.Fatalf("visible rows=%+v err=%v, want fresh notification", rows, err)
	}
}

func TestNotificationStore_ListUnreadNewestFirstAcrossProjects(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	seedProject(t, s, "ao")
	mer, _ := s.CreateSession(ctx, sampleRecord("mer"))
	ao, _ := s.CreateSession(ctx, sampleRecord("ao"))
	base := time.Now().UTC().Truncate(time.Second)
	for _, rec := range []domain.NotificationRecord{
		{ID: "old", SessionID: mer.ID, ProjectID: mer.ProjectID, Type: domain.NotificationNeedsInput, Title: "old", Status: domain.NotificationUnread, CreatedAt: base},
		{ID: "new", SessionID: mer.ID, ProjectID: mer.ProjectID, PRURL: "https://github.com/o/r/pull/1", Type: domain.NotificationReadyToMerge, Title: "new", Status: domain.NotificationUnread, CreatedAt: base.Add(time.Minute)},
		{ID: "other", SessionID: ao.ID, ProjectID: ao.ProjectID, Type: domain.NotificationNeedsInput, Title: "other", Status: domain.NotificationUnread, CreatedAt: base.Add(2 * time.Minute)},
	} {
		if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
			t.Fatalf("insert %s inserted=%v err=%v", rec.ID, inserted, err)
		}
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListUnread, time.Time{}, "", 2)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != "other" || rows[1].ID != "new" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestNotificationStore_ListAllUsesStableCursorWithoutAgeCutoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	for _, rec := range []domain.NotificationRecord{
		{ID: "same-z", SessionID: sess.ID, ProjectID: sess.ProjectID, Type: domain.NotificationNeedsInput, Title: "same z", Status: domain.NotificationUnread, CreatedAt: now},
		{ID: "same-a", SessionID: sess.ID, ProjectID: sess.ProjectID, PRURL: "https://github.com/o/r/pull/1", Type: domain.NotificationPRMerged, Title: "same a", Status: domain.NotificationRead, CreatedAt: now},
		{ID: "old", SessionID: sess.ID, ProjectID: sess.ProjectID, PRURL: "https://github.com/o/r/pull/old", Type: domain.NotificationPRClosedUnmerged, Title: "old", Status: domain.NotificationUnread, CreatedAt: now.Add(-30 * 24 * time.Hour)},
	} {
		if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
			t.Fatalf("insert %s inserted=%v err=%v", rec.ID, inserted, err)
		}
	}

	rows, err := s.ListNotifications(ctx, domain.NotificationListAll, time.Time{}, "", 2)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != "same-z" || rows[1].ID != "same-a" {
		t.Fatalf("rows = %+v", rows)
	}
	older, err := s.ListNotifications(ctx, domain.NotificationListAll, rows[1].CreatedAt, rows[1].ID, 2)
	if err != nil {
		t.Fatalf("ListNotifications older: %v", err)
	}
	if len(older) != 1 || older[0].ID != "old" {
		t.Fatalf("older = %+v", older)
	}
	count, err := s.CountUnreadNotifications(ctx)
	if err != nil || count != 2 {
		t.Fatalf("unread count=%d err=%v", count, err)
	}
}

func TestNotificationStore_CheckConstraintRejectsInvalidStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, _ := s.CreateSession(ctx, sampleRecord("mer"))
	_, _, err := s.CreateNotification(ctx, domain.NotificationRecord{
		ID: "bad", SessionID: sess.ID, ProjectID: sess.ProjectID, Type: domain.NotificationNeedsInput,
		Title: "bad", Status: "archived", CreatedAt: time.Now(),
	})
	if !errors.Is(err, domain.ErrInvalidNotificationStatus) {
		t.Fatalf("err = %v, want invalid status", err)
	}
}

// Readiness is more than open/closed. The SCM observer persists PR facts before
// it asks lifecycle to resolve, so a crash in that window leaves an open PR
// whose CI (or review, or mergeability) already moved against it. Startup
// reconciliation has to apply the same rule the live path does.
func TestNotificationStore_ReconcileAppliesFullReadyToMergePredicate(t *testing.T) {
	ready := domain.PullRequest{
		CI: domain.CIPassing, Review: domain.ReviewApproved, Mergeability: domain.MergeMergeable,
	}
	withPR := func(mutate func(*domain.PullRequest)) domain.PullRequest {
		pr := ready
		mutate(&pr)
		return pr
	}
	for _, tt := range []struct {
		name         string
		pr           domain.PullRequest
		comments     []domain.PullRequestComment
		wantResolved bool
	}{
		{name: "still ready", pr: ready},
		{name: "merged", pr: withPR(func(p *domain.PullRequest) { p.Merged = true }), wantResolved: true},
		{name: "closed", pr: withPR(func(p *domain.PullRequest) { p.Closed = true }), wantResolved: true},
		{name: "draft", pr: withPR(func(p *domain.PullRequest) { p.Draft = true }), wantResolved: true},
		{name: "ci failing", pr: withPR(func(p *domain.PullRequest) { p.CI = domain.CIFailing }), wantResolved: true},
		{name: "ci pending", pr: withPR(func(p *domain.PullRequest) { p.CI = domain.CIPending }), wantResolved: true},
		{
			name:         "changes requested",
			pr:           withPR(func(p *domain.PullRequest) { p.Review = domain.ReviewChangesRequest }),
			wantResolved: true,
		},
		{
			name:         "not mergeable",
			pr:           withPR(func(p *domain.PullRequest) { p.Mergeability = domain.MergeConflicting }),
			wantResolved: true,
		},
		{
			name:         "unresolved human comment",
			pr:           ready,
			comments:     []domain.PullRequestComment{{ID: "c1", Author: "dev", Body: "please fix", CreatedAt: time.Now().UTC()}},
			wantResolved: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			seedProject(t, s, "mer")
			sess, err := s.CreateSession(ctx, sampleRecord("mer"))
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			prURL := "https://github.com/o/r/pull/1"
			pr := tt.pr
			pr.URL = prURL
			pr.SessionID = sess.ID
			pr.UpdatedAt = now
			if err := s.WritePR(ctx, pr, nil, tt.comments); err != nil {
				t.Fatalf("WritePR: %v", err)
			}
			rec := domain.NotificationRecord{
				ID: "ntf_ready", SessionID: sess.ID, ProjectID: sess.ProjectID, PRURL: prURL,
				Type: domain.NotificationReadyToMerge, Title: "ready", Status: domain.NotificationUnread, CreatedAt: now,
			}
			if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
				t.Fatalf("CreateNotification inserted=%v err=%v", inserted, err)
			}

			resolved, err := s.ReconcileResolvedNotifications(ctx, now.Add(time.Minute))
			if err != nil {
				t.Fatalf("ReconcileResolvedNotifications: %v", err)
			}
			gotResolved := false
			for _, r := range resolved {
				if r.ID == "ntf_ready" {
					gotResolved = true
				}
			}
			if gotResolved != tt.wantResolved {
				t.Fatalf("resolved = %v, want %v", gotResolved, tt.wantResolved)
			}
			count, err := s.CountUnresolvedNotifications(ctx)
			if err != nil {
				t.Fatalf("CountUnresolvedNotifications: %v", err)
			}
			wantCount := int64(1)
			if tt.wantResolved {
				wantCount = 0
			}
			if count != wantCount {
				t.Fatalf("unresolved count = %d, want %d", count, wantCount)
			}
		})
	}
}

func TestNotificationStore_ReconcileTreatsAnchoredBotCommentsAsActionable(t *testing.T) {
	for _, tt := range []struct {
		name         string
		comment      domain.PullRequestComment
		wantResolved bool
	}{
		{
			name: "anchored bot comment",
			comment: domain.PullRequestComment{
				ID: "bot-1", Author: "review-bot[bot]", IsBot: true,
				File: "main.go", Line: 7, Body: "please fix", CreatedAt: time.Now().UTC(),
			},
			wantResolved: true,
		},
		{
			name: "unanchored bot chatter",
			comment: domain.PullRequestComment{
				ID: "bot-2", Author: "review-bot[bot]", IsBot: true,
				Body: "status update", CreatedAt: time.Now().UTC(),
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			seedProject(t, s, "mer")
			sess, err := s.CreateSession(ctx, sampleRecord("mer"))
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			prURL := "https://github.com/o/r/pull/1"
			pr := domain.PullRequest{
				URL: prURL, SessionID: sess.ID, CI: domain.CIPassing,
				Review: domain.ReviewApproved, Mergeability: domain.MergeMergeable, UpdatedAt: now,
			}
			if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, []domain.PullRequestComment{tt.comment}, ports.ReviewWriteReplace); err != nil {
				t.Fatalf("WriteSCMObservation: %v", err)
			}
			rec := domain.NotificationRecord{
				ID: "ntf_ready", SessionID: sess.ID, ProjectID: sess.ProjectID, PRURL: prURL,
				Type: domain.NotificationReadyToMerge, Title: "ready", Status: domain.NotificationUnread, CreatedAt: now,
			}
			if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
				t.Fatalf("CreateNotification inserted=%v err=%v", inserted, err)
			}

			resolved, err := s.ReconcileResolvedNotifications(ctx, now.Add(time.Minute))
			if err != nil {
				t.Fatalf("ReconcileResolvedNotifications: %v", err)
			}
			gotResolved := false
			for _, r := range resolved {
				if r.ID == "ntf_ready" {
					gotResolved = true
				}
			}
			if gotResolved != tt.wantResolved {
				t.Fatalf("resolved = %v, want %v", gotResolved, tt.wantResolved)
			}
		})
	}
}

// Acknowledging must be scoped to the rows a client actually rendered.
func TestNotificationStore_MarkNotificationsReadOnlyTouchesGivenIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for i, id := range []string{"ntf_1", "ntf_2", "ntf_3"} {
		rec := domain.NotificationRecord{
			ID: id, SessionID: sess.ID, ProjectID: sess.ProjectID,
			PRURL: "https://github.com/o/r/pull/" + id,
			Type:  domain.NotificationPRMerged, Title: id,
			Status: domain.NotificationUnread, CreatedAt: now.Add(-time.Duration(i) * time.Minute),
		}
		if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
			t.Fatalf("CreateNotification %s inserted=%v err=%v", id, inserted, err)
		}
	}

	updated, err := s.MarkNotificationsRead(ctx, []string{"ntf_1", "ntf_2"})
	if err != nil || updated != 2 {
		t.Fatalf("MarkNotificationsRead updated=%d err=%v, want 2", updated, err)
	}
	count, err := s.CountUnreadNotifications(ctx)
	if err != nil || count != 1 {
		t.Fatalf("unread count = %d err=%v, want 1 (ntf_3 must stay reachable)", count, err)
	}
}

func decisionNotificationRecord(id, callID string, at time.Time) domain.NotificationRecord {
	return domain.NotificationRecord{
		ID:                   id,
		Type:                 domain.NotificationDecisionNeeded,
		Title:                "Decision needed · ao",
		Body:                 "Ship it?",
		Status:               domain.NotificationUnread,
		CreatedAt:            at,
		SourceKey:            "decision:" + callID,
		DecisionCallID:       callID,
		DecisionOriginHostID: "h_origin",
	}
}

func TestNotificationStore_DecisionNotificationIsSessionless(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	created, inserted, err := s.CreateNotification(ctx, decisionNotificationRecord("ntf_dec", "dec_1", now))
	if err != nil || !inserted {
		t.Fatalf("CreateNotification inserted=%v err=%v", inserted, err)
	}
	if created.SessionID != "" || created.ProjectID != "" || created.DecisionCallID != "dec_1" ||
		created.DecisionOriginHostID != "h_origin" || created.Type != domain.NotificationDecisionNeeded {
		t.Fatalf("created = %+v", created)
	}
	// An empty session_id would violate the sessions foreign key, so a
	// successful insert proves the row stored NULL.
	unresolved, err := s.ListNotifications(ctx, domain.NotificationListUnresolved, time.Time{}, "", 10)
	if err != nil || len(unresolved) != 1 || unresolved[0].DecisionCallID != "dec_1" {
		t.Fatalf("unresolved=%+v err=%v", unresolved, err)
	}
	count, err := s.CountUnresolvedNotifications(ctx)
	if err != nil || count != 1 {
		t.Fatalf("unresolved count=%d err=%v", count, err)
	}
}

func TestNotificationStore_DecisionNotificationDedupesByCall(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if _, inserted, err := s.CreateNotification(ctx, decisionNotificationRecord("ntf_a", "dec_1", now)); err != nil || !inserted {
		t.Fatalf("first create inserted=%v err=%v", inserted, err)
	}
	existing, inserted, err := s.CreateNotification(ctx, decisionNotificationRecord("ntf_b", "dec_1", now.Add(time.Second)))
	if err != nil || inserted || existing.ID != "ntf_a" {
		t.Fatalf("retry existing=%+v inserted=%v err=%v", existing, inserted, err)
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListAll, time.Time{}, "", 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestNotificationStore_SessionDeleteKeepsDecisionNotifications(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	now := time.Now().UTC().Truncate(time.Second)
	// DeleteSession only removes seed-state rows, so cascade is observable on one.
	sess, err := s.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, _, err := s.CreateNotification(ctx, domain.NotificationRecord{
		ID: "ntf_session", SessionID: sess.ID, ProjectID: sess.ProjectID, Type: domain.NotificationNeedsInput,
		Title: "needs input", Status: domain.NotificationUnread, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateNotification(ctx, decisionNotificationRecord("ntf_dec", "dec_1", now)); err != nil {
		t.Fatal(err)
	}
	if deleted, err := s.DeleteSession(ctx, sess.ID); err != nil || !deleted {
		t.Fatalf("DeleteSession deleted=%v err=%v", deleted, err)
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListAll, time.Time{}, "", 10)
	if err != nil || len(rows) != 1 || rows[0].ID != "ntf_dec" {
		t.Fatalf("after session delete rows=%+v err=%v", rows, err)
	}
}

func decisionEntry(entryID, callID string, kind domain.DecisionEntryKind, payload any, at time.Time) domain.DecisionEntry {
	author := domain.DecisionAuthorPerson
	if kind == domain.DecisionKindWithdrawn {
		author = domain.DecisionAuthorSession
	}
	entry := domain.DecisionEntry{
		ID: entryID, Origin: "h_a", CallID: callID, Kind: kind, Payload: mustDecisionJSON(payload),
		WrittenAt: at, AuthorKind: author,
	}
	if author == domain.DecisionAuthorPerson {
		entry.AuthorSurface = domain.RequestSurfaceCLI
	} else {
		entry.AuthorSession = "w-1"
	}
	return entry
}

func TestNotificationStore_ReconcileResolvesStaleDecisionNotifications(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.UnixMilli(1_000_000).UTC()
	calls := []string{"dec_answer", "dec_dismiss", "dec_withdraw", "dec_wait", "dec_read_wait", "dec_outcome", "dec_hidden"}
	for i, callID := range calls {
		appendDecision(t, s, decisionRaised("dent_r"+callID, "h_a", callID, "w-1", "", "", at), at)
		if _, _, err := s.CreateNotification(ctx, decisionNotificationRecord("ntf_"+callID, callID, at.Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	// A notification whose call row does not exist (pruned or never projected).
	if _, _, err := s.CreateNotification(ctx, decisionNotificationRecord("ntf_dec_missing", "dec_missing", at)); err != nil {
		t.Fatal(err)
	}
	later := at.Add(time.Minute)
	appendDecision(t, s, decisionAnswered("dent_a", "h_a", "dec_answer", 1, later), later)
	appendDecision(t, s, decisionEntry("dent_d", "dec_dismiss", domain.DecisionKindDismissed, domain.DecisionDismissedPayload{}, later), later)
	appendDecision(t, s, decisionEntry("dent_w", "dec_withdraw", domain.DecisionKindWithdrawn, map[string]any{}, later), later)
	appendDecision(t, s, decisionAnswered("dent_o1", "h_a", "dec_outcome", 1, later), later)
	appendDecision(t, s, decisionAnswered("dent_h", "h_a", "dec_hidden", 1, later), later)
	if _, err := s.MarkNotificationsRead(ctx, []string{"ntf_dec_read_wait"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DeleteNotification(ctx, "ntf_dec_hidden"); err != nil {
		t.Fatal(err)
	}

	resolved, err := s.ReconcileResolvedNotifications(ctx, later)
	if err != nil {
		t.Fatalf("ReconcileResolvedNotifications: %v", err)
	}
	got := map[string]bool{}
	for _, rec := range resolved {
		got[rec.ID] = true
	}
	for _, id := range []string{"ntf_dec_answer", "ntf_dec_dismiss", "ntf_dec_withdraw", "ntf_dec_outcome", "ntf_dec_missing"} {
		if !got[id] {
			t.Fatalf("reconcile did not resolve %s; resolved=%v", id, got)
		}
	}
	if len(got) != 5 {
		t.Fatalf("reconcile resolved unexpected rows: %v", got)
	}
	unresolved, err := s.ListNotifications(ctx, domain.NotificationListUnresolved, time.Time{}, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	var open []string
	for _, rec := range unresolved {
		open = append(open, rec.ID)
	}
	if len(open) != 2 {
		t.Fatalf("still-waiting rows (read or unread) must stay open; open=%v", open)
	}
	// The hidden tombstone for a call that left Needs you is deleted so it
	// cannot block a later re-raise and never emits a resolved event.
	all, err := s.ListNotifications(ctx, domain.NotificationListAll, time.Time{}, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range all {
		if rec.ID == "ntf_dec_hidden" {
			t.Fatal("dismissed decision tombstone survived reconcile")
		}
	}
	// A surviving tombstone would be returned by the source-key lookup; once it
	// is deleted the answered call simply gets no new alert.
	probe, inserted, err := s.CreateNotification(ctx, decisionNotificationRecord("ntf_dec_hidden_2", "dec_hidden", later))
	if err != nil || inserted || probe.ID != "" {
		t.Fatalf("after reconcile: probe=%+v inserted=%v err=%v; want no tombstone and no new alert", probe, inserted, err)
	}

	again, err := s.ReconcileResolvedNotifications(ctx, later.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second reconcile re-resolved %+v", again)
	}
}

func TestNotificationStore_ResolveDecisionNotificationsByCall(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	for _, callID := range []string{"dec_1", "dec_2"} {
		if _, _, err := s.CreateNotification(ctx, decisionNotificationRecord("ntf_"+callID, callID, now)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ResolveDecisionNotifications(ctx, "dec_1", domain.NotificationDecisionNeeded, now)
	if err != nil || len(rows) != 1 || rows[0].ID != "ntf_dec_1" || !rows[0].Resolved() {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	again, err := s.ResolveDecisionNotifications(ctx, "dec_1", domain.NotificationDecisionNeeded, now)
	if err != nil || len(again) != 0 {
		t.Fatalf("second resolve rows=%+v err=%v", again, err)
	}
}

func TestNotificationStore_DecisionNotificationNotCreatedAfterCallLeavesNeedsYou(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.UnixMilli(1_000_000).UTC()
	appendDecision(t, s, decisionRaised("dent_r", "h_a", "dec_race", "w-1", "", "", at), at)
	// The projector's scan sees the call waiting...
	waiting, err := s.ListDecisionCallsByState(ctx, domain.DecisionStateNeedsYou, -1)
	if err != nil || len(waiting) != 1 {
		t.Fatalf("scan = %+v err=%v", waiting, err)
	}
	// ...then the person answers before the notification write lands.
	later := at.Add(time.Second)
	appendDecision(t, s, decisionAnswered("dent_a", "h_a", "dec_race", 1, later), later)

	created, inserted, err := s.CreateNotification(ctx, decisionNotificationRecord("ntf_race", "dec_race", later))
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	if inserted || created.ID != "" {
		t.Fatalf("alert created for a call that already left Needs you: inserted=%v created=%+v", inserted, created)
	}
	rows, err := s.ListNotifications(ctx, domain.NotificationListAll, time.Time{}, "", 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}
