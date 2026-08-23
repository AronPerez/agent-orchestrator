package lifecycle

import "testing"

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
