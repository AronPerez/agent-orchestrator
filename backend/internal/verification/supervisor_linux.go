//go:build linux

package verification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

const (
	linuxSupervisorArgument    = "__ao_internal_verification_supervisor_v1"
	linuxSupervisorFailureCode = 125
	linuxSupervisorStopWait    = 5 * time.Second
)

// RunLinuxSupervisorIfRequested handles AO's private verification-supervisor
// re-exec mode before normal CLI or test-binary startup.
func RunLinuxSupervisorIfRequested(args []string) (bool, int, error) {
	if len(args) < 2 || args[1] != linuxSupervisorArgument {
		return false, 0, nil
	}
	if len(args) < 6 {
		return true, linuxSupervisorFailureCode, errors.New("invalid verification supervisor arguments")
	}
	gateFD, err := strconv.Atoi(args[2])
	if err != nil || gateFD < 3 {
		return true, linuxSupervisorFailureCode, errors.New("invalid verification supervisor gate descriptor")
	}
	passthroughFiles, err := strconv.Atoi(args[3])
	if err != nil || passthroughFiles < 0 || passthroughFiles > 64 || gateFD != 3+passthroughFiles {
		return true, linuxSupervisorFailureCode, errors.New("invalid verification supervisor descriptor layout")
	}
	code, runErr := runLinuxSupervisor(gateFD, passthroughFiles, args[4], args[5:])
	return true, code, runErr
}

func runLinuxSupervisor(gateFD, passthroughFiles int, path string, args []string) (int, error) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return linuxSupervisorFailureCode, fmt.Errorf("enable verification subreaper: %w", err)
	}
	gate := os.NewFile(uintptr(gateFD), "verification-start-gate")
	if gate == nil {
		return linuxSupervisorFailureCode, errors.New("open verification start gate")
	}
	var release [1]byte
	_, readErr := gate.Read(release[:])
	closeErr := gate.Close()
	if readErr != nil || closeErr != nil {
		return linuxSupervisorFailureCode, fmt.Errorf(
			"wait for verification start gate: %w",
			errors.Join(readErr, closeErr),
		)
	}

	root := &exec.Cmd{
		Path:  path,
		Args:  append([]string(nil), args...),
		Env:   os.Environ(),
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		SysProcAttr: &syscall.SysProcAttr{Setpgid: true},
	}
	for fd := 3; fd < 3+passthroughFiles; fd++ {
		root.ExtraFiles = append(root.ExtraFiles, os.NewFile(uintptr(fd), "verification-owned-descriptor"))
	}
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(interrupts)
	startErr := root.Start()
	for _, file := range root.ExtraFiles {
		_ = file.Close()
	}
	if startErr != nil {
		return linuxSupervisorFailureCode, startErr
	}

	rootDone := make(chan error, 1)
	go func() { rootDone <- root.Wait() }()
	var rootErr error
	select {
	case rootErr = <-rootDone:
	case <-interrupts:
		_ = syscall.Kill(-root.Process.Pid, syscall.SIGKILL)
		rootErr = <-rootDone
	}
	survived, cleanupErr := stopLinuxSupervisorChildren(linuxSupervisorStopWait)
	code := linuxProcessExitCode(rootErr)
	if survived || cleanupErr != nil {
		code = linuxSupervisorFailureCode
	}
	if survived {
		cleanupErr = errors.Join(errLingeringProcessTree, cleanupErr)
	}
	return code, cleanupErr
}

func stopLinuxSupervisorChildren(timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	survived := false
	for {
		for {
			var status unix.WaitStatus
			pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			switch {
			case pid > 0:
				continue
			case errors.Is(err, unix.EINTR):
				continue
			case errors.Is(err, unix.ECHILD):
				return survived, nil
			case err != nil:
				return survived, fmt.Errorf("reap verification child: %w", err)
			}
			break
		}

		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		identities, err := procmem.Identities(ctx)
		cancel()
		if err != nil {
			return survived, fmt.Errorf("find adopted verification children: %w", err)
		}
		found := false
		for pid, identity := range identities {
			if identity.PPID != os.Getpid() || !identity.Known() {
				continue
			}
			found = true
			survived = true
			process := unixOwnedProcess{pid: pid, start: identity.Start}
			if err := signalUnixProcessIfCurrent(process, syscall.SIGKILL, currentVerificationProcessStart, syscall.Kill); err != nil {
				return survived, fmt.Errorf("kill adopted verification child %d: %w", pid, err)
			}
		}
		if !time.Now().Before(deadline) {
			return survived, errors.New("adopted verification children did not stop before the deadline")
		}
		if !found {
			// wait4 reported a live child that raced with the process snapshot.
			// Retry until it is visible, exits, or the bounded cleanup expires.
			time.Sleep(time.Millisecond)
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func linuxProcessExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
		return exitErr.ExitCode()
	}
	return 1
}
