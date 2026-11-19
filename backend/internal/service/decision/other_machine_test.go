package decision

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// remoteCall stores a call raised on another machine (h_b, "laptop") by its
// session "w-1", which shares an id with a session on this machine.
func remoteCall(t *testing.T, fixture decisionServiceFixture, callID string, kind domain.DecisionEntryKind, payload any) domain.DecisionCall {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	vector, err := fixture.store.DecisionVector(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entry := domain.DecisionEntry{
		ID: "dent_" + callID, Origin: "h_b", OriginSeq: vector["h_b"] + 1, Stamp: domain.DecisionStamp{WallMS: 900_000},
		CallID: callID, Kind: kind, Payload: raw, WrittenAt: time.UnixMilli(900_000).UTC(),
		AuthorKind: domain.DecisionAuthorSession, AuthorSession: fixture.w1,
	}
	res, err := fixture.service.ApplySyncedEntries(context.Background(), "h_b", []domain.DecisionEntry{entry}, time.UnixMilli(900_000))
	if err != nil || len(res.Calls) != 1 {
		t.Fatalf("store remote call: %+v, %v", res, err)
	}
	return res.Calls[0]
}

func remoteRaised(t *testing.T, fixture decisionServiceFixture) domain.DecisionCall {
	payload := domain.DecisionRaisedPayload{
		Title: "Which cache?", Context: "Remote call.", MachineLabel: "laptop",
		Options:        []domain.DecisionOption{{Label: "A", Consequence: "a", Recommended: true}, {Label: "B", Consequence: "b"}},
		OwnerSessionID: fixture.w1, OwnerSessionName: "w-1",
	}
	return remoteCall(t, fixture, "dec_remote", domain.DecisionKindRaised, payload)
}

func remoteDecided(t *testing.T, fixture decisionServiceFixture) domain.DecisionCall {
	payload := domain.DecisionDecidedPayload{
		What: "Use the queue", Why: "It exists", Undo: "Revert", Kind: "design",
		MachineLabel: "laptop", OwnerSessionID: fixture.w1, OwnerSessionName: "w-1",
	}
	return remoteCall(t, fixture, "dec_remote_decided", domain.DecisionKindDecided, payload)
}

func assertOtherMachine(t *testing.T, err error) {
	t.Helper()
	assertAPICode(t, err, "DECISION_OTHER_MACHINE")
	var apiErr *apierr.Error
	_ = errors.As(err, &apiErr)
	if !strings.Contains(apiErr.Message, "This call belongs to a session on laptop. Only that session can do this.") {
		t.Fatalf("message = %q", apiErr.Message)
	}
}

func TestWithdrawOnAnotherMachinesCallNamesTheMachine(t *testing.T) {
	fixture := newDecisionService(t)
	call := remoteRaised(t, fixture)
	_, err := fixture.service.Withdraw(context.Background(), Actor{Session: fixture.w1}, call.ID)
	assertOtherMachine(t, err)
}

func TestOutcomeBySessionOnAnotherMachinesCallNamesTheMachine(t *testing.T) {
	fixture := newDecisionService(t)
	call := remoteRaised(t, fixture)
	answered, err := fixture.service.Answer(context.Background(), Actor{Surface: domain.RequestSurfaceDesktop}, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.RecordOutcome(context.Background(), Actor{Session: fixture.w1}, call.ID, OutcomeInput{ForEntry: answered.StandingEntryID, Text: "done"})
	assertOtherMachine(t, err)

	decided := remoteDecided(t, fixture)
	_, err = fixture.service.RecordOutcome(context.Background(), Actor{Session: fixture.w1}, decided.ID, OutcomeInput{Text: "not done"})
	assertOtherMachine(t, err)
}

func TestCarriedOutOnAnotherMachinesCallNamesTheMachine(t *testing.T) {
	fixture := newDecisionService(t)
	decided := remoteDecided(t, fixture)
	_, err := fixture.service.CarriedOut(context.Background(), Actor{Session: fixture.w1}, decided.ID)
	assertOtherMachine(t, err)
}

func TestSameMachineNonOwnerStillGetsNotOwner(t *testing.T) {
	fixture := newDecisionService(t)
	call, err := fixture.service.Raise(context.Background(), Actor{Session: fixture.w1}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.Withdraw(context.Background(), Actor{Session: fixture.w2}, call.ID)
	assertAPICode(t, err, "DECISION_NOT_OWNER")
}

func TestPersonMayAnswerAndOverruleAnotherMachinesCall(t *testing.T) {
	fixture := newDecisionService(t)
	call := remoteRaised(t, fixture)
	if _, err := fixture.service.Answer(context.Background(), Actor{Surface: domain.RequestSurfaceDesktop}, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 2}); err != nil {
		t.Fatalf("answer another machine's call: %v", err)
	}
	decided := remoteDecided(t, fixture)
	overruled, err := fixture.service.Overrule(context.Background(), Actor{Surface: domain.RequestSurfaceDesktop}, decided.ID, "Do the other thing")
	if err != nil {
		t.Fatalf("overrule another machine's decision: %v", err)
	}
	if overruled.Origin != "h_b" || overruled.Delivery != domain.DecisionDeliveryPending {
		t.Fatalf("overruled = origin %s delivery %s, want h_b and pending until the owner delivers", overruled.Origin, overruled.Delivery)
	}
}
