package decision

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestObserveCountsAgreementPerKind(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	person := Actor{Surface: domain.RequestSurfaceDesktop}
	raise := func(input RaiseInput) domain.DecisionCall {
		t.Helper()
		call, err := fixture.service.Raise(ctx, Actor{Session: fixture.w1}, input)
		if err != nil {
			t.Fatal(err)
		}
		return call
	}
	answer := func(call domain.DecisionCall, input AnswerInput) {
		t.Helper()
		if _, err := fixture.service.Answer(ctx, person, call.ID, input); err != nil {
			t.Fatal(err)
		}
	}

	// Raised in off mode: never counted, even with a kind.
	offMode := validRaiseInput()
	offMode.Kind = "pr_hygiene"
	answer(raise(offMode), AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1})

	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyObserve})
	answer(raise(predictedRaise("pr_hygiene")), AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1})
	answer(raise(predictedRaise("pr_hygiene")), AnswerInput{Choice: domain.DecisionChoiceOption, Option: 2})
	answer(raise(predictedRaise("pr_hygiene")), AnswerInput{Choice: domain.DecisionChoiceOther, Note: "Do neither."})
	raise(predictedRaise("pr_hygiene")) // still needs the person
	dismissed := raise(predictedRaise("ticket_hygiene"))
	if _, err := fixture.service.Dismiss(ctx, person, dismissed.ID, ""); err != nil {
		t.Fatal(err)
	}
	withdrawn := raise(predictedRaise("ticket_hygiene"))
	if _, err := fixture.service.Withdraw(ctx, Actor{Session: fixture.w1}, withdrawn.ID); err != nil {
		t.Fatal(err)
	}
	reserved := validRaiseInput()
	reserved.Kind = "design"
	answer(raise(reserved), AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1})

	report, err := fixture.service.Observe(ctx, person, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if report.ProjectID != "shop" || report.Mode != domain.AutonomyObserve {
		t.Fatalf("report = %+v", report)
	}
	want := []ObserveRow{
		{Kind: "design", Label: "Design and product behaviour", Reserved: true, Calls: 1},
		{Kind: "ticket_hygiene", Label: "Ticket hygiene", Calls: 1},
		{Kind: "pr_hygiene", Label: "Pull request hygiene", Calls: 4, Answered: 3, Agreed: 1, Differed: 2},
	}
	got := map[domain.DecisionKindID]ObserveRow{}
	for _, row := range report.Rows {
		got[row.Kind] = row
	}
	if len(report.Rows) != len(want) {
		t.Fatalf("rows = %+v", report.Rows)
	}
	for _, row := range want {
		if got[row.Kind] != row {
			t.Errorf("row %q = %+v, want %+v", row.Kind, got[row.Kind], row)
		}
	}
	// Rows follow the kind vocabulary's order.
	if report.Rows[0].Kind != "design" || report.Rows[1].Kind != "ticket_hygiene" || report.Rows[2].Kind != "pr_hygiene" {
		t.Fatalf("row order = %+v", report.Rows)
	}

	fromSession, err := fixture.service.Observe(ctx, Actor{Session: fixture.w2}, "")
	if err != nil || fromSession.ProjectID != "shop" || len(fromSession.Rows) != 3 {
		t.Fatalf("session view = %+v, %v", fromSession, err)
	}
	if _, err := fixture.service.Observe(ctx, person, ""); err == nil {
		t.Fatal("the person must name a project")
	}
}
