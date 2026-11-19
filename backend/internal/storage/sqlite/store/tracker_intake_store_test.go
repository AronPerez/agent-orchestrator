package store_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
		RuntimeLaunchID:          "launch-13",
		RuntimeHandleIDs:         []string{"demo-1"},
		CapacityReleased:         true,
		ReleasedRuntimeLaunchID:  "launch-13",
		ReleasedRuntimeHandleIDs: []string{"demo-1"},
		CleanupConfirmed:         true,
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

func TestTrackerIntakeConfirmedCleanupRetainsOutstandingRuntimeTeardown(t *testing.T) {
	for _, ownerState := range []string{"missing", "terminated"} {
		t.Run(ownerState, func(t *testing.T) {
			store := newTestStore(t)
			ctx := context.Background()
			seedProject(t, store, "demo")

			record := sampleRecord("demo")
			record.Metadata = domain.SessionMetadata{}
			issueID := domain.IssueID("github:acme/demo#130")
			if ownerState == "terminated" {
				issueID = "github:acme/demo#131"
			}
			record.IssueID = issueID
			limits := domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2}
			created, err := store.CreateTrackerIntakeSession(ctx, record, limits)
			if err != nil {
				t.Fatal(err)
			}
			reservation := domain.TrackerIntakeRecoveryReservation{
				RuntimeLaunchID: "launch-130", RuntimeHandleIDs: []string{"runtime-130"},
				RuntimeTeardownHandleID: "runtime-130", RuntimeTeardownLaunchID: "launch-130",
				DeliveryPossible: true, CleanupConfirmed: true,
			}
			if err := store.ReserveTrackerIntakeRecovery(ctx, created.ID, reservation); err != nil {
				t.Fatalf("ReserveTrackerIntakeRecovery(): %v", err)
			}
			switch ownerState {
			case "missing":
				if deleted, err := store.DeleteSession(ctx, created.ID); err != nil || !deleted {
					t.Fatalf("DeleteSession() = %t, %v", deleted, err)
				}
			case "terminated":
				created.IsTerminated = true
				if err := store.UpdateSession(ctx, created); err != nil {
					t.Fatalf("UpdateSession(terminated): %v", err)
				}
			}

			if err := store.ReconcileTrackerIntakeSession(ctx, created.ID, time.Now()); err != nil {
				t.Fatalf("ReconcileTrackerIntakeSession(): %v", err)
			}
			got, pending, err := store.GetPendingTrackerIntakeRecovery(ctx, created.ID)
			if err != nil || !pending {
				t.Fatalf("GetPendingTrackerIntakeRecovery() pending=%t err=%v", pending, err)
			}
			if got.RuntimeTeardownHandleID != reservation.RuntimeTeardownHandleID ||
				got.RuntimeTeardownLaunchID != reservation.RuntimeTeardownLaunchID {
				t.Fatalf("retained teardown = %+v", got)
			}
			retry := sampleRecord("demo")
			retry.IssueID = record.IssueID
			if _, err := store.CreateTrackerIntakeSession(ctx, retry, limits); !errors.Is(err, ports.ErrTrackerIntakeActive) {
				t.Fatalf("CreateTrackerIntakeSession(retry) error = %v, want ErrTrackerIntakeActive", err)
			}
		})
	}
}

func TestTrackerIntakeConfirmedCleanupRetainsLegacyUnreleasedRuntime(t *testing.T) {
	for _, ownerState := range []string{"missing", "terminated"} {
		t.Run(ownerState, func(t *testing.T) {
			store := newTestStore(t)
			ctx := context.Background()
			seedProject(t, store, "demo")
			limits := domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2}

			record := sampleRecord("demo")
			record.Metadata = domain.SessionMetadata{}
			record.IssueID = "github:acme/demo#132"
			if ownerState == "terminated" {
				record.IssueID = "github:acme/demo#133"
			}
			created, err := store.CreateTrackerIntakeSession(ctx, record, limits)
			if err != nil {
				t.Fatal(err)
			}
			reservation := domain.TrackerIntakeRecoveryReservation{
				RuntimeLaunchID: "launch-132", RuntimeHandleIDs: []string{"runtime-132"},
				DeliveryPossible: true, CapacityReleased: true, ReleasedRuntimeLaunchID: "launch-132",
				CleanupConfirmed: true,
			}
			if err := store.ReserveTrackerIntakeRecovery(ctx, created.ID, reservation); err != nil {
				t.Fatalf("ReserveTrackerIntakeRecovery(): %v", err)
			}
			if ownerState == "missing" {
				if deleted, err := store.DeleteSession(ctx, created.ID); err != nil || !deleted {
					t.Fatalf("DeleteSession() = %t, %v", deleted, err)
				}
			} else {
				created.IsTerminated = true
				if err := store.UpdateSession(ctx, created); err != nil {
					t.Fatalf("UpdateSession(terminated): %v", err)
				}
			}

			if err := store.ReconcileTrackerIntakeSession(ctx, created.ID, time.Now()); err != nil {
				t.Fatalf("ReconcileTrackerIntakeSession(): %v", err)
			}
			got, pending, err := store.GetPendingTrackerIntakeRecovery(ctx, created.ID)
			if err != nil || !pending || got.RuntimeLaunchID != reservation.RuntimeLaunchID ||
				!reflect.DeepEqual(got.RuntimeHandleIDs, reservation.RuntimeHandleIDs) {
				t.Fatalf("legacy runtime fence pending=%t recovery=%+v err=%v", pending, got, err)
			}
			retry := sampleRecord("demo")
			retry.IssueID = record.IssueID
			if _, err := store.CreateTrackerIntakeSession(ctx, retry, limits); !errors.Is(err, ports.ErrTrackerIntakeActive) {
				t.Fatalf("CreateTrackerIntakeSession(retry) error = %v, want ErrTrackerIntakeActive", err)
			}
		})
	}
}

func TestTrackerIntakeConfirmedCleanupPromotesPossibleDelivery(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")
	limits := domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2}

	record := sampleRecord("demo")
	record.Metadata = domain.SessionMetadata{}
	record.IssueID = "github:acme/demo#14"
	created, err := store.CreateTrackerIntakeSession(ctx, record, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReserveTrackerIntakeRecovery(ctx, created.ID, domain.TrackerIntakeRecoveryReservation{
		RuntimeLaunchID: "launch-14", RuntimeHandleIDs: []string{"runtime-14"},
		DeliveryPossible: true, CapacityReleased: true, CleanupConfirmed: true,
		ReleasedRuntimeLaunchID: "launch-14", ReleasedRuntimeHandleIDs: []string{"runtime-14"},
	}); err != nil {
		t.Fatal(err)
	}
	if deleted, err := store.DeleteSession(ctx, created.ID); err != nil || !deleted {
		t.Fatalf("DeleteSession(seed) = %t, %v", deleted, err)
	}

	handledAt := time.Date(2026, 11, 18, 4, 0, 0, 0, time.UTC)
	if err := store.ReconcileTrackerIntakeSession(ctx, created.ID, handledAt); err != nil {
		t.Fatalf("ReconcileTrackerIntakeSession(): %v", err)
	}
	retry := sampleRecord("demo")
	retry.IssueID = record.IssueID
	if _, err := store.CreateTrackerIntakeSession(ctx, retry, limits); !errors.Is(err, ports.ErrTrackerIntakeHandled) {
		t.Fatalf("CreateTrackerIntakeSession(retry) error = %v, want ErrTrackerIntakeHandled", err)
	}
}

func TestTrackerIntakePendingMissingOwnerConsumesConcurrentCapacity(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")
	limits := domain.AutomatedSessionLimits{MaxConcurrent: 1, MaxStartsPerPass: 1}

	first := sampleRecord("demo")
	first.Metadata = domain.SessionMetadata{}
	first.IssueID = "github:acme/demo#1"
	created, err := store.CreateTrackerIntakeSession(ctx, first, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReserveTrackerIntakeRecovery(ctx, created.ID, domain.TrackerIntakeRecoveryReservation{
		RuntimeLaunchID: "launch-1", RuntimeHandleIDs: []string{"runtime-1"}, DeliveryPossible: true,
	}); err != nil {
		t.Fatal(err)
	}
	if deleted, err := store.DeleteSession(ctx, created.ID); err != nil || !deleted {
		t.Fatalf("DeleteSession() = %t, %v", deleted, err)
	}

	second := sampleRecord("demo")
	second.IssueID = "github:acme/demo#2"
	if _, err := store.CreateTrackerIntakeSession(ctx, second, limits); !errors.Is(err, ports.ErrAutomatedSessionCapacity) {
		t.Fatalf("CreateTrackerIntakeSession(second) error = %v, want unresolved owner to consume capacity", err)
	}
}

func TestTrackerIntakeConfirmedStoppedDeliveryFenceDoesNotConsumeConcurrentCapacity(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")
	limits := domain.AutomatedSessionLimits{MaxConcurrent: 1, MaxStartsPerPass: 1}

	first := sampleRecord("demo")
	first.ID = "demo-1"
	first.Metadata = domain.SessionMetadata{}
	first.IssueID = "github:acme/demo#1"
	created, err := store.CreateTrackerIntakeSession(ctx, first, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReserveTrackerIntakeRecovery(ctx, created.ID, domain.TrackerIntakeRecoveryReservation{
		RuntimeLaunchID: "launch-1", RuntimeHandleIDs: []string{"runtime-1"},
		DeliveryPossible: true, CapacityReleased: true,
	}); err != nil {
		t.Fatal(err)
	}
	second := sampleRecord("demo")
	second.ID = "demo-2"
	second.IssueID = "github:acme/demo#2"
	if _, err := store.CreateTrackerIntakeSession(ctx, second, limits); err != nil {
		t.Fatalf("CreateTrackerIntakeSession(second) error = %v, want released capacity", err)
	}

	retry := sampleRecord("demo")
	retry.ID = "demo-3"
	retry.IssueID = first.IssueID
	if _, err := store.CreateTrackerIntakeSession(ctx, retry, limits); !errors.Is(err, ports.ErrTrackerIntakeActive) {
		t.Fatalf("CreateTrackerIntakeSession(retry) error = %v, want delivery fence preserved", err)
	}
}

func TestListPendingTrackerIntakeClaimsIncludesMissingAndTerminatedOwners(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")
	limits := domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2}

	missing := sampleRecord("demo")
	missing.Metadata = domain.SessionMetadata{}
	missing.IssueID = "github:acme/demo#31"
	missing, err := store.CreateTrackerIntakeSession(ctx, missing, limits)
	if err != nil {
		t.Fatal(err)
	}
	missingRecovery := domain.TrackerIntakeRecoveryReservation{
		RuntimeLaunchID:  "launch-31",
		RuntimeHandleIDs: []string{" runtime-31 ", "runtime-31"},
		DeliveryPossible: true,
	}
	if err := store.ReserveTrackerIntakeRecovery(ctx, missing.ID, missingRecovery); err != nil {
		t.Fatal(err)
	}
	terminated := sampleRecord("demo")
	terminated.Metadata = domain.SessionMetadata{}
	terminated.IssueID = "github:acme/demo#32"
	terminated, err = store.CreateTrackerIntakeSession(ctx, terminated, limits)
	if err != nil {
		t.Fatal(err)
	}
	terminatedRecovery := domain.TrackerIntakeRecoveryReservation{
		WorkspacePath:     "/tmp/demo-32",
		WorkspaceRepoPath: "/tmp/demo-32/repo",
	}
	if err := store.ReserveTrackerIntakeRecovery(ctx, terminated.ID, terminatedRecovery); err != nil {
		t.Fatal(err)
	}
	if deleted, err := store.DeleteSession(ctx, missing.ID); err != nil || !deleted {
		t.Fatalf("DeleteSession(missing owner) = %t, %v", deleted, err)
	}
	terminated.IsTerminated = true
	if err := store.UpdateSession(ctx, terminated); err != nil {
		t.Fatal(err)
	}

	claims, err := store.ListPendingTrackerIntakeClaims(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bySession := make(map[domain.SessionID]domain.TrackerIntakePendingClaim, len(claims))
	for _, claim := range claims {
		bySession[claim.SessionID] = claim
	}
	if got := bySession[missing.ID]; got.ProjectID != "demo" || got.IssueID != missing.IssueID ||
		!got.Recovery.DeliveryPossible || fmt.Sprint(got.Recovery.RuntimeHandleIDs) != "[runtime-31]" {
		t.Fatalf("missing owner claim = %+v", got)
	}
	if got := bySession[terminated.ID]; got.ProjectID != "demo" || got.IssueID != terminated.IssueID ||
		got.Recovery.WorkspacePath != terminatedRecovery.WorkspacePath {
		t.Fatalf("terminated owner claim = %+v", got)
	}
}

func TestStoppedHandledTrackerIntakeClaimReleasesCapacityUntilRestored(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")
	limits := domain.AutomatedSessionLimits{MaxConcurrent: 1, MaxStartsPerPass: 1}

	first := sampleRecord("demo")
	first.IssueID = "github:acme/demo#320"
	created, err := store.CreateTrackerIntakeSession(ctx, first, limits)
	if err != nil {
		t.Fatal(err)
	}
	created.Activity.State = domain.ActivityExited
	if err := store.UpdateSession(ctx, created); err != nil {
		t.Fatal(err)
	}
	if err := store.ReserveTrackerIntakeRecovery(ctx, created.ID, domain.TrackerIntakeRecoveryReservation{
		DeliveryPossible: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitStoppedTrackerIntakeSession(ctx, created.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	handled, err := store.ListTrackerIntakeHandled(ctx, "demo")
	if err != nil || len(handled) != 1 || !handled[0].CapacityReleased {
		t.Fatalf("stopped handled claim = %+v, %v", handled, err)
	}

	second := sampleRecord("demo")
	second.IssueID = "github:acme/demo#321"
	second, err = store.CreateTrackerIntakeSession(ctx, second, limits)
	if err != nil {
		t.Fatalf("CreateTrackerIntakeSession(second) error = %v, want released handled capacity", err)
	}
	second.IsTerminated = true
	if err := store.UpdateSession(ctx, second); err != nil {
		t.Fatal(err)
	}
	created.Activity.State = domain.ActivityIdle
	created.Metadata.RuntimeLaunchID = "launch-restored"
	if err := store.UpdateSession(ctx, created); err != nil {
		t.Fatal(err)
	}
	third := sampleRecord("demo")
	third.IssueID = "github:acme/demo#322"
	if _, err := store.CreateTrackerIntakeSession(ctx, third, limits); !errors.Is(err, ports.ErrAutomatedSessionCapacity) {
		t.Fatalf("CreateTrackerIntakeSession(third) error = %v, want restored handled worker to consume capacity", err)
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
		Branch:                            "ao/demo-12/root",
		WorkspacePath:                     "/tmp/demo-12",
		WorkspaceRepoPath:                 "/tmp/demo-12",
		RuntimeLaunchID:                   "launch-12",
		RuntimeHandleIDs:                  []string{"ptyhost-v1:demo-1", "demo-1"},
		RuntimeTeardownHandleID:           " demo-1 ",
		RuntimeTeardownLaunchID:           " launch-12 ",
		RuntimeTeardownRestoreCapacity:    true,
		RuntimeTeardownReservationOnly:    true,
		RuntimeTeardownCompletedLaunchID:  " launch-11 ",
		RuntimeTeardownCompletedHandleIDs: []string{" old-demo-1 ", "old-demo-1"},
		CapacityReleased:                  true,
		ReleasedRuntimeLaunchID:           " launch-12 ",
		ReleasedRuntimeHandleIDs:          []string{" ptyhost-v1:demo-1 ", "ptyhost-v1:demo-1", "demo-1"},
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
	if got.ReleasedRuntimeLaunchID != "launch-12" ||
		fmt.Sprint(got.ReleasedRuntimeHandleIDs) != fmt.Sprint(reservation.RuntimeHandleIDs) {
		t.Fatalf("released runtime proof = %+v, want launch-12 and handles %v", got, reservation.RuntimeHandleIDs)
	}
	if got.RuntimeTeardownHandleID != "demo-1" || got.RuntimeTeardownLaunchID != "launch-12" {
		t.Fatalf("runtime teardown checkpoint = %q/%q, want demo-1/launch-12",
			got.RuntimeTeardownHandleID, got.RuntimeTeardownLaunchID)
	}
	if !got.RuntimeTeardownRestoreCapacity {
		t.Fatal("runtime teardown capacity restore bit was not preserved")
	}
	if !got.RuntimeTeardownReservationOnly {
		t.Fatal("runtime teardown reservation-only scope was not preserved")
	}
	if got.RuntimeTeardownCompletedLaunchID != "launch-11" ||
		!reflect.DeepEqual(got.RuntimeTeardownCompletedHandleIDs, []string{"old-demo-1"}) {
		t.Fatalf("completed runtime teardown proof = %q/%v",
			got.RuntimeTeardownCompletedLaunchID, got.RuntimeTeardownCompletedHandleIDs)
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
		{name: "github url case variant", manual: "https://github.com/Acme/Demo/issues/18", canonical: "github:acme/demo#18", wantMatch: true},
		{name: "gitlab url", manual: "https://gitlab.com/acme/demo/-/issues/18", canonical: "gitlab:acme/demo#18", wantMatch: true},
		{name: "self managed gitlab url", manual: "https://gitlab.one/acme/demo/-/issues/18", canonical: "gitlab:acme/demo#18@gitlab.one", wantMatch: true},
		{name: "numeric is scoped to project repository", manual: "18", canonical: "github:other/demo#18", wantMatch: false},
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

func TestCreateTrackerIntakeSessionRejectsLegacyHandledAliasAfterSessionTermination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedProject(t, store, "demo")
	limits := domain.AutomatedSessionLimits{MaxConcurrent: 3, MaxStartsPerPass: 2}

	legacy := sampleRecord("demo")
	legacy.IssueID = "https://github.com/Acme/Demo/issues/18"
	created, err := store.CreateSession(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InsertRawTrackerIntakeHandledForTest(ctx, "demo", string(legacy.IssueID), string(created.ID), time.Now()); err != nil {
		t.Fatal(err)
	}
	created.IsTerminated = true
	if err := store.UpdateSession(ctx, created); err != nil {
		t.Fatal(err)
	}

	retry := sampleRecord("demo")
	retry.IssueID = "github:acme/demo#18"
	if _, err := store.CreateTrackerIntakeSession(ctx, retry, limits); !errors.Is(err, ports.ErrTrackerIntakeHandled) {
		t.Fatalf("CreateTrackerIntakeSession(canonical retry) error = %v, want ErrTrackerIntakeHandled", err)
	}
	handled, err := store.ListTrackerIntakeHandled(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(handled) != 1 || handled[0].IssueID != retry.IssueID {
		t.Fatalf("handled records = %+v, want canonicalized durable identity %q", handled, retry.IssueID)
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
