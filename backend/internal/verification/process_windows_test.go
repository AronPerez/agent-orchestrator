//go:build windows

package verification

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestWindowsJobAccountingLayout(t *testing.T) {
	if got, want := unsafe.Offsetof(jobBasicAccountingInformation{}.ActiveProcesses), uintptr(40); got != want {
		t.Fatalf("ActiveProcesses offset = %d, want %d", got, want)
	}
}

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
	survivorFile := filepath.Join(workspace, "child-survived")
	env := newStepEnvironment(os.Environ(), map[string]string{
		"GO_WANT_VERIFICATION_WINDOWS_LAUNCHER": "1",
		"VERIFY_CHILD_READY":                    readyFile,
		"VERIFY_CHILD_SURVIVED":                 survivorFile,
	})
	command := syscall.EscapeArg(executable) + ` -test.run=^TestVerificationWindowsLauncherHelper$`

	err = runWorkspaceStep(context.Background(), workspace, command, env, io.Discard)
	if err == nil {
		t.Fatal("runWorkspaceStep() succeeded with a surviving Windows job child")
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("background child never started: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(survivorFile); !os.IsNotExist(err) {
		t.Fatalf("background child survived Windows job cleanup: %v", err)
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
	_ = child.Process.Release()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(os.Getenv("VERIFY_CHILD_READY")); err == nil {
			os.Exit(0)
		}
		if time.Now().After(deadline) {
			os.Exit(4)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestVerificationWindowsChildHelper(t *testing.T) {
	if os.Getenv("GO_WANT_VERIFICATION_WINDOWS_CHILD") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("VERIFY_CHILD_READY"), []byte("ready"), 0o600); err != nil {
		os.Exit(5)
	}
	time.Sleep(time.Second)
	if err := os.WriteFile(os.Getenv("VERIFY_CHILD_SURVIVED"), []byte("survived"), 0o600); err != nil {
		os.Exit(6)
	}
	os.Exit(0)
}
