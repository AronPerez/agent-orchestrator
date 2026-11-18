package decision

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func setShopAutonomy(t *testing.T, fixture decisionServiceFixture, cfg domain.AutonomyConfig) {
	t.Helper()
	ctx := context.Background()
	project, ok, err := fixture.store.GetProject(ctx, "shop")
	if err != nil || !ok {
		t.Fatalf("GetProject = %v, %v", ok, err)
	}
	project.Config.Autonomy = cfg
	if err := fixture.store.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
}

func assertRaiseRule(t *testing.T, err error, rule string) {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "DECISION_INVALID" {
		t.Fatalf("err = %v, want DECISION_INVALID", err)
	}
	if got := apiErr.Details["rule"]; got != rule {
		t.Fatalf("rule = %v, want %q (message %q)", got, rule, apiErr.Message)
	}
}

func predictedRaise(kind domain.DecisionKindID) RaiseInput {
	input := validRaiseInput()
	input.Kind = string(kind)
	input.Prediction = &domain.DecisionPrediction{Option: 1, Why: "It matches the last three calls.", Undo: "Switch the setting back."}
	return input
}

func TestRaiseOffModeIsUnchanged(t *testing.T) {
	fixture := newDecisionService(t)
	call, err := fixture.service.Raise(context.Background(), Actor{Session: fixture.w1}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	if call.Raised.AutonomyMode != domain.AutonomyOff || call.Raised.Reserved || call.Raised.Kind != "" {
		t.Fatalf("raised = %+v", call.Raised)
	}
}

func TestRaiseDefaultsUrgencyToCanWait(t *testing.T) {
	fixture := newDecisionService(t)
	call, err := fixture.service.Raise(context.Background(), Actor{Session: fixture.w1}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	if call.Raised.Urgency != domain.DecisionUrgencyCanWait {
		t.Fatalf("urgency = %q, want can_wait", call.Raised.Urgency)
	}
	input := validRaiseInput()
	input.Urgency = domain.DecisionUrgencyBlocking
	call, err = fixture.service.Raise(context.Background(), Actor{Session: fixture.w1}, input)
	if err != nil {
		t.Fatal(err)
	}
	if call.Raised.Urgency != domain.DecisionUrgencyBlocking {
		t.Fatalf("urgency = %q, want blocking", call.Raised.Urgency)
	}
}

func TestRaiseObserveModeNeedsAKind(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyObserve})
	_, err := fixture.service.Raise(context.Background(), Actor{Session: fixture.w1}, validRaiseInput())
	assertRaiseRule(t, err, "kind_required")
}

func TestRaiseObserveModeNeedsAPredictionForOpenKinds(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyObserve})
	input := validRaiseInput()
	input.Kind = "pr_hygiene"
	_, err := fixture.service.Raise(context.Background(), Actor{Session: fixture.w1}, input)
	assertRaiseRule(t, err, "prediction_required")

	call, err := fixture.service.Raise(context.Background(), Actor{Session: fixture.w1}, predictedRaise("pr_hygiene"))
	if err != nil {
		t.Fatal(err)
	}
	if call.Raised.AutonomyMode != domain.AutonomyObserve || call.Raised.Reserved || call.Raised.Prediction == nil {
		t.Fatalf("raised = %+v", call.Raised)
	}
}

func TestRaiseRefusesAPredictionOnAReservedKind(t *testing.T) {
	for _, mode := range []domain.AutonomyMode{"", domain.AutonomyObserve} {
		t.Run(string(mode), func(t *testing.T) {
			fixture := newDecisionService(t)
			setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: mode})
			_, err := fixture.service.Raise(context.Background(), Actor{Session: fixture.w1}, predictedRaise("production"))
			assertRaiseRule(t, err, "prediction_on_reserved")
		})
	}
}

func TestRaiseReservedKindNeedsNoPrediction(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyObserve})
	input := validRaiseInput()
	input.Kind = "design"
	call, err := fixture.service.Raise(context.Background(), Actor{Session: fixture.w1}, input)
	if err != nil {
		t.Fatal(err)
	}
	if !call.Raised.Reserved || call.Raised.Kind != "design" {
		t.Fatalf("raised = %+v", call.Raised)
	}
}

func TestRaiseSnapshotsReservedAndMode(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyObserve})
	call, err := fixture.service.Raise(ctx, Actor{Session: fixture.w1}, predictedRaise("pr_hygiene"))
	if err != nil {
		t.Fatal(err)
	}
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyOff, Reserved: []domain.DecisionKindID{"pr_hygiene"}})
	detail, err := fixture.service.Get(ctx, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Call.Raised.Reserved || detail.Call.Raised.AutonomyMode != domain.AutonomyObserve {
		t.Fatalf("raised changed with the policy: %+v", detail.Call.Raised)
	}
}

func TestRaiseFromASessionWithNoProjectIgnoresPolicy(t *testing.T) {
	fixture := newDecisionService(t)
	call, err := fixture.service.Raise(context.Background(), Actor{Session: fixture.loose}, predictedRaise("pr_hygiene"))
	if err != nil {
		t.Fatal(err)
	}
	if call.Raised.AutonomyMode != domain.AutonomyOff || call.Raised.Reserved {
		t.Fatalf("raised = %+v", call.Raised)
	}
}

func TestKindsReportsTheProjectPolicy(t *testing.T) {
	fixture := newDecisionService(t)
	setShopAutonomy(t, fixture, domain.AutonomyConfig{Mode: domain.AutonomyObserve, Reserved: []domain.DecisionKindID{"design"}})
	policy, err := fixture.service.Kinds(context.Background(), Actor{Session: fixture.w1}, "")
	if err != nil {
		t.Fatal(err)
	}
	if policy.ProjectID != "shop" || policy.Mode != domain.AutonomyObserve || len(policy.Kinds) != len(domain.DecisionKinds()) {
		t.Fatalf("policy = %+v", policy)
	}
	for _, kind := range policy.Kinds {
		if want := kind.ID == "design"; kind.Reserved != want {
			t.Fatalf("kind %q reserved = %v, want %v", kind.ID, kind.Reserved, want)
		}
	}
	personal, err := fixture.service.Kinds(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, "shop")
	if err != nil || personal.Mode != domain.AutonomyObserve {
		t.Fatalf("person view = %+v, %v", personal, err)
	}
	if _, err := fixture.service.Kinds(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, ""); err == nil {
		t.Fatal("the person must name a project")
	}
}
