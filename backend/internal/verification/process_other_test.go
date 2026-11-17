//go:build !windows

package verification

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunnerHostShellUsesRequestedWorkspaceAndEnvironment(t *testing.T) {
	workspace := t.TempDir()
	result, err := NewRunner().Run(context.Background(), Request{
		ProjectID: "demo", WorkspacePath: workspace,
		Commands: []string{`printf '%s|%s' "$PWD" "$VERIFY_VALUE"`},
		Env:      map[string]string{"VERIFY_VALUE": "from-project"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || !strings.Contains(result.OutputTail, workspace+"|[REDACTED]") {
		t.Fatalf("result = %+v", result)
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
