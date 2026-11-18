//go:build darwin

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

func TestDarwinOwnershipExcludesDaemonDescriptorCollision(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close() //nolint:errcheck // test cleanup
	defer writer.Close() //nolint:errcheck // test cleanup
	descriptor, err := darwinDescriptorName(context.Background(), os.Getpid(), int(writer.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	start, err := currentVerificationProcessStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	watch := sharedUnixDescendantObserver.watch(unixOwnedProcess{pid: os.Getpid(), start: start})
	defer sharedUnixDescendantObserver.unwatch(watch)
	owned, err := ownedVerificationProcesses(context.Background(), unixProcessOwnership{
		token: "not-present", descriptor: descriptor, fd: int(writer.Fd()), daemonPID: os.Getpid(),
	}, watch)
	if err != nil {
		t.Fatal(err)
	}
	for _, process := range owned {
		if process.pid == os.Getpid() {
			t.Fatalf("daemon pid %d was treated as verification-owned", process.pid)
		}
	}
}

func TestWorkspaceStepStopsDetachedChildThatClosesInheritedDescriptors(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	pidFile := filepath.Join(workspace, "closed-descriptors.pid")
	env := newStepEnvironment(os.Environ(), map[string]string{
		"GO_WANT_VERIFICATION_CLOSE_FDS_CHILD": "1",
		"VERIFY_CLOSE_FDS_HELPER":              executable,
		"VERIFY_CHILD_PID":                     pidFile,
	})

	err = runWorkspaceStep(
		context.Background(),
		workspace,
		`"$VERIFY_CLOSE_FDS_HELPER" -test.run=^TestVerificationCloseFDsChildHelper$ >/dev/null 2>&1 & sleep 1`,
		env,
		io.Discard,
	)
	pid := readProcessID(t, pidFile)
	assertDarwinVerificationChildStopped(t, pid, err)
}

func TestVerificationCloseFDsChildHelper(t *testing.T) {
	if os.Getenv("GO_WANT_VERIFICATION_CLOSE_FDS_CHILD") != "1" {
		return
	}
	for fd := 3; fd < 512; fd++ {
		_ = unix.Close(fd)
	}
	if _, err := syscall.Setsid(); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(os.Getenv("VERIFY_CHILD_PID"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		os.Exit(3)
	}
	time.Sleep(time.Minute)
}

func assertDarwinVerificationChildStopped(t *testing.T, pid int, runErr error) {
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
