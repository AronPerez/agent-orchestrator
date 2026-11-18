//go:build linux

package verification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if handled, code, err := RunLinuxSupervisorIfRequested(os.Args); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestLinuxSupervisorStopsFastSanitizedDoubleFork(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	pidFile := filepath.Join(workspace, "sanitized-grandchild.pid")
	env := newStepEnvironment(os.Environ(), map[string]string{
		"VERIFY_SANITIZED_HELPER": executable,
		"VERIFY_SANITIZED_PID":    pidFile,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = runWorkspaceStep(
		ctx,
		workspace,
		`env -i GO_WANT_VERIFICATION_SANITIZED_FORK=1 VERIFY_SANITIZED_PID="$VERIFY_SANITIZED_PID" "$VERIFY_SANITIZED_HELPER" -test.run=^TestVerificationSanitizedForkHelper$ >/dev/null 2>&1`,
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
		t.Fatal("runWorkspaceStep() succeeded with a fast sanitized double-fork child")
	}
	deadline := time.Now().Add(2 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Fatalf("sanitized double-fork child %d survived verification (error %v)", pid, err)
	}
}

func TestLinuxSupervisorIgnoresAnOrphanThatExitedBeforeTheRoot(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := newStepEnvironment(os.Environ(), map[string]string{
		"GO_WANT_VERIFICATION_EXITED_ORPHAN": "root",
		"VERIFY_EXITED_ORPHAN_HELPER":        executable,
	})
	if err = runWorkspaceStep(
		context.Background(),
		t.TempDir(),
		`"$VERIFY_EXITED_ORPHAN_HELPER" -test.run=^TestVerificationExitedOrphanHelper$`,
		env,
		io.Discard,
	); err != nil {
		t.Fatalf("runWorkspaceStep() = %v, want an already-exited orphan to be ignored", err)
	}
}

func TestLinuxSupervisorPreservesExit124WithoutPoisoningTheProjectGate(t *testing.T) {
	runner := NewRunner()
	request := Request{
		ProjectID: "exit-124", WorkspacePath: t.TempDir(), Commands: []string{`exit 124`},
	}
	_, err := runner.Run(context.Background(), request)
	var stepErr *StepError
	var exitErr *exec.ExitError
	if !errors.As(err, &stepErr) || !errors.As(err, &exitErr) || exitErr.ExitCode() != 124 {
		t.Fatalf("first run error = %v, want ordinary exit status 124", err)
	}
	if isCleanupUncertain(err) {
		t.Fatalf("ordinary exit status 124 was marked cleanup-uncertain: %v", err)
	}

	request.Commands = []string{`:`}
	result, err := runner.Run(context.Background(), request)
	if err != nil || !result.Passed {
		t.Fatalf("second run = %+v, %v; project gate was poisoned by exit 124", result, err)
	}
}

func TestLinuxCleanupConfirmationIsIndependentOfCommandExitStatus(t *testing.T) {
	for _, tc := range []struct {
		name      string
		confirmed bool
		uncertain bool
	}{
		{name: "confirmed", confirmed: true},
		{name: "missing", uncertain: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if tc.confirmed {
				if _, err = writer.Write([]byte{linuxSupervisorCleanupComplete}); err != nil {
					t.Fatal(err)
				}
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			exitErr := exec.Command("sh", "-c", "exit 124").Run()
			got := finalizePlatformCleanupConfirmation(reader, exitErr)
			if err = reader.Close(); err != nil {
				t.Fatal(err)
			}
			if isCleanupUncertain(got) != tc.uncertain {
				t.Fatalf("finalized error = %v, cleanup uncertainty = %v, want %v", got, isCleanupUncertain(got), tc.uncertain)
			}
			var gotExit *exec.ExitError
			if !errors.As(got, &gotExit) || gotExit.ExitCode() != 124 {
				t.Fatalf("finalized error = %v, want exit status 124", got)
			}
		})
	}
}

func TestLinuxSupervisorDeathWithAnUnmarkedChildPoisonsTheProjectGate(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	pidFile := filepath.Join(workspace, "unmarked-grandchild.pid")
	runner := NewRunner()
	request := Request{
		ProjectID: "killed-supervisor", WorkspacePath: workspace,
		Commands: []string{
			`env -i GO_WANT_VERIFICATION_SANITIZED_FORK=1 VERIFY_SANITIZED_PID="$VERIFY_SANITIZED_PID" "$VERIFY_SANITIZED_HELPER" -test.run=^TestVerificationSanitizedForkHelper$ >/dev/null 2>&1; while [ ! -s "$VERIFY_SANITIZED_PID" ]; do sleep 0.01; done; kill -KILL "$PPID"`,
		},
		Env: map[string]string{
			"VERIFY_SANITIZED_HELPER": executable,
			"VERIFY_SANITIZED_PID":    pidFile,
		},
	}
	_, err = runner.Run(context.Background(), request)
	pid := readProcessID(t, pidFile)
	t.Cleanup(func() {
		if processAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	if !isCleanupUncertain(err) {
		t.Fatalf("supervisor death error = %v, want cleanup uncertainty", err)
	}

	request.Commands = []string{`:`}
	if _, err = runner.Run(context.Background(), request); !errors.Is(err, errVerificationProjectUnsafe) {
		t.Fatalf("second run error = %v, want poisoned project gate", err)
	}
}

func TestVerificationSanitizedForkHelper(t *testing.T) {
	stage := os.Getenv("GO_WANT_VERIFICATION_SANITIZED_FORK")
	if stage == "" {
		return
	}
	if stage == "3" {
		if err := os.WriteFile(os.Getenv("VERIFY_SANITIZED_PID"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
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
	child := exec.Command(executable, "-test.run=^TestVerificationSanitizedForkHelper$")
	child.Env = []string{
		"GO_WANT_VERIFICATION_SANITIZED_FORK=" + next,
		"VERIFY_SANITIZED_PID=" + os.Getenv("VERIFY_SANITIZED_PID"),
	}
	if stage == "1" {
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err = child.Start(); err != nil {
		os.Exit(3)
	}
	if err = child.Process.Release(); err != nil {
		os.Exit(4)
	}
	if stage == "1" {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if _, statErr := os.Stat(os.Getenv("VERIFY_SANITIZED_PID")); statErr == nil {
				os.Exit(0)
			}
			time.Sleep(time.Millisecond)
		}
		os.Exit(6)
	}
	os.Exit(0)
}

func TestVerificationExitedOrphanHelper(t *testing.T) {
	switch os.Getenv("GO_WANT_VERIFICATION_EXITED_ORPHAN") {
	case "":
		return
	case "child":
		os.Exit(0)
	case "root":
		executable, err := os.Executable()
		if err != nil {
			os.Exit(2)
		}
		child := exec.Command(executable, "-test.run=^TestVerificationExitedOrphanHelper$")
		child.Env = []string{"GO_WANT_VERIFICATION_EXITED_ORPHAN=child"}
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err = child.Start(); err != nil {
			os.Exit(3)
		}
		pid := child.Process.Pid
		if err = child.Process.Release(); err != nil {
			os.Exit(4)
		}
		var info unix.Siginfo
		if err = unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil); err != nil {
			os.Exit(6)
		}
		os.Exit(0)
	default:
		os.Exit(5)
	}
}
