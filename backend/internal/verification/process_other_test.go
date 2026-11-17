//go:build !windows

package verification

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

func TestRunnerHostShellUsesRequestedWorkspaceAndEnvironment(t *testing.T) {
	workspace := t.TempDir()
	result, err := NewRunner().Run(context.Background(), Request{
		ProjectID: "demo", WorkspacePath: workspace,
		Commands: []string{`printf '%s|%s|%s|%s' "$PWD" "$VERIFY_VALUE" "$0" "${1-}"`},
		Env:      map[string]string{"VERIFY_VALUE": "from-project"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || !strings.Contains(result.OutputTail, workspace+"|[REDACTED]|sh|") {
		t.Fatalf("result = %+v", result)
	}
}

func TestWorkspaceStepPreservesShellExitStatus(t *testing.T) {
	env := newStepEnvironment(os.Environ(), nil)
	err := runWorkspaceStep(context.Background(), t.TempDir(), `exit 7`, env, io.Discard)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("runWorkspaceStep() error = %v, want exit status 7", err)
	}
}

func TestWorkspaceStepFailsAndStopsALingeringBackgroundChild(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command string
	}{
		{name: "holds output pipe", command: `sleep 60 & echo $! > "$VERIFY_CHILD_PID"`},
		{name: "shell fails", command: `sleep 60 & echo $! > "$VERIFY_CHILD_PID"; exit 7`},
		{name: "redirects output", command: `sleep 60 >/dev/null 2>&1 & echo $! > "$VERIFY_CHILD_PID"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			pidFile := filepath.Join(workspace, "child.pid")
			env := newStepEnvironment(os.Environ(), map[string]string{
				"VERIFY_CHILD_PID": pidFile,
			})
			err := runWorkspaceStep(context.Background(), workspace, tc.command, env, io.Discard)
			pid := readProcessID(t, pidFile)
			t.Cleanup(func() {
				if processAlive(pid) {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			})
			if err == nil {
				t.Fatal("runWorkspaceStep() succeeded with a surviving background child")
			}
			deadline := time.Now().Add(2 * time.Second)
			for processAlive(pid) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if processAlive(pid) {
				t.Fatalf("background child %d survived verification (error %v)", pid, err)
			}
		})
	}
}

func TestWorkspaceStepFailsAndStopsADetachedChild(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	pidFile := filepath.Join(workspace, "detached.pid")
	env := newStepEnvironment(os.Environ(), map[string]string{
		"GO_WANT_VERIFICATION_DETACHED_CHILD": "1",
		"VERIFY_DETACHED_HELPER":              executable,
		"VERIFY_DETACHED_PID":                 pidFile,
	})
	err = runWorkspaceStep(
		context.Background(),
		workspace,
		`"$VERIFY_DETACHED_HELPER" -test.run=^TestVerificationDetachedChildHelper$ >/dev/null 2>&1 & sleep 1`,
		env,
		io.Discard,
	)
	pid := readProcessID(t, pidFile)
	t.Cleanup(func() {
		if processAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	if err == nil {
		t.Fatal("runWorkspaceStep() succeeded with a detached background child")
	}
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Fatalf("detached child %d survived verification (error %v)", pid, err)
	}
}

func TestVerificationDetachedChildHelper(t *testing.T) {
	if os.Getenv("GO_WANT_VERIFICATION_DETACHED_CHILD") != "1" {
		return
	}
	if _, err := syscall.Setsid(); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(os.Getenv("VERIFY_DETACHED_PID"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		os.Exit(3)
	}
	time.Sleep(time.Minute)
}

func TestUnixCleanupNeverSignalsARecycledPID(t *testing.T) {
	var signals []syscall.Signal
	hooks := unixProcessHooks{
		identities: func(context.Context) (map[int]procmem.Identity, error) {
			return map[int]procmem.Identity{
				4242: {PPID: 1, Start: "replacement", StartedAt: time.Unix(20, 0)},
			}, nil
		},
		signal: func(_ int, signal syscall.Signal) error {
			signals = append(signals, signal)
			return nil
		},
		wait: time.Millisecond,
		poll: time.Microsecond,
	}
	survived, err := stopCapturedUnixProcesses(
		context.Background(),
		unixCapturedProcess{pid: 4000, start: "anchor", startedAt: time.Unix(1, 0)},
		[]unixCapturedProcess{{pid: 4242, start: "original", startedAt: time.Unix(2, 0)}},
		hooks,
	)
	if err != nil {
		t.Fatal(err)
	}
	if survived || len(signals) != 0 {
		t.Fatalf("recycled pid cleanup = survived %v, signals %v; want no signal", survived, signals)
	}
}

func TestRunnerPreservesParentCancellationFromARealCommand(t *testing.T) {
	workspace := t.TempDir()
	ready := filepath.Join(workspace, "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result Result
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := NewRunner().Run(ctx, Request{
			ProjectID: "demo", WorkspacePath: workspace,
			Commands: []string{`touch "$VERIFY_READY"; while :; do sleep 1; done`},
			Env:      map[string]string{"VERIFY_READY": ready},
		})
		finished <- outcome{result: result, err: err}
	}()
	waitForFile(t, ready)
	cancel()
	select {
	case got := <-finished:
		if !errors.Is(got.err, context.Canceled) || got.result.Passed {
			t.Fatalf("canceled run = %+v, %v; want context.Canceled", got.result, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled verification did not stop")
	}
}

func readProcessID(t *testing.T, path string) int {
	t.Helper()
	waitForFile(t, path)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		t.Fatalf("child pid = %q, %v", raw, err)
	}
	return pid
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("file %s did not appear", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
