package agy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// StandingRuleHolds must recognise exactly what GetAgentHooks writes, so a
// change to the rule format cannot silently stop or wrongly start the Chat
// de-duplication that relies on it.
func TestStandingRuleHoldsMatchesTheInstalledInstructionsExactly(t *testing.T) {
	workspace := t.TempDir()
	if err := (&Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{
		WorkspacePath: workspace, SystemPrompt: "  You are an AO worker.\n",
	}); err != nil {
		t.Fatal(err)
	}

	if !StandingRuleHolds(workspace, "You are an AO worker.") {
		t.Fatal("the installed instructions must be recognised")
	}
	for name, instructions := range map[string]string{
		"different":  "You are an AO orchestrator.",
		"appended":   "You are an AO worker.\n\nConversation so far: ...",
		"blank":      "  ",
		"no context": "",
	} {
		if StandingRuleHolds(workspace, instructions) {
			t.Errorf("%s: reported as held by the rule", name)
		}
	}
	if StandingRuleHolds("", "You are an AO worker.") {
		t.Error("no workspace: reported as held")
	}
}

func TestStandingRuleHoldsRejectsAMissingOrForeignRule(t *testing.T) {
	if StandingRuleHolds(t.TempDir(), "x") {
		t.Fatal("no rule: reported as held")
	}
	workspace := t.TempDir()
	rulePath := agyStandingRulePath(workspace)
	if err := os.MkdirAll(filepath.Dir(rulePath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulePath, []byte(agyStandingRuleFront+"x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if StandingRuleHolds(workspace, "x") {
		t.Fatal("a rule without AO's marker: reported as held")
	}
}
