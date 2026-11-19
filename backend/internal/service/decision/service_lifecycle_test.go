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

func decideWithKey(t *testing.T, fixture decisionServiceFixture, owner domain.SessionID, key string) domain.DecisionCall {
	t.Helper()
	input := validDecideInput()
	input.Key = key
	call, err := fixture.service.Decide(context.Background(), Actor{Session: owner}, input)
	if err != nil {
		t.Fatal(err)
	}
	return call
}

// Row: retrying `ao decision decide` with the same key after a lost
// response returns the existing record; another key, another session, or no
// key at all never merges distinct operations.
func TestLifecycleRetryWithTheSameKeyReturnsTheSameRecord(t *testing.T) {
	fixture, _ := onModeFixture(t)
	first := decideWithKey(t, fixture, fixture.w1, "close-pr-12")
	if retry := decideWithKey(t, fixture, fixture.w1, "close-pr-12"); retry.ID != first.ID {
		t.Fatalf("retry created a second record: %s and %s", first.ID, retry.ID)
	}
	if other := decideWithKey(t, fixture, fixture.w1, "close-pr-12-again"); other.ID == first.ID {
		t.Fatal("a different key is a different operation")
	}
	if fromOtherSession := decideWithKey(t, fixture, fixture.w2, "close-pr-12"); fromOtherSession.ID == first.ID {
		t.Fatal("keys belong to one session")
	}
	a, b := decide(t, fixture, fixture.w1), decide(t, fixture, fixture.w1)
	if a.ID == b.ID {
		t.Fatal("without a key, identical text must not merge two operations")
	}
}

// Row: the person overruled the record before the retry arrived. The retry
// returns the overruled call, so the session is not told to act.
func TestLifecycleRetryAfterOverruleReturnsTheOverruledCall(t *testing.T) {
	fixture, ctx := onModeFixture(t)
	first := decideWithKey(t, fixture, fixture.w1, "close-pr-12")
	if _, err := fixture.service.Overrule(ctx, person, first.ID, "Keep it open."); err != nil {
		t.Fatal(err)
	}
	retry := decideWithKey(t, fixture, fixture.w1, "close-pr-12")
	if retry.ID != first.ID || retry.State != domain.DecisionStateInProgress || retry.StandingKind != domain.DecisionKindOverruled || retry.Note != "Keep it open." {
		t.Fatalf("retry = %+v", retry)
	}
	decided, err := fixture.service.List(ctx, person, ScopeDecided)
	if err != nil || len(decided) != 0 {
		t.Fatalf("the retry must not add a new standing decision: %+v, %v", decided, err)
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
	first := decideWithKey(t, fixture, fixture.w1, "close-pr-12")
	setShopAutonomy(t, fixture, domain.AutonomyConfig{})
	retry := decideWithKey(t, fixture, fixture.w1, "close-pr-12")
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

// Row: the person closed an unconfirmed decision while its session was
// acting. The session's late confirmation reopens it as carried out and
// marks it changed after close, so it stays visible and overrulable.
func TestLifecycleLateConfirmationReopensADecisionClosedUnconfirmed(t *testing.T) {
	fixture, ctx := onModeFixture(t)
	call := decide(t, fixture, fixture.w1)
	if _, err := fixture.service.RecordOutcome(ctx, person, call.ID, OutcomeInput{Text: "Not carried out."}); err != nil {
		t.Fatal(err)
	}
	reopened, err := fixture.service.CarriedOut(ctx, Actor{Session: fixture.w1}, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.State != domain.DecisionStateDecided || !reopened.Decided.CarriedOut || !reopened.ChangedAfterClose {
		t.Fatalf("reopened = %+v decided = %+v", reopened, reopened.Decided)
	}
	if _, err := fixture.service.Overrule(ctx, person, call.ID, "Undo it."); err != nil {
		t.Fatalf("a reopened decision must be overrulable: %v", err)
	}
}

// Row: once an overrule's undo is recorded, a late confirmation is refused.
func TestLifecycleConfirmationAfterUndoIsRefused(t *testing.T) {
	fixture, ctx := onModeFixture(t)
	call := decide(t, fixture, fixture.w1)
	overruled, err := fixture.service.Overrule(ctx, person, call.ID, "Undo it.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w1}, call.ID, OutcomeInput{ForEntry: overruled.StandingEntryID, Text: "Nothing to undo."}); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.CarriedOut(ctx, Actor{Session: fixture.w1}, call.ID)
	assertAPICode(t, err, "DECISION_WRONG_STATE")
}
