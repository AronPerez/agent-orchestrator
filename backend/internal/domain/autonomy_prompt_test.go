package domain

import (
	"strings"
	"testing"
)

func TestAutonomyInstructionsOffIsEmpty(t *testing.T) {
	for _, cfg := range []AutonomyConfig{{}, {Mode: AutonomyOff, Reserved: []DecisionKindID{"design"}}} {
		if got := AutonomyInstructions(cfg); got != "" {
			t.Fatalf("AutonomyInstructions(%+v) = %q, want empty", cfg, got)
		}
	}
}

func TestAutonomyInstructionsObserveListsKindsAndRules(t *testing.T) {
	got := AutonomyInstructions(AutonomyConfig{Mode: AutonomyObserve})
	for _, want := range []string{
		"## Reserved Decisions (observe mode)",
		"Reserved for the human:\n- Production changes and deploys (`production`)",
		"- Anything else (`other`)",
		"Not reserved:\n- Ticket hygiene (`ticket_hygiene`)",
		"`--kind <id>`",
		"Before any action of a reserved kind, raise a call",
		"`--would-decide <n> --why <reason> --undo <how the human could undo it>`",
		"follow that answer even when it differs",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions missing %q:\n%s", want, got)
		}
	}
	reservedPart, openPart, _ := strings.Cut(got, "Not reserved:")
	if strings.Contains(reservedPart, "`pr_hygiene`") || strings.Contains(openPart, "`production`") {
		t.Fatalf("kinds listed under the wrong heading:\n%s", got)
	}
}

func TestAutonomyInstructionsFollowTheProjectList(t *testing.T) {
	got := AutonomyInstructions(AutonomyConfig{Mode: AutonomyObserve, Reserved: []DecisionKindID{"pr_hygiene"}})
	reservedPart, openPart, _ := strings.Cut(got, "Not reserved:")
	if !strings.Contains(reservedPart, "`pr_hygiene`") || !strings.Contains(openPart, "`production`") {
		t.Fatalf("custom list not applied:\n%s", got)
	}
}

func TestAutonomyInstructionsOnModeDecidesAndRecords(t *testing.T) {
	got := AutonomyInstructions(AutonomyConfig{Mode: AutonomyOn})
	for _, want := range []string{
		"## Reserved Decisions (on)",
		"Reserved for the human:\n- Production changes and deploys (`production`)",
		"Not reserved:\n- Ticket hygiene (`ticket_hygiene`)",
		"`ao decision decide --kind <id> --what <what you decided> --why <why> --undo <how to undo it>`",
		"Decided for you",
		"record the decision before you act on it",
		"`ao decision outcome <id> --text \"Not carried out: <why>\"`",
		`"I overruled the decision you made for me"`,
		"When you are unsure which kind applies, or whether it can be undone, raise a call instead.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--would-decide") {
		t.Error("on mode does not ask for predictions")
	}
}
