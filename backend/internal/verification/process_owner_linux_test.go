//go:build linux

package verification

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWorkspaceStepStopsDetachedChildWithCleanEnvironment(t *testing.T) {
	workspace := t.TempDir()
	pidFile := filepath.Join(workspace, "clean-environment.pid")
	env := newStepEnvironment(os.Environ(), map[string]string{"VERIFY_CHILD_PID": pidFile})

	err := runWorkspaceStep(
		context.Background(),
		workspace,
		`env -i PATH=/usr/bin:/bin VERIFY_CHILD_PID="$VERIFY_CHILD_PID" sh -c 'setsid sleep 60 >/dev/null 2>&1 & echo $! > "$VERIFY_CHILD_PID"'; sleep 1`,
		env,
		io.Discard,
	)
	pid := readProcessID(t, pidFile)
	assertVerificationChildStopped(t, pid, err)
}

func TestWorkspaceStepStopsChildWithUnreadableEnvironment(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	pidFile := filepath.Join(workspace, "unreadable-environment.pid")
	env := newStepEnvironment(os.Environ(), map[string]string{
		"GO_WANT_VERIFICATION_NONDUMPABLE_CHILD": "1",
		"VERIFY_NONDUMPABLE_HELPER":              executable,
		"VERIFY_CHILD_PID":                       pidFile,
	})

	err = runWorkspaceStep(
		context.Background(),
		workspace,
		`"$VERIFY_NONDUMPABLE_HELPER" -test.run=^TestVerificationNondumpableChildHelper$ >/dev/null 2>&1 & sleep 1`,
		env,
		io.Discard,
	)
	pid := readProcessID(t, pidFile)
	assertVerificationChildStopped(t, pid, err)
}

func TestVerificationNondumpableChildHelper(t *testing.T) {
	if os.Getenv("GO_WANT_VERIFICATION_NONDUMPABLE_CHILD") != "1" {
		return
	}
	if _, err := syscall.Setsid(); err != nil {
		os.Exit(2)
	}
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(os.Getenv("VERIFY_CHILD_PID"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		os.Exit(4)
	}
	time.Sleep(time.Minute)
}

func assertVerificationChildStopped(t *testing.T, pid int, runErr error) {
	t.Helper()
	t.Cleanup(func() {
		if processAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	if runErr == nil {
		t.Fatal("runWorkspaceStep() succeeded with a surviving background child")
	}
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Fatalf("background child %d survived verification (error %v)", pid, runErr)
	}
}
