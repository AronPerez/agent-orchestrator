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

// Fork regression: native identity now lives on the session, not the retired
// switching saga. A nested agent must still not overwrite its owner's identity.
func TestActivitySignalProtectsNativeIdentityByCWD(t *testing.T) {
	for _, tc := range []struct {
		name, cwd string
		accepted  bool
	}{
		{"nested agent", "/tmp/scratch/probe", false},
		{"session agent", "/data/worktrees/proj/mer-1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			rec := working("mer-1")
			rec.Metadata.RuntimeLaunchID = "target-generation"
			rec.Metadata.WorkspacePath = "/data/worktrees/proj/mer-1"
			store.sessions[rec.ID] = rec
			m := New(store, &fakeMessenger{})
			if err := m.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{
				Valid: true, State: domain.ActivityActive, Event: "user-prompt-submit",
				LaunchID: "target-generation", AgentSessionID: "codex-native",
				TranscriptPath: "/provider/codex-native.jsonl", AgentCWD: tc.cwd,
			}); err != nil {
				t.Fatal(err)
			}
			got := store.sessions[rec.ID].Metadata
			if tc.accepted {
				if got.AgentSessionID != "codex-native" || got.NativeTranscriptPath != "/provider/codex-native.jsonl" {
					t.Fatalf("own native identity was not recorded: %+v", got)
				}
			} else if got.AgentSessionID != "" || got.NativeTranscriptPath != "" {
				t.Fatalf("nested agent replaced native identity: %+v", got)
			}
		})
	}
}
