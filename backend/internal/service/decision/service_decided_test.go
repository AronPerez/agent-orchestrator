package decision

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func validDecideInput() DecideInput {
	return DecideInput{
		Kind: "pr_hygiene", What: "Closed the duplicate pull request #12",
		Why: "It repeats #11, which is further along.", Undo: "Reopen #12; its branch is kept.",
		Verify: "One open PR remains for the ticket.",
	}
}

func assertAPICode(t *testing.T, err error, code string) {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

func TestDecideRecordsADecisionMadeForThePerson(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyOn})
	call, err := fixture.service.Decide(context.Background(), Actor{Session: fixture.w1}, validDecideInput())
	if err != nil {
		t.Fatal(err)
	}
	if call.State != domain.DecisionStateDecided || call.Decided == nil || call.Decided.Undo != "Reopen #12; its branch is kept." {
		t.Fatalf("call = %+v", call)
	}
	if call.Raised.ProjectID != "shop" || call.Raised.StreamSessionID != fixture.epic || call.Raised.OwnerSessionID != fixture.w1 ||
		call.Raised.AutonomyMode != domain.AutonomyOn || call.Raised.Title != "Closed the duplicate pull request #12" {
		t.Fatalf("origin = %+v", call.Raised)
	}
	entries := *fixture.appended
	if last := entries[len(entries)-1]; last.Kind != domain.DecisionKindDecided || last.AuthorSession != fixture.w1 {
		t.Fatalf("appended = %+v", last)
	}
	decided, err := fixture.service.List(context.Background(), Actor{Surface: domain.RequestSurfaceDesktop}, ScopeDecided)
	if err != nil || len(decided) != 1 || decided[0].ID != call.ID {
		t.Fatalf("decided list = %+v, %v", decided, err)
	}
}

// A session in mode on acts first and records afterwards. If the project's
// policy changed while it was running, the record must still be kept, so the
// person can see and overrule an action that already happened. It is flagged
// with the reason instead of refused.
func TestDecideKeepsTheRecordWhenThePolicyChangedUnderTheSession(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config domain.AutonomyConfig
		kind   string
		want   string
	}{
		{"mode switched off", domain.AutonomyConfig{}, "pr_hygiene", domain.DecisionOutsidePolicyNotOn},
		{"mode switched to observe", domain.AutonomyConfig{Mode: domain.AutonomyObserve}, "pr_hygiene", domain.DecisionOutsidePolicyNotOn},
		{"kind reserved since", domain.AutonomyConfig{Mode: domain.AutonomyOn, Reserved: []domain.DecisionKindID{"pr_hygiene"}}, "pr_hygiene", domain.DecisionOutsidePolicyReserved},
		{"catch-all kind", domain.AutonomyConfig{Mode: domain.AutonomyOn}, "other", domain.DecisionOutsidePolicyReserved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newDecisionService(t)
			setShopAutonomy(t, fixture, tc.config)
			input := validDecideInput()
			input.Kind = tc.kind
			call, err := fixture.service.Decide(context.Background(), Actor{Session: fixture.w1}, input)
			if err != nil {
				t.Fatal(err)
			}
			if call.State != domain.DecisionStateDecided || call.Decided == nil || call.Decided.OutsidePolicy != tc.want {
				t.Fatalf("call = %+v decided = %+v", call, call.Decided)
			}
		})
	}
}

func TestDecideWithinPolicyIsNotFlagged(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyOn})
	call, err := fixture.service.Decide(context.Background(), Actor{Session: fixture.w1}, validDecideInput())
	if err != nil {
		t.Fatal(err)
	}
	if call.Decided.OutsidePolicy != "" {
		t.Fatalf("outsidePolicy = %q", call.Decided.OutsidePolicy)
	}
}

func TestDecideStillRefusesAMalformedRecord(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyOn})
	ctx := context.Background()
	input := validDecideInput()
	input.Undo = " "
	_, err := fixture.service.Decide(ctx, Actor{Session: fixture.w1}, input)
	assertRaiseRule(t, err, "undo_required")
	_, err = fixture.service.Decide(ctx, Actor{Surface: domain.RequestSurfaceCLI}, validDecideInput())
	assertAPICode(t, err, "DECISION_SESSION_REQUIRED")
}

func TestOverruleGoesBackToTheOwningSession(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyOn})
	ctx := context.Background()
	call, err := fixture.service.Decide(ctx, Actor{Session: fixture.w1}, validDecideInput())
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.Overrule(ctx, Actor{Session: fixture.w2}, call.ID, "No.")
	assertAPICode(t, err, "DECISION_PERSON_ONLY")
	_, err = fixture.service.Overrule(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, call.ID, " ")
	assertRaiseRule(t, err, "overrule_note_required")

	overruled, err := fixture.service.Overrule(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, call.ID, "Keep both open.")
	if err != nil {
		t.Fatal(err)
	}
	if overruled.State != domain.DecisionStateInProgress || overruled.StandingKind != domain.DecisionKindOverruled ||
		overruled.Note != "Keep both open." || overruled.Delivery != domain.DecisionDeliveryPending || overruled.AnswerSurface != domain.RequestSurfaceDesktop {
		t.Fatalf("overruled = %+v", overruled)
	}
	_, err = fixture.service.Overrule(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, call.ID, "Again.")
	assertAPICode(t, err, "DECISION_WRONG_STATE")

	done, err := fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w1}, call.ID, OutcomeInput{ForEntry: overruled.StandingEntryID, Text: "Reopened #12."})
	if err != nil {
		t.Fatal(err)
	}
	if done.State != domain.DecisionStateDone {
		t.Fatalf("done = %+v", done)
	}
}

func TestOverruleOnlyAppliesToDecidedCallsAndAnswerNotToThem(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyOn})
	ctx := context.Background()
	person := Actor{Surface: domain.RequestSurfaceDesktop}
	question, err := fixture.service.Raise(ctx, Actor{Session: fixture.w1}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.Overrule(ctx, person, question.ID, "No.")
	assertAPICode(t, err, "DECISION_NOT_DECIDED")

	decided, err := fixture.service.Decide(ctx, Actor{Session: fixture.w1}, validDecideInput())
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.Answer(ctx, person, decided.ID, AnswerInput{Choice: domain.DecisionChoiceOther, Note: "n"})
	assertAPICode(t, err, "DECISION_NOT_A_QUESTION")
}

// After a restart or compaction a session runs `ao decision ls` to find what
// it owes, so its decided calls must be in its own list, overruled or not.
func TestADecidedCallIsInItsSessionsOwnList(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyOn})
	ctx := context.Background()
	call, err := fixture.service.Decide(ctx, Actor{Session: fixture.w1}, validDecideInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Overrule(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, call.ID, "Keep both open."); err != nil {
		t.Fatal(err)
	}
	mine, err := fixture.service.List(ctx, Actor{Session: fixture.w1}, ScopeMine)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0].ID != call.ID || mine[0].State != domain.DecisionStateInProgress || mine[0].Raised.StreamName != "Checkout epic" {
		t.Fatalf("mine = %+v", mine)
	}
}

// A session records the decision before acting, so if it then does not
// carry it out, it closes the record with an outcome saying so.
func TestTheOwnerCanCloseADecisionItDidNotCarryOut(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyOn})
	ctx := context.Background()
	call, err := fixture.service.Decide(ctx, Actor{Session: fixture.w1}, validDecideInput())
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w2}, call.ID, OutcomeInput{Text: "Not carried out."})
	assertAPICode(t, err, "DECISION_NOT_OWNER")
	done, err := fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w1}, call.ID, OutcomeInput{Text: "Not carried out: #12 was merged first."})
	if err != nil {
		t.Fatal(err)
	}
	if done.State != domain.DecisionStateDone || done.Outcome != "Not carried out: #12 was merged first." {
		t.Fatalf("done = %+v", done)
	}
}

func TestTheOwnerConfirmsADecisionWasCarriedOut(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyOn})
	ctx := context.Background()
	call, err := fixture.service.Decide(ctx, Actor{Session: fixture.w1}, validDecideInput())
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.CarriedOut(ctx, Actor{Session: fixture.w2}, call.ID)
	assertAPICode(t, err, "DECISION_NOT_OWNER")
	_, err = fixture.service.CarriedOut(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, call.ID)
	assertAPICode(t, err, "DECISION_NOT_OWNER")
	done, err := fixture.service.CarriedOut(ctx, Actor{Session: fixture.w1}, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !done.Decided.CarriedOut || done.State != domain.DecisionStateDecided {
		t.Fatalf("done = %+v", done.Decided)
	}
	before := len(*fixture.appended)
	if _, err := fixture.service.CarriedOut(ctx, Actor{Session: fixture.w1}, call.ID); err != nil {
		t.Fatal(err)
	}
	if len(*fixture.appended) != before {
		t.Fatal("a repeated confirmation must not append another entry")
	}
	question, err := fixture.service.Raise(ctx, Actor{Session: fixture.w1}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.CarriedOut(ctx, Actor{Session: fixture.w1}, question.ID)
	assertAPICode(t, err, "DECISION_NOT_DECIDED")
}
