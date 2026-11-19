package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var syncedSeq int64

// syncedAnswer is a person's answer written on peer machine h_b, as it
// arrives by sync: ordering assigned by h_b, stamped after everything local.
func syncedAnswer(callID string, option int) domain.DecisionEntry {
	syncedSeq++
	payload, _ := json.Marshal(domain.DecisionAnsweredPayload{Choice: domain.DecisionChoiceOption, Option: option})
	return domain.DecisionEntry{
		ID: fmt.Sprintf("dent_peer_%d", syncedSeq), Origin: "h_b", OriginSeq: syncedSeq,
		Stamp:  domain.DecisionStamp{WallMS: 9_000_000 + syncedSeq},
		CallID: callID, Kind: domain.DecisionKindAnswered, Payload: payload,
		WrittenAt: time.UnixMilli(9_000_000).UTC(), AuthorKind: domain.DecisionAuthorPerson, AuthorSurface: domain.RequestSurfaceDesktop,
	}
}

func planState(t *testing.T, fixture decisionServiceFixture, session domain.SessionID) domain.PlanFirstState {
	t.Helper()
	record, ok, err := fixture.store.GetSession(context.Background(), session)
	if err != nil || !ok {
		t.Fatalf("get session: %v %v", ok, err)
	}
	return record.PlanFirstState
}

func TestSyncedPlanDeclineWaitsForBlockedApprovalDelivery(t *testing.T) {
	syncedSeq = 0
	fixture := newDecisionService(t)
	if updated, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.loose, domain.PlanFirstPending); err != nil || !updated {
		t.Fatalf("set plan-first state: %v %v", updated, err)
	}
	approval := answeredPlanFirstCall(t, fixture, fixture.loose, 1, "")
	messenger := &blockingDecisionMessenger{
		fakeDecisionMessenger: newFakeDecisionMessenger(), block: fixture.loose,
		started: make(chan struct{}, 1), release: make(chan struct{}),
	}
	deliverer := newTestDeliverer(fixture.store, messenger, "h_a")
	deliverer.mutationGate = fixture.service.mutationGate
	delivered := make(chan error, 1)
	go func() {
		_, err := deliverer.RunDue(context.Background())
		delivered <- err
	}()
	<-messenger.started

	applied := make(chan error, 1)
	go func() {
		_, err := fixture.service.ApplySyncedEntries(context.Background(), "h_b", []domain.DecisionEntry{syncedAnswer(approval.ID, 3)}, time.UnixMilli(9_000_000))
		applied <- err
	}()
	select {
	case err := <-applied:
		t.Fatalf("the synced decline was stored while the approval delivery held the gate (err %v)", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(messenger.release)
	if err := <-delivered; err != nil {
		t.Fatal(err)
	}
	if err := <-applied; err != nil {
		t.Fatal(err)
	}
	if got := planState(t, fixture, fixture.loose); got != domain.PlanFirstDeclined {
		t.Fatalf("plan-first state = %q, want declined: the earlier approval delivery must not reopen the gate", got)
	}
	call := getDecisionCall(t, fixture, approval.ID)
	if call.Option != 3 {
		t.Fatalf("standing option = %d, want the synced decline", call.Option)
	}
}

func TestSyncedPlanDeclineClosesAnApprovedGate(t *testing.T) {
	syncedSeq = 0
	fixture := newDecisionService(t)
	if _, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.loose, domain.PlanFirstPending); err != nil {
		t.Fatal(err)
	}
	approval := answeredPlanFirstCall(t, fixture, fixture.loose, 1, "")
	if _, err := newTestDeliverer(fixture.store, newFakeDecisionMessenger(), "h_a").RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := planState(t, fixture, fixture.loose); got != domain.PlanFirstApproved {
		t.Fatalf("setup: plan-first state = %q, want approved", got)
	}
	decline := syncedAnswer(approval.ID, 3)
	if _, err := fixture.service.ApplySyncedEntries(context.Background(), "h_b", []domain.DecisionEntry{decline}, time.UnixMilli(9_000_000)); err != nil {
		t.Fatal(err)
	}
	if got := planState(t, fixture, fixture.loose); got != domain.PlanFirstDeclined {
		t.Fatalf("plan-first state after a synced decline = %q, want declined before any delivery", got)
	}
	// Receiving the same page again changes nothing.
	if _, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.loose, domain.PlanFirstApproved); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ApplySyncedEntries(context.Background(), "h_b", []domain.DecisionEntry{decline}, time.UnixMilli(9_000_000)); err != nil {
		t.Fatal(err)
	}
	if got := planState(t, fixture, fixture.loose); got != domain.PlanFirstApproved {
		t.Fatalf("a duplicate synced answer moved the gate to %q", got)
	}
}

func TestSyncedAnswerToAnotherMachinesPlanCallLeavesLocalSessionAlone(t *testing.T) {
	syncedSeq = 0
	fixture := newDecisionService(t)
	if _, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.loose, domain.PlanFirstPending); err != nil {
		t.Fatal(err)
	}
	// h_b's own plan call, owned by h_b's session that happens to share the
	// local session's id. Its payload is borrowed from a local plan call.
	local := raisePlanFirstCall(t, fixture, fixture.loose)
	if _, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.loose, domain.PlanFirstApproved); err != nil {
		t.Fatal(err)
	}
	entries, err := fixture.store.ListDecisionEntries(context.Background(), local.ID)
	if err != nil {
		t.Fatal(err)
	}
	raised := entries[0]
	raised.ID, raised.Origin, raised.OriginSeq, raised.CallID = "dent_remote_raised", "h_b", 1, "dec_remote_plan"
	raised.Stamp = domain.DecisionStamp{WallMS: 8_000_000}
	syncedSeq = 1
	answer := syncedAnswer("dec_remote_plan", 3)
	if _, err := fixture.service.ApplySyncedEntries(context.Background(), "h_b", []domain.DecisionEntry{raised, answer}, time.UnixMilli(9_000_000)); err != nil {
		t.Fatal(err)
	}
	if got := planState(t, fixture, fixture.loose); got != domain.PlanFirstApproved {
		t.Fatalf("another machine's plan answer moved this machine's session to %q", got)
	}
}
