package decision

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// These tests walk the record → act → confirm → overrule lifecycle of a
// decision made on the person's behalf, one row of the state table in the
// pull request each.

func onModeFixture(t *testing.T) (decisionServiceFixture, context.Context) {
	t.Helper()
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyOn})
	return fixture, context.Background()
}

func decide(t *testing.T, fixture decisionServiceFixture, owner domain.SessionID) domain.DecisionCall {
	t.Helper()
	call, err := fixture.service.Decide(context.Background(), Actor{Session: owner}, validDecideInput())
	if err != nil {
		t.Fatal(err)
	}
	return call
}

var person = Actor{Surface: domain.RequestSurfaceDesktop}

// Row: the session stops between recording and acting (or between acting
// and confirming). The record stands unconfirmed and its session finds it
// again with `ao decision ls`.
func TestLifecycleCrashBeforeConfirmLeavesARecoverableRecord(t *testing.T) {
	fixture, ctx := onModeFixture(t)
	call := decide(t, fixture, fixture.w1)
	mine, err := fixture.service.List(ctx, Actor{Session: fixture.w1}, ScopeMine)
	if err != nil || len(mine) != 1 || mine[0].ID != call.ID || mine[0].Decided.CarriedOut {
		t.Fatalf("mine = %+v, %v", mine, err)
	}
	decided, err := fixture.service.List(ctx, person, ScopeDecided)
	if err != nil || len(decided) != 1 || decided[0].Decided.CarriedOut {
		t.Fatalf("decided = %+v, %v", decided, err)
	}
}

// Row: retrying `ao decision decide` after a lost response must not create
// a second record of the same action.
func TestLifecycleRetriedDecideReturnsTheSameRecord(t *testing.T) {
	fixture, ctx := onModeFixture(t)
	first := decide(t, fixture, fixture.w1)
	second := decide(t, fixture, fixture.w1)
	if second.ID != first.ID {
		t.Fatalf("retry created a second record: %s and %s", first.ID, second.ID)
	}
	other := validDecideInput()
	other.What = "Closed the duplicate pull request #13"
	third, err := fixture.service.Decide(ctx, Actor{Session: fixture.w1}, other)
	if err != nil || third.ID == first.ID {
		t.Fatalf("a different decision must get its own record: %+v, %v", third, err)
	}
	fromOtherSession := decide(t, fixture, fixture.w2)
	if fromOtherSession.ID == first.ID {
		t.Fatal("another session's identical decision is a separate record")
	}
	if _, err := fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w1}, first.ID, OutcomeInput{Text: "Not carried out."}); err != nil {
		t.Fatal(err)
	}
	if again := decide(t, fixture, fixture.w1); again.ID == first.ID {
		t.Fatal("a closed record must not absorb a new decision")
	}
}

// Row: the session acted and confirmed. It may not then close the record
// as not carried out; the decision stays for the person to review or
// overrule. The person may still close it.
func TestLifecycleConfirmedDecisionStaysOpenForThePerson(t *testing.T) {
	fixture, ctx := onModeFixture(t)
	call := decide(t, fixture, fixture.w1)
	if _, err := fixture.service.CarriedOut(ctx, Actor{Session: fixture.w1}, call.ID); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w1}, call.ID, OutcomeInput{Text: "Not carried out."})
	assertAPICode(t, err, "DECISION_CARRIED_OUT")
	done, err := fixture.service.RecordOutcome(ctx, person, call.ID, OutcomeInput{Text: "Reviewed; fine."})
	if err != nil || done.State != domain.DecisionStateDone {
		t.Fatalf("person close = %+v, %v", done, err)
	}
	_, err = fixture.service.CarriedOut(ctx, Actor{Session: fixture.w1}, call.ID)
	assertAPICode(t, err, "DECISION_WRONG_STATE")
}

// Row: overrule after the action. The owner confirms (idempotently),
// undoes, and records the outcome against the overrule.
func TestLifecycleOverruleAfterActThenOutcome(t *testing.T) {
	fixture, ctx := onModeFixture(t)
	call := decide(t, fixture, fixture.w1)
	if _, err := fixture.service.CarriedOut(ctx, Actor{Session: fixture.w1}, call.ID); err != nil {
		t.Fatal(err)
	}
	overruled, err := fixture.service.Overrule(ctx, person, call.ID, "Reopen it.")
	if err != nil || !overruled.Decided.CarriedOut {
		t.Fatalf("overruled = %+v, %v", overruled, err)
	}
	if _, err := fixture.service.CarriedOut(ctx, Actor{Session: fixture.w1}, call.ID); err != nil {
		t.Fatal(err)
	}
	done, err := fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w1}, call.ID, OutcomeInput{ForEntry: overruled.StandingEntryID, Text: "Reopened #12."})
	if err != nil || done.State != domain.DecisionStateDone {
		t.Fatalf("done = %+v, %v", done, err)
	}
}

// Row: overrule before the session confirmed. The session can still record
// that it had acted, so the undo has something to undo.
func TestLifecycleOverruleBeforeConfirmThenConfirm(t *testing.T) {
	fixture, ctx := onModeFixture(t)
	call := decide(t, fixture, fixture.w1)
	overruled, err := fixture.service.Overrule(ctx, person, call.ID, "Reopen it.")
	if err != nil || overruled.Decided.CarriedOut {
		t.Fatalf("overruled = %+v, %v", overruled, err)
	}
	confirmed, err := fixture.service.CarriedOut(ctx, Actor{Session: fixture.w1}, call.ID)
	if err != nil || !confirmed.Decided.CarriedOut || confirmed.State != domain.DecisionStateInProgress {
		t.Fatalf("confirmed = %+v, %v", confirmed, err)
	}
}

// Row: the policy changed under a running session that had already acted.
// The record is kept outside policy and can still be confirmed and overruled.
func TestLifecycleOutsidePolicyRecordCanBeConfirmedAndOverruled(t *testing.T) {
	fixture, ctx := onModeFixture(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{})
	call := decide(t, fixture, fixture.w1)
	if call.Decided.OutsidePolicy != domain.DecisionOutsidePolicyNotOn {
		t.Fatalf("decided = %+v", call.Decided)
	}
	if _, err := fixture.service.CarriedOut(ctx, Actor{Session: fixture.w1}, call.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Overrule(ctx, person, call.ID, "Undo it."); err != nil {
		t.Fatal(err)
	}
}

// Row: actions that belong to questions are refused on a decided call.
func TestLifecycleQuestionActionsAreRefusedOnADecidedCall(t *testing.T) {
	fixture, ctx := onModeFixture(t)
	call := decide(t, fixture, fixture.w1)
	_, err := fixture.service.Dismiss(ctx, person, call.ID, "")
	assertAPICode(t, err, "DECISION_WRONG_STATE")
	_, err = fixture.service.Withdraw(ctx, Actor{Session: fixture.w1}, call.ID)
	assertAPICode(t, err, "DECISION_WRONG_STATE")
	_, err = fixture.service.Answer(ctx, person, call.ID, AnswerInput{Choice: domain.DecisionChoiceOther, Note: "n"})
	assertAPICode(t, err, "DECISION_NOT_A_QUESTION")
}

// Row: a retry arrives after the policy changed. The existing record is
// returned (no second record), carrying the current policy's flag so the
// session is told not to act.
func TestLifecycleRetryAfterPolicyChangeIsFlagged(t *testing.T) {
	fixture, ctx := onModeFixture(t)
	first := decide(t, fixture, fixture.w1)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{})
	retry := decide(t, fixture, fixture.w1)
	if retry.ID != first.ID {
		t.Fatalf("retry created a second record: %s and %s", first.ID, retry.ID)
	}
	if retry.Decided.OutsidePolicy != domain.DecisionOutsidePolicyNotOn {
		t.Fatalf("retry not flagged: %+v", retry.Decided)
	}
	stored, err := fixture.service.Get(ctx, first.ID)
	if err != nil || stored.Call.Decided.OutsidePolicy != "" {
		t.Fatalf("the stored record was within policy when written and stays so: %+v, %v", stored.Call.Decided, err)
	}
}
