package lifecycle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A nested agent process inherits AO_SESSION_ID and AO_RUNTIME_LAUNCH_ID from the
// session that spawned it, so the launch fence cannot tell it apart. Its cwd can.
//
// Measured on a disposable session (agent-orchestrator-87) before this guard
// existed: a nested `claude -p` run from a scratch directory with the parent's
// env overwrote latest_assistant_update, replaced agent_session_id and
// native_transcript_path with its own throwaway conversation, and drove the
// session to activity_state=exited while its real agent was alive and idle —
// after which `ao send` refused delivery with AGENT_EXITED.
func TestSignalCWDBelongsToSession(t *testing.T) {
	const workspace = "/data/worktrees/proj/proj-1"

	for _, tc := range []struct {
		name      string
		workspace string
		cwd       string
		want      bool
	}{
		{"session's own agent, workspace root", workspace, workspace, true},
		{"session's own agent, subdirectory", workspace, workspace + "/backend/internal", true},
		{"unnormalized but inside", workspace, workspace + "/backend/../backend", true},
		{"trailing separator", workspace, workspace + "/", true},

		{"nested agent in a scratch dir", workspace, "/tmp/scratch/probe", false},
		{"nested agent in another session's worktree", workspace, "/data/worktrees/proj/proj-2", false},
		{"sibling with a shared prefix", workspace, workspace + "-other", false},
		{"parent of the workspace", workspace, "/data/worktrees/proj", false},

		// Absent facts must never invent a rejection: older CLIs send no cwd, and
		// some sessions have no recorded workspace.
		{"harness reports no cwd", workspace, "", true},
		{"session has no workspace", "", "/tmp/anywhere", true},
		{"neither known", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := signalCWDBelongsToSession(tc.workspace, tc.cwd); got != tc.want {
				t.Fatalf("signalCWDBelongsToSession(%q, %q) = %v, want %v",
					tc.workspace, tc.cwd, got, tc.want)
			}
		})
	}
}

// The guard compares a path AO recorded against a path an OS reported, and those
// two can name the same directory in different forms. Rejecting on the form is a
// false rejection, and a false rejection strands the session exactly the way the
// dropped hooks of fork #109 did — silently, and permanently.
func TestSignalCWDBelongsToSessionAcceptsSymlinkedWorkspaceForm(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "physical", "proj", "sess-1")
	if err := os.MkdirAll(filepath.Join(physical, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "worktrees")
	if err := os.Symlink(filepath.Join(root, "physical"), link); err != nil {
		t.Fatal(err)
	}
	viaLink := filepath.Join(link, "proj", "sess-1")

	for _, tc := range []struct {
		name      string
		workspace string
		cwd       string
	}{
		{"workspace recorded through a symlink, agent reports the physical path", viaLink, physical},
		{"same, agent in a subdirectory", viaLink, filepath.Join(physical, "backend")},
		{"reversed: workspace physical, agent reports the symlinked form", physical, viaLink},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !signalCWDBelongsToSession(tc.workspace, tc.cwd) {
				t.Fatalf("signalCWDBelongsToSession(%q, %q) = false, want true: "+
					"the same directory in two forms is not an outside agent", tc.workspace, tc.cwd)
			}
		})
	}

	// Resolving forms must not cost the guard its teeth.
	outside := filepath.Join(root, "physical", "proj", "sess-2")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if signalCWDBelongsToSession(viaLink, outside) {
		t.Fatal("a real sibling worktree was accepted after symlink resolution")
	}
}

// The cwd guard in ApplyActivitySignal runs after stagePendingAgentSwitchNativeMetadata,
// which writes the very resume handle the guard exists to protect. A nested agent
// spawned by an agent-switch target inherits its launch id, so it clears the launch
// fence; without a cwd check on this path its throwaway conversation becomes the
// target's resume handle.
func TestNestedAgentCannotStageAgentSwitchResumeHandle(t *testing.T) {
	store := newFakeAgentSwitchLifecycleStore()
	ref := domain.AgentNativeSessionID("native-target")
	store.native[ref] = domain.AgentNativeSession{
		ID: ref, AOSessionID: "mer-1", Harness: domain.HarnessCodex,
		LastGenerationID: "target-generation",
	}
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "target-generation"
	rec.Metadata.WorkspacePath = "/data/worktrees/proj/mer-1"
	store.setSession(rec)
	store.setActiveSwitch(domain.AgentSwitch{
		ID: "switch-1", SessionID: rec.ID, FromHarness: domain.HarnessClaudeCode,
		TargetHarness: domain.HarnessCodex, State: domain.AgentSwitchStartingTarget,
		SourceGenerationID: "source-generation", TargetGenerationID: "target-generation",
		TargetNativeSessionRef: &ref,
	})
	m := New(store, &fakeMessenger{})

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit",
		LaunchID:       "target-generation",
		AgentSessionID: "nested-throwaway",
		TranscriptPath: "/tmp/scratch/nested-throwaway.jsonl",
		AgentCWD:       "/tmp/scratch/probe",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal: %v", err)
	}

	store.mu.Lock()
	got := store.native[ref]
	store.mu.Unlock()
	if got.NativeSessionID != "" || got.TranscriptPath != "" {
		t.Fatalf("out-of-workspace signal staged the target resume handle: %+v", got)
	}
}

// The session's own agent must still be able to stage it.
func TestSessionAgentStillStagesAgentSwitchResumeHandle(t *testing.T) {
	store := newFakeAgentSwitchLifecycleStore()
	ref := domain.AgentNativeSessionID("native-target")
	store.native[ref] = domain.AgentNativeSession{
		ID: ref, AOSessionID: "mer-1", Harness: domain.HarnessCodex,
		LastGenerationID: "target-generation",
	}
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "target-generation"
	rec.Metadata.WorkspacePath = "/data/worktrees/proj/mer-1"
	store.setSession(rec)
	store.setActiveSwitch(domain.AgentSwitch{
		ID: "switch-1", SessionID: rec.ID, FromHarness: domain.HarnessClaudeCode,
		TargetHarness: domain.HarnessCodex, State: domain.AgentSwitchStartingTarget,
		SourceGenerationID: "source-generation", TargetGenerationID: "target-generation",
		TargetNativeSessionRef: &ref,
	})
	m := New(store, &fakeMessenger{})

	if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit",
		LaunchID:       "target-generation",
		AgentSessionID: "codex-native",
		TranscriptPath: "/provider/codex-native.jsonl",
		AgentCWD:       "/data/worktrees/proj/mer-1",
	}); err != nil {
		t.Fatalf("ApplyActivitySignal: %v", err)
	}

	store.mu.Lock()
	got := store.native[ref]
	store.mu.Unlock()
	if got.NativeSessionID != "codex-native" || got.TranscriptPath != "/provider/codex-native.jsonl" {
		t.Fatalf("the session's own agent failed to stage its resume handle: %+v", got)
	}
}
