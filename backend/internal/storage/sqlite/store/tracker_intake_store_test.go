package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCreateTrackerIntakeSessionAtomicallyAdmitsThenPromotes(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")
	limits := domain.AutomatedSessionLimits{MaxConcurrent: 1, MaxStartsPerPass: 1}

	first := sampleRecord("demo")
	first.IssueID = "github:acme/demo#1"
	created, err := store.CreateTrackerIntakeSession(ctx, first, limits)
	if err != nil {
		t.Fatalf("CreateTrackerIntakeSession(first): %v", err)
	}
	handled, err := store.ListTrackerIntakeHandled(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(handled) != 0 {
		t.Fatalf("handled records before launch commit = %+v, want pending claim hidden", handled)
	}

	duplicate := sampleRecord("demo")
	duplicate.IssueID = first.IssueID
	if _, err := store.CreateTrackerIntakeSession(ctx, duplicate, limits); !errors.Is(err, ports.ErrTrackerIntakeActive) {
		t.Fatalf("CreateTrackerIntakeSession(pending duplicate) error = %v, want ErrTrackerIntakeActive", err)
	}

	when := time.Date(2026, 11, 17, 16, 0, 0, 0, time.UTC)
	if err := store.CommitTrackerIntakeSession(ctx, created.ID, when); err != nil {
		t.Fatalf("CommitTrackerIntakeSession(): %v", err)
	}
	handled, err = store.ListTrackerIntakeHandled(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(handled) != 1 || handled[0].IssueID != first.IssueID || handled[0].SessionID != created.ID || !handled[0].HandledAt.Equal(when) {
		t.Fatalf("handled records = %+v, want committed delivery for %s", handled, created.ID)
	}

	second := sampleRecord("demo")
	second.IssueID = "github:acme/demo#2"
	if _, err := store.CreateTrackerIntakeSession(ctx, second, limits); !errors.Is(err, ports.ErrAutomatedSessionCapacity) {
		t.Fatalf("CreateTrackerIntakeSession(at capacity) error = %v, want ErrAutomatedSessionCapacity", err)
	}
}

func TestTrackerIntakeFailedLaunchRollbackReleasesPendingClaim(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")

	record := sampleRecord("demo")
	record.Metadata.WorkspacePath = ""
	record.Metadata.Branch = ""
	record.IssueID = "github:acme/demo#12"
	created, err := store.CreateTrackerIntakeSession(ctx, record, domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteSession(ctx, created.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteSession(seed) = %t, %v", deleted, err)
	}
	if err := store.ReconcileTrackerIntakeSession(ctx, created.ID, time.Now()); err != nil {
		t.Fatalf("ReconcileTrackerIntakeSession(): %v", err)
	}

	handled, err := store.ListTrackerIntakeHandled(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(handled) != 0 {
		t.Fatalf("handled after launch rollback = %+v, want no delivery proof", handled)
	}
	retry := sampleRecord("demo")
	retry.IssueID = record.IssueID
	if _, err := store.CreateTrackerIntakeSession(ctx, retry, domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2}); err != nil {
		t.Fatalf("CreateTrackerIntakeSession(retry): %v", err)
	}
}

func TestCreateTrackerIntakeSessionRejectsLiveSessionForSameIssue(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")

	manual := sampleRecord("demo")
	manual.IssueID = "github:acme/demo#18"
	if _, err := store.CreateSession(ctx, manual); err != nil {
		t.Fatal(err)
	}
	intake := sampleRecord("demo")
	intake.IssueID = manual.IssueID
	_, err := store.CreateTrackerIntakeSession(ctx, intake, domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2})
	if !errors.Is(err, ports.ErrTrackerIntakeActive) {
		t.Fatalf("CreateTrackerIntakeSession(same live issue) error = %v, want ErrTrackerIntakeActive", err)
	}
}

func TestReconcileTrackerIntakeClaimsPromotesLaunchedSessionAndKeepsPrelaunchClaimsPending(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")
	limits := domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2}

	seed := sampleRecord("demo")
	seed.IssueID = "github:acme/demo#20"
	seed.Metadata.WorkspacePath = ""
	seed.Metadata.RuntimeHandleID = ""
	seed.Metadata.AgentSessionID = ""
	seed.Metadata.Prompt = ""
	seed.Metadata.LatestUserPrompt = ""
	seed.Metadata.LatestUserPromptAt = time.Time{}
	seed.Metadata.LatestAssistantUpdate = ""
	seed.Metadata.NativeTranscriptPath = ""
	seedClaim, err := store.CreateTrackerIntakeSession(ctx, seed, limits)
	if err != nil {
		t.Fatal(err)
	}
	workspaceOnly := seed
	workspaceOnly.IssueID = "github:acme/demo#21"
	workspaceOnly.Metadata.WorkspacePath = "/tmp/demo-21"
	workspaceClaim, err := store.CreateTrackerIntakeSession(ctx, workspaceOnly, limits)
	if err != nil {
		t.Fatal(err)
	}
	progressed := seed
	progressed.IssueID = "github:acme/demo#22"
	progressedClaim, err := store.CreateTrackerIntakeSession(ctx, progressed, limits)
	if err != nil {
		t.Fatal(err)
	}
	progressedClaim.Metadata.RuntimeHandleID = "runtime-21"
	if err := store.UpdateSession(ctx, progressedClaim); err != nil {
		t.Fatal(err)
	}

	when := time.Date(2026, 11, 17, 16, 5, 0, 0, time.UTC)
	if err := store.ReconcileTrackerIntakeClaims(ctx, "demo", when); err != nil {
		t.Fatalf("ReconcileTrackerIntakeClaims(): %v", err)
	}
	handled, err := store.ListTrackerIntakeHandled(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(handled) != 1 || handled[0].SessionID != progressedClaim.ID || !handled[0].HandledAt.Equal(when) {
		t.Fatalf("handled after reconcile = %+v, want only launched claim", handled)
	}
	if _, err := store.CreateTrackerIntakeSession(ctx, seed, limits); !errors.Is(err, ports.ErrTrackerIntakeActive) {
		t.Fatalf("still-live seed claim became retryable: %v", err)
	}
	if _, err := store.CreateTrackerIntakeSession(ctx, workspaceOnly, limits); !errors.Is(err, ports.ErrTrackerIntakeActive) {
		t.Fatalf("workspace-only claim became handled or retryable: %v", err)
	}
	if seedClaim.ID == workspaceClaim.ID || seedClaim.ID == progressedClaim.ID || workspaceClaim.ID == progressedClaim.ID {
		t.Fatal("test setup reused a session id")
	}
}

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
