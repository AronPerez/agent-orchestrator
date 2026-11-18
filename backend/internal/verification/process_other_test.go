//go:build !windows

package verification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
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

func TestRunnerHostShellRedactsInheritedAuggieSessionAuth(t *testing.T) {
	const secret = "inherited-auggie-host-session"
	t.Setenv("AUGMENT_SESSION_AUTH", secret)
	result, err := NewRunner().Run(context.Background(), Request{
		ProjectID: "demo", WorkspacePath: t.TempDir(),
		Commands: []string{`printf '%s' "$AUGMENT_SESSION_AUTH"`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.OutputTail, secret) || !strings.Contains(result.OutputTail, "[REDACTED]") {
		t.Fatalf("inherited Auggie credential was not redacted: %q", result.OutputTail)
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

func TestWorkspaceStepPreservesProjectFileDescriptors(t *testing.T) {
	for _, fd := range []int{3, 4} {
		t.Run(strconv.Itoa(fd), func(t *testing.T) {
			workspace := t.TempDir()
			logFile := filepath.Join(workspace, "verification.log")
			env := newStepEnvironment(os.Environ(), map[string]string{"VERIFY_FD_LOG": logFile})
			command := fmt.Sprintf(`exec %d>"$VERIFY_FD_LOG"; printf ok >&%d`, fd, fd)
			if err := runWorkspaceStep(context.Background(), workspace, command, env, io.Discard); err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(logFile)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := string(contents), "ok"; got != want {
				t.Fatalf("fd %d output = %q, want %q", fd, got, want)
			}
		})
	}
}

func TestWorkspaceStepDoesNotStartProjectBeforeIdentityCapture(t *testing.T) {
	workspace := t.TempDir()
	pidFile := filepath.Join(workspace, "child.pid")
	original := verificationProcessStartForWatchFn
	verificationProcessStartForWatchFn = func(int) (string, error) {
		deadline := time.Now().Add(250 * time.Millisecond)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(pidFile); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		return "", errors.New("injected identity failure")
	}
	t.Cleanup(func() { verificationProcessStartForWatchFn = original })
	env := newStepEnvironment(os.Environ(), map[string]string{"VERIFY_CHILD_PID": pidFile})
	cmd := workspaceStepCommand(
		context.Background(),
		`sleep 60 >/dev/null 2>&1 & echo $! > "$VERIFY_CHILD_PID"`,
	)
	cmd.Dir = workspace
	cmd.Env = append([]string(nil), env.list...)
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	tree, err := startWorkspaceProcessTree(context.Background(), cmd)
	if tree != nil || err == nil || !strings.Contains(err.Error(), "injected identity failure") {
		t.Fatalf("startWorkspaceProcessTree() = tree %v, error %v", tree, err)
	}
	if raw, readErr := os.ReadFile(pidFile); readErr == nil {
		pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
		if pid > 0 && processAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		t.Fatalf("project command started before identity capture; child pid %d", pid)
	} else if !os.IsNotExist(readErr) {
		t.Fatalf("read child pid: %v", readErr)
	}
}

func TestWorkspaceStepDrainsOwnershipDescriptorWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	env := newStepEnvironment(os.Environ(), nil)
	if err := runWorkspaceStep(
		ctx,
		t.TempDir(),
		`dd if=/dev/zero bs=65536 count=4 >&3 2>/dev/null`,
		env,
		io.Discard,
	); err != nil {
		t.Fatalf("write to inherited fd 3 blocked verification: %v", err)
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

func TestWorkspaceStepFailsAndStopsADoubleForkedChild(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	pidFile := filepath.Join(workspace, "double-forked.pid")
	env := newStepEnvironment(os.Environ(), map[string]string{
		"GO_WANT_VERIFICATION_DOUBLE_FORK": "1",
		"VERIFY_DOUBLE_FORK_HELPER":        executable,
		"VERIFY_DOUBLE_FORK_PID":           pidFile,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = runWorkspaceStep(
		ctx,
		workspace,
		`"$VERIFY_DOUBLE_FORK_HELPER" -test.run=^TestVerificationDoubleForkHelper$; while [ ! -s "$VERIFY_DOUBLE_FORK_PID" ]; do sleep 1; done`,
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
		t.Fatal("runWorkspaceStep() succeeded with a double-forked background child")
	}
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Fatalf("double-forked child %d survived verification (error %v)", pid, err)
	}
}

func TestVerificationDoubleForkHelper(t *testing.T) {
	stage := os.Getenv("GO_WANT_VERIFICATION_DOUBLE_FORK")
	if stage == "" {
		return
	}
	if stage == "3" {
		if err := os.WriteFile(os.Getenv("VERIFY_DOUBLE_FORK_PID"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(5)
		}
		time.Sleep(time.Minute)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	next := "2"
	if stage == "2" {
		next = "3"
	}
	child := exec.Command(executable, "-test.run=^TestVerificationDoubleForkHelper$")
	child.Env = newStepEnvironment(os.Environ(), map[string]string{
		"GO_WANT_VERIFICATION_DOUBLE_FORK": next,
	}).list
	if stage == "1" {
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err = child.Start(); err != nil {
		os.Exit(3)
	}
	if err = child.Process.Release(); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func TestUnixCleanupNeverSignalsARecycledPID(t *testing.T) {
	var signals []syscall.Signal
	err := signalUnixProcessIfCurrent(
		unixOwnedProcess{pid: 4242, start: "original"},
		syscall.SIGKILL,
		func(int) (string, error) { return "replacement", nil },
		func(_ int, signal syscall.Signal) error {
			signals = append(signals, signal)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(signals) != 0 {
		t.Fatalf("recycled pid received signals %v", signals)
	}
}

func TestUnixOwnershipRejectsPIDReusedAfterMarkerObservation(t *testing.T) {
	process, current, err := unixProcessFromSnapshot(
		4242,
		"original",
		func(int) (string, error) { return "replacement", nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if current || process != (unixOwnedProcess{}) {
		t.Fatalf("reused pid admitted as owned: current=%v process=%+v", current, process)
	}
}

func TestUnixDescendantObserverSharesSnapshotAcrossProjectWatches(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var reads atomic.Int32
	observer := newUnixDescendantObserver(time.Hour, func(ctx context.Context) (map[int]procmem.Identity, error) {
		reads.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			return map[int]procmem.Identity{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	first := observer.watch(unixOwnedProcess{pid: 100, start: "first"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("observer did not start its shared snapshot")
	}
	second := observer.watch(unixOwnedProcess{pid: 200, start: "second"})
	if got := reads.Load(); got != 1 {
		t.Fatalf("host snapshots = %d, want one shared in-flight read", got)
	}
	close(release)
	observer.unwatch(first)
	observer.unwatch(second)
}

func TestUnixDescendantObserverKeepsReparentedIdentity(t *testing.T) {
	owned := map[unixOwnedProcess]struct{}{{pid: 100, start: "root"}: {}}
	observeUnixDescendants(owned, map[int]procmem.Identity{
		100: {PPID: 1, Start: "root"},
		101: {PPID: 100, Start: "child"},
	})
	live := liveUnixDescendants(owned, map[int]procmem.Identity{
		101: {PPID: 1, Start: "child"},
	})
	if got, want := live, []unixOwnedProcess{{pid: 101, start: "child"}}; !slices.Equal(got, want) {
		t.Fatalf("reparented descendants = %+v, want %+v", got, want)
	}
	if got := liveUnixDescendants(owned, map[int]procmem.Identity{
		101: {PPID: 1, Start: "replacement"},
	}); len(got) != 0 {
		t.Fatalf("reused child pid remained owned: %+v", got)
	}
}

func TestUnixCleanupKillsAlreadyFrozenProcessesAfterAScanFailure(t *testing.T) {
	process := unixOwnedProcess{pid: 4242, start: "owned"}
	scanCalls := 0
	var signals []syscall.Signal
	hooks := unixProcessHooks{
		owned: func(context.Context) ([]unixOwnedProcess, error) {
			scanCalls++
			if scanCalls == 1 {
				return []unixOwnedProcess{process}, nil
			}
			return nil, errors.New("scan failed")
		},
		signal: func(got unixOwnedProcess, signal syscall.Signal) error {
			if got != process {
				t.Fatalf("signal process = %+v, want %+v", got, process)
			}
			signals = append(signals, signal)
			return nil
		},
		wait: time.Millisecond,
		poll: time.Microsecond,
	}
	survived, err := stopOwnedUnixProcesses(context.Background(), hooks)
	if !survived || err == nil || !strings.Contains(err.Error(), "scan failed") {
		t.Fatalf("cleanup = survived %v, error %v", survived, err)
	}
	if got, want := signals, []syscall.Signal{syscall.SIGSTOP, syscall.SIGKILL}; !slices.Equal(got, want) {
		t.Fatalf("signals = %v, want %v", got, want)
	}
}

func TestUnixCleanupUsesBoundedPostExitOwnershipSweeps(t *testing.T) {
	process := unixOwnedProcess{pid: 4242, start: "owned"}
	scanCalls := 0
	var signals []syscall.Signal
	hooks := unixProcessHooks{
		owned: func(context.Context) ([]unixOwnedProcess, error) {
			scanCalls++
			switch scanCalls {
			case 1, 2:
				return []unixOwnedProcess{process}, nil
			default:
				return nil, nil
			}
		},
		signal: func(got unixOwnedProcess, signal syscall.Signal) error {
			if got != process {
				t.Fatalf("signal process = %+v, want %+v", got, process)
			}
			signals = append(signals, signal)
			return nil
		},
		wait: time.Millisecond,
		poll: time.Microsecond,
	}
	survived, err := stopOwnedUnixProcesses(context.Background(), hooks)
	if !survived || err != nil {
		t.Fatalf("cleanup = survived %v, error %v", survived, err)
	}
	if scanCalls != 3 {
		t.Fatalf("ownership scans = %d, want 3", scanCalls)
	}
	if got, want := signals, []syscall.Signal{syscall.SIGSTOP, syscall.SIGKILL}; !slices.Equal(got, want) {
		t.Fatalf("signals = %v, want %v", got, want)
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
