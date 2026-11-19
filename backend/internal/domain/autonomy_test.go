package domain

import (
	"strings"
	"testing"
)

func TestDecisionKindsTableIsStable(t *testing.T) {
	kinds := DecisionKinds()
	wantReserved := []DecisionKindID{
		"production", "merge_publish", "destructive", "credentials", "spending",
		"public_api", "dependency", "scope", "design", "people", "project_default", "other",
	}
	wantOpen := []DecisionKindID{
		"ticket_hygiene", "pr_hygiene", "tests_hooks", "session_lifecycle",
		"test_fixtures", "observability", "interpretation",
	}
	if len(kinds) != len(wantReserved)+len(wantOpen) {
		t.Fatalf("got %d kinds, want %d", len(kinds), len(wantReserved)+len(wantOpen))
	}
	seen := map[DecisionKindID]DecisionKindInfo{}
	for _, kind := range kinds {
		if strings.TrimSpace(kind.Label) == "" {
			t.Fatalf("kind %q has no label", kind.ID)
		}
		if _, dup := seen[kind.ID]; dup {
			t.Fatalf("kind %q listed twice", kind.ID)
		}
		seen[kind.ID] = kind
	}
	for _, id := range wantReserved {
		if !seen[id].ReservedByDefault {
			t.Errorf("kind %q should be reserved by default", id)
		}
	}
	for _, id := range wantOpen {
		info, ok := seen[id]
		if !ok || info.ReservedByDefault {
			t.Errorf("kind %q should exist and not be reserved by default", id)
		}
	}
	if kinds[len(kinds)-1].ID != DecisionKindOther {
		t.Errorf("other should be listed last, got %q", kinds[len(kinds)-1].ID)
	}
}

func TestLookupDecisionKind(t *testing.T) {
	info, ok := LookupDecisionKind("pr_hygiene")
	if !ok || info.Label != "Pull request hygiene" {
		t.Fatalf("LookupDecisionKind(pr_hygiene) = %+v, %v", info, ok)
	}
	if _, ok := LookupDecisionKind("nope"); ok {
		t.Fatal("unknown kind was found")
	}
}

func TestAutonomyConfigValidate(t *testing.T) {
	cases := []struct {
		name string
		cfg  AutonomyConfig
		want string
	}{
		{"zero", AutonomyConfig{}, ""},
		{"off", AutonomyConfig{Mode: AutonomyOff}, ""},
		{"observe with list", AutonomyConfig{Mode: AutonomyObserve, Reserved: []DecisionKindID{"production", "other"}}, ""},
		{"unknown mode", AutonomyConfig{Mode: "always"}, `autonomy.mode: unknown mode "always"`},
		{"unknown kind", AutonomyConfig{Reserved: []DecisionKindID{"production", "lunch"}}, `autonomy.reserved: unknown decision kind "lunch"`},
		{"duplicate kind", AutonomyConfig{Reserved: []DecisionKindID{"design", "design"}}, `autonomy.reserved: "design" is listed twice`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("Validate() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAutonomyEffectiveMode(t *testing.T) {
	if got := (AutonomyConfig{}).EffectiveMode(); got != AutonomyOff {
		t.Fatalf("empty mode = %q, want off", got)
	}
	if got := (AutonomyConfig{Mode: AutonomyObserve}).EffectiveMode(); got != AutonomyObserve {
		t.Fatalf("observe mode = %q", got)
	}
}

func TestAutonomyIsReservedDefaults(t *testing.T) {
	var defaults AutonomyConfig
	for id, want := range map[DecisionKindID]bool{"production": true, "other": true, "pr_hygiene": false, "lunch": false} {
		if got := defaults.IsReserved(id); got != want {
			t.Errorf("default IsReserved(%q) = %v, want %v", id, got, want)
		}
	}
	custom := AutonomyConfig{Reserved: []DecisionKindID{"pr_hygiene"}}
	if !custom.IsReserved("pr_hygiene") || custom.IsReserved("production") {
		t.Fatal("an explicit reserved list must replace the defaults")
	}
}

func TestProjectConfigValidateIncludesAutonomy(t *testing.T) {
	cfg := ProjectConfig{Autonomy: AutonomyConfig{Mode: "sometimes"}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "autonomy.mode") {
		t.Fatalf("Validate() = %v, want autonomy.mode error", err)
	}
}
