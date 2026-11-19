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

func TestDecideIsRefusedOutsideModeOn(t *testing.T) {
	for _, mode := range []domain.AutonomyMode{"", domain.AutonomyObserve} {
		t.Run(string(mode), func(t *testing.T) {
			fixture := newDecisionService(t)
			setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: mode})
			_, err := fixture.service.Decide(context.Background(), Actor{Session: fixture.w1}, validDecideInput())
			assertRaiseRule(t, err, "autonomy_not_on")
		})
	}
	fixture := newDecisionService(t)
	_, err := fixture.service.Decide(context.Background(), Actor{Session: fixture.loose}, validDecideInput())
	assertRaiseRule(t, err, "autonomy_not_on")
}

func TestDecideRefusesReservedKindsAndBadInput(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyOn})
	ctx := context.Background()
	for _, kind := range []string{"production", "other"} {
		input := validDecideInput()
		input.Kind = kind
		_, err := fixture.service.Decide(ctx, Actor{Session: fixture.w1}, input)
		assertRaiseRule(t, err, "kind_reserved")
	}
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
