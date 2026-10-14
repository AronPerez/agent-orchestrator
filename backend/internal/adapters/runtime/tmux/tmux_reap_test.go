//go:build !windows

package tmux

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestDestroyReapsSIGHUPTrappingAgent reproduces the reap-then-resurrect bug: an
// agent that traps SIGHUP survives `tmux kill-session`, so before the fix Destroy
// returned while the process group was still alive (and Manager.Kill would then
// mark the session terminated while it kept running, completing outbound side
// effects). Destroy must escalate to an uncatchable SIGKILL and confirm the group
// is dead before returning.
func TestDestroyReapsSIGHUPTrappingAgent(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	ctx := context.Background()
	id := strings.ReplaceAll(t.Name(), "/", "_")
	// Short grace so the test spends ~100ms in the SIGHUP window before SIGKILL.
	r := New(Options{Timeout: 5 * time.Second, KillTermGrace: 100 * time.Millisecond, KillConfirmTimeout: 2 * time.Second})
	_ = r.Destroy(ctx, ports.RuntimeHandle{ID: id})
	t.Cleanup(func() { _ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: id}) })

	// A shell that ignores SIGHUP and never exits on its own: kill-session's
	// SIGHUP cannot reap it, so only the SIGKILL escalation can.
	h, err := r.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(id),
		WorkspacePath: t.TempDir(),
		Argv:          []string{"sh", "-c", "trap '' HUP; while :; do sleep 1; done"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Resolve the pane's process group while it is alive, the same way Destroy
	// does, so the assertion targets the exact group Destroy signals.
	out, err := r.run(ctx, panePIDArgs(id)...)
	if err != nil {
		t.Fatalf("resolve pane pid: %v", err)
	}
	panePID, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse pane pid %q: %v", string(out), err)
	}
	pgid, err := syscall.Getpgid(panePID)
	if err != nil {
		t.Fatalf("getpgid(%d): %v", panePID, err)
	}

	if err := r.Destroy(ctx, h); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	// The whole process group must be gone the instant Destroy returns. Before
	// the fix, the SIGHUP-trapping shell is still alive here.
	if err := syscall.Kill(-pgid, 0); err != syscall.ESRCH {
		t.Fatalf("process group %d still alive after Destroy (kill -0 => %v), want ESRCH", pgid, err)
	}
}
