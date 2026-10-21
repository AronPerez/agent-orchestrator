package specgen_test

import (
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec/specgen"
)

func TestBuildForkFrozenClientContracts(t *testing.T) {
	raw, err := specgen.Build()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]openAPISchemaNode `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	scopes := doc.Components.Schemas["WorkspaceDiffRequest"].Properties["scope"].Enum
	if !slices.Equal(scopes, []string{"combined", "committed", "staged", "unstaged", "untracked"}) {
		t.Fatalf("frozen review scope changed: %v", scopes)
	}
	owners := doc.Components.Schemas["APIError"].Properties["reporting_owner"].Enum
	if !slices.Contains(owners, "agent_switch_saga") {
		t.Fatal("frozen error classifier cannot read historical saga ownership")
	}
	if _, ok := doc.Components.Schemas["AgentSwitch"]; !ok {
		t.Fatal("legacy switch receipt schema missing")
	}
}
