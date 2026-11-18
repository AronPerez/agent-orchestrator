//go:build windows

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

	"golang.org/x/sys/windows"
)

func TestWorkspaceStepAllowsACleanWindowsCommand(t *testing.T) {
	env := newStepEnvironment(os.Environ(), nil)
	if err := runWorkspaceStep(context.Background(), t.TempDir(), `exit /b 0`, env, io.Discard); err != nil {
		t.Fatalf("clean Windows command failed: %v", err)
	}
}

func TestWorkspaceStepStopsAWindowsJobChildAfterTheShellExits(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	readyFile := filepath.Join(workspace, "child-ready")
	pidFile := filepath.Join(workspace, "child.pid")
	env := newStepEnvironment(os.Environ(), map[string]string{
		"GO_WANT_VERIFICATION_WINDOWS_LAUNCHER": "1",
		"VERIFY_CHILD_READY":                    readyFile,
		"VERIFY_CHILD_PID":                      pidFile,
	})
	command := syscall.EscapeArg(executable) + ` -test.run=^TestVerificationWindowsLauncherHelper$`

	err = runWorkspaceStep(context.Background(), workspace, command, env, io.Discard)
	if err == nil {
		t.Fatal("runWorkspaceStep() succeeded with a surviving Windows job child")
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("background child never started: %v", err)
	}
	assertWindowsProcessStopped(t, awaitWindowsChildPID(t, pidFile))
}

func TestWindowsJobOwnsGrandchildBeforeShellExit(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	readyFile := filepath.Join(workspace, "child-ready")
	pidFile := filepath.Join(workspace, "child.pid")
	releaseFile := filepath.Join(workspace, "release-launcher")
	env := newStepEnvironment(os.Environ(), map[string]string{
		"GO_WANT_VERIFICATION_WINDOWS_LAUNCHER": "1",
		"VERIFY_CHILD_READY":                    readyFile,
		"VERIFY_CHILD_PID":                      pidFile,
		"VERIFY_LAUNCHER_RELEASE":               releaseFile,
	})
	command := syscall.EscapeArg(executable) + ` -test.run=^TestVerificationWindowsLauncherHelper$`
	cmd := workspaceStepCommand(context.Background(), command)
	cmd.Dir = workspace
	cmd.Env = append([]string(nil), env.list...)
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	treeValue, err := startWorkspaceProcessTree(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	tree := treeValue.(*windowsWorkspaceProcessTree)
	waited := false
	defer func() {
		if !waited {
			_ = tree.terminate()
			_, _ = tree.Wait()
		}
		_ = tree.Close()
	}()
	childPID := awaitWindowsChildPID(t, pidFile)
	child, err := windows.OpenProcess(
		windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(childPID),
	)
	if err != nil {
		t.Fatalf("open child %d: %v", childPID, err)
	}
	defer windows.CloseHandle(child) //nolint:errcheck // test-only OS handle cleanup
	inJob, membershipErr := isProcessInJob(child, tree.job)
	if err = os.WriteFile(releaseFile, []byte("release"), 0o600); err != nil {
		_ = tree.terminate()
	}
	survived, waitErr := tree.Wait()
	waited = true
	closeErr := tree.Close()
	status, statusErr := windows.WaitForSingleObject(child, 0)
	if err != nil {
		t.Fatalf("release launcher: %v", err)
	}
	if membershipErr != nil {
		t.Fatalf("check child %d job membership: %v", childPID, membershipErr)
	}
	if !inJob {
		t.Fatalf("child %d was not associated with the verification job", childPID)
	}
	if !survived {
		t.Fatalf("verification job did not report child %d as surviving the shell", childPID)
	}
	if waitErr != nil || closeErr != nil {
		t.Fatalf("clean up child %d: wait=%v close=%v", childPID, waitErr, closeErr)
	}
	if statusErr != nil || status != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(child, 1)
		t.Fatalf("child %d survived job cleanup: status=%d error=%v", childPID, status, statusErr)
	}
}

func TestVerificationWindowsLauncherHelper(t *testing.T) {
	if os.Getenv("GO_WANT_VERIFICATION_WINDOWS_LAUNCHER") != "1" {
		return
	}
	executable, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	child := exec.Command(executable, "-test.run=^TestVerificationWindowsChildHelper$")
	child.Env = append(os.Environ(), "GO_WANT_VERIFICATION_WINDOWS_CHILD=1")
	if err = child.Start(); err != nil {
		os.Exit(3)
	}
	if pidFile := os.Getenv("VERIFY_CHILD_PID"); pidFile != "" {
		if err = os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			os.Exit(7)
		}
	}
	_ = child.Process.Release()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(os.Getenv("VERIFY_CHILD_READY")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(4)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if releaseFile := os.Getenv("VERIFY_LAUNCHER_RELEASE"); releaseFile != "" {
		deadline = time.Now().Add(5 * time.Second)
		for {
			if _, err = os.Stat(releaseFile); err == nil {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(8)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	os.Exit(0)
}

func TestVerificationWindowsChildHelper(t *testing.T) {
	if os.Getenv("GO_WANT_VERIFICATION_WINDOWS_CHILD") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("VERIFY_CHILD_READY"), []byte("ready"), 0o600); err != nil {
		os.Exit(5)
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func awaitWindowsChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child PID was not published to %s", path)
	return 0
}

func assertWindowsProcessStopped(t *testing.T, pid int) {
	t.Helper()
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return
	}
	if err != nil {
		t.Fatalf("open child %d after cleanup: %v", pid, err)
	}
	defer windows.CloseHandle(process) //nolint:errcheck // test-only OS handle cleanup
	status, waitErr := windows.WaitForSingleObject(process, 0)
	if waitErr != nil || status != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(process, 1)
		t.Fatalf("child %d survived job cleanup: status=%d error=%v", pid, status, waitErr)
	}
}
