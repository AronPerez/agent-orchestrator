package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestTrackerIntakeHandledRecordsAreProjectScopedAndIdempotent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "one")
	seedProject(t, store, "two")

	when := time.Date(2026, 11, 17, 15, 30, 0, 0, time.UTC)
	issueID := domain.IssueID("github:acme/demo#7")
	for _, record := range []domain.TrackerIntakeHandledRecord{
		{ProjectID: "one", IssueID: issueID, SessionID: "one-1", HandledAt: when},
		{ProjectID: "two", IssueID: issueID, SessionID: "two-1", HandledAt: when.Add(time.Minute)},
		// Re-recording one delivery is a no-op, not a second row or an overwrite.
		{ProjectID: "one", IssueID: issueID, SessionID: "one-2", HandledAt: when.Add(2 * time.Minute)},
	} {
		if err := store.RecordTrackerIntakeHandled(ctx, record); err != nil {
			t.Fatalf("RecordTrackerIntakeHandled(%+v): %v", record, err)
		}
	}

	one, err := store.ListTrackerIntakeHandled(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	two, err := store.ListTrackerIntakeHandled(ctx, "two")
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].ProjectID != "one" || one[0].IssueID != issueID || one[0].SessionID != "one-1" || !one[0].HandledAt.Equal(when) {
		t.Fatalf("project one records = %+v", one)
	}
	if len(two) != 1 || two[0].ProjectID != "two" || two[0].IssueID != issueID || two[0].SessionID != "two-1" || !two[0].HandledAt.Equal(when.Add(time.Minute)) {
		t.Fatalf("project two records = %+v", two)
	}
}

func TestTrackerIntakeHandledRecordSurvivesSessionDeletion(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")

	record := sampleRecord("demo")
	record.IssueID = "github:acme/demo#12"
	created, err := store.CreateSession(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTrackerIntakeHandled(ctx, domain.TrackerIntakeHandledRecord{
		ProjectID: "demo",
		IssueID:   record.IssueID,
		SessionID: created.ID,
		HandledAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	created.IsTerminated = true
	if err := store.UpdateSession(ctx, created); err != nil {
		t.Fatal(err)
	}
	if deleted, err := store.DeleteTerminatedSession(ctx, created.ID); err != nil || !deleted {
		t.Fatalf("DeleteTerminatedSession() = %t, %v", deleted, err)
	}

	handled, err := store.ListTrackerIntakeHandled(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(handled) != 1 || handled[0].SessionID != created.ID || handled[0].IssueID != record.IssueID {
		t.Fatalf("handled after session deletion = %+v", handled)
	}
}
