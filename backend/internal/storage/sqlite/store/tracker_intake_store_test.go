package store_test

import (
	"context"
	"errors"
	"fmt"
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

func TestTrackerIntakeMissingSessionKeepsClaimWithPossibleRuntime(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")

	record := sampleRecord("demo")
	record.Metadata = domain.SessionMetadata{}
	record.IssueID = "github:acme/demo#12"
	limits := domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2}
	created, err := store.CreateTrackerIntakeSession(ctx, record, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReserveTrackerIntakeRecovery(ctx, created.ID, domain.TrackerIntakeRecoveryReservation{
		RuntimeLaunchID:  "launch-12",
		RuntimeHandleIDs: []string{"demo-1"},
	}); err != nil {
		t.Fatalf("ReserveTrackerIntakeRecovery(): %v", err)
	}
	deleted, err := store.DeleteSession(ctx, created.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteSession(seed) = %t, %v", deleted, err)
	}

	if err := store.ReconcileTrackerIntakeSession(ctx, created.ID, time.Now()); err != nil {
		t.Fatalf("ReconcileTrackerIntakeSession(): %v", err)
	}
	retry := sampleRecord("demo")
	retry.IssueID = record.IssueID
	if _, err := store.CreateTrackerIntakeSession(ctx, retry, limits); !errors.Is(err, ports.ErrTrackerIntakeActive) {
		t.Fatalf("CreateTrackerIntakeSession(retry) error = %v, want ErrTrackerIntakeActive", err)
	}
}

func TestTrackerIntakeConfirmedCleanupReleasesMissingSessionClaim(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")

	record := sampleRecord("demo")
	record.Metadata = domain.SessionMetadata{}
	record.IssueID = "github:acme/demo#13"
	limits := domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2}
	created, err := store.CreateTrackerIntakeSession(ctx, record, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReserveTrackerIntakeRecovery(ctx, created.ID, domain.TrackerIntakeRecoveryReservation{
		RuntimeLaunchID:  "launch-13",
		RuntimeHandleIDs: []string{"demo-1"},
		CleanupConfirmed: true,
	}); err != nil {
		t.Fatalf("ReserveTrackerIntakeRecovery(): %v", err)
	}
	deleted, err := store.DeleteSession(ctx, created.ID)
	if err != nil || !deleted {
		t.Fatalf("DeleteSession(seed) = %t, %v", deleted, err)
	}

	if err := store.ReconcileTrackerIntakeSession(ctx, created.ID, time.Now()); err != nil {
		t.Fatalf("ReconcileTrackerIntakeSession(): %v", err)
	}
	retry := sampleRecord("demo")
	retry.IssueID = record.IssueID
	if _, err := store.CreateTrackerIntakeSession(ctx, retry, limits); err != nil {
		t.Fatalf("CreateTrackerIntakeSession(retry): %v", err)
	}
}

func TestTrackerIntakeRuntimeReservationAtomicallyCheckpointsRecoveryIdentity(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")

	record := sampleRecord("demo")
	record.Metadata = domain.SessionMetadata{}
	record.IssueID = "github:acme/demo#12"
	created, err := store.CreateTrackerIntakeSession(ctx, record, domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2})
	if err != nil {
		t.Fatal(err)
	}
	reservation := domain.TrackerIntakeRecoveryReservation{
		Branch:            "ao/demo-12/root",
		WorkspacePath:     "/tmp/demo-12",
		WorkspaceRepoPath: "/tmp/demo-12",
		RuntimeLaunchID:   "launch-12",
		RuntimeHandleIDs:  []string{"ptyhost-v1:demo-1", "demo-1"},
	}
	if err := store.ReserveTrackerIntakeRecovery(ctx, created.ID, reservation); err != nil {
		t.Fatalf("ReserveTrackerIntakeRecovery(): %v", err)
	}

	got, pending, err := store.GetPendingTrackerIntakeRecovery(ctx, created.ID)
	if err != nil || !pending {
		t.Fatalf("GetPendingTrackerIntakeRecovery() = %+v, %t, %v", got, pending, err)
	}
	if got.RuntimeLaunchID != reservation.RuntimeLaunchID || got.WorkspacePath != reservation.WorkspacePath ||
		fmt.Sprint(got.RuntimeHandleIDs) != fmt.Sprint(reservation.RuntimeHandleIDs) {
		t.Fatalf("runtime reservation = %+v, want %+v", got, reservation)
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

func TestCreateTrackerIntakeSessionRejectsLegacyManualIssueReferences(t *testing.T) {
	tests := []struct {
		name      string
		manual    domain.IssueID
		canonical domain.IssueID
		wantMatch bool
	}{
		{name: "numeric", manual: "18", canonical: "github:acme/demo#18", wantMatch: true},
		{name: "number with hash", manual: "#18", canonical: "github:acme/demo#18", wantMatch: true},
		{name: "native github", manual: "acme/demo#18", canonical: "github:acme/demo#18", wantMatch: true},
		{name: "github url", manual: "https://github.com/acme/demo/issues/18", canonical: "github:acme/demo#18", wantMatch: true},
		{name: "gitlab url", manual: "https://gitlab.com/acme/demo/-/issues/18", canonical: "gitlab:acme/demo#18", wantMatch: true},
		{name: "self managed gitlab url", manual: "https://gitlab.one/acme/demo/-/issues/18", canonical: "gitlab:acme/demo#18@gitlab.one", wantMatch: true},
		{name: "different repository", manual: "github:other/demo#18", canonical: "github:acme/demo#18", wantMatch: false},
		{name: "different provider", manual: "github:acme/demo#18", canonical: "gitlab:acme/demo#18", wantMatch: false},
		{name: "different gitlab host", manual: "gitlab:acme/demo#18@gitlab.one", canonical: "gitlab:acme/demo#18@gitlab.two", wantMatch: false},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t)
			ctx := context.Background()
			seedProject(t, store, "demo")
			manual := sampleRecord("demo")
			manual.ID = domain.SessionID(fmt.Sprintf("manual-%d", i))
			manual.IssueID = tt.manual
			if _, err := store.CreateSession(ctx, manual); err != nil {
				t.Fatal(err)
			}
			intake := sampleRecord("demo")
			intake.ID = domain.SessionID(fmt.Sprintf("intake-%d", i))
			intake.IssueID = tt.canonical
			_, err := store.CreateTrackerIntakeSession(ctx, intake, domain.AutomatedSessionLimits{MaxConcurrent: 100, MaxStartsPerPass: 2})
			if tt.wantMatch && !errors.Is(err, ports.ErrTrackerIntakeActive) {
				t.Fatalf("CreateTrackerIntakeSession() error = %v, want ErrTrackerIntakeActive", err)
			}
			if !tt.wantMatch && err != nil {
				t.Fatalf("CreateTrackerIntakeSession() error = %v, want distinct scoped issue", err)
			}
		})
	}
}

func TestReconcileTrackerIntakeClaimsNeverInfersPromptDeliveryFromControllerMetadata(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")
	limits := domain.AutomatedSessionLimits{MaxConcurrent: 10, MaxStartsPerPass: 2}

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
	chat := seed
	chat.IssueID = "github:acme/demo#23"
	chat.Mode = domain.SessionModeChat
	chatClaim, err := store.CreateTrackerIntakeSession(ctx, chat, limits)
	if err != nil {
		t.Fatal(err)
	}
	chatClaim.Metadata.ProviderConversationID = "conversation-23"
	chatClaim.Metadata.ControllerGeneration = "controller-23"
	if err := store.UpdateSession(ctx, chatClaim); err != nil {
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
	if len(handled) != 0 {
		t.Fatalf("handled after reconcile = %+v, want no inferred prompt delivery", handled)
	}
	if _, err := store.CreateTrackerIntakeSession(ctx, seed, limits); !errors.Is(err, ports.ErrTrackerIntakeActive) {
		t.Fatalf("still-live seed claim became retryable: %v", err)
	}
	if _, err := store.CreateTrackerIntakeSession(ctx, workspaceOnly, limits); !errors.Is(err, ports.ErrTrackerIntakeActive) {
		t.Fatalf("workspace-only claim became handled or retryable: %v", err)
	}
	if _, err := store.CreateTrackerIntakeSession(ctx, progressed, limits); !errors.Is(err, ports.ErrTrackerIntakeActive) {
		t.Fatalf("controller metadata claim became handled or retryable: %v", err)
	}
	if _, err := store.CreateTrackerIntakeSession(ctx, chat, limits); !errors.Is(err, ports.ErrTrackerIntakeActive) {
		t.Fatalf("chat controller claim became handled or retryable: %v", err)
	}
	if seedClaim.ID == workspaceClaim.ID || seedClaim.ID == progressedClaim.ID || workspaceClaim.ID == progressedClaim.ID || chatClaim.ID == progressedClaim.ID {
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
