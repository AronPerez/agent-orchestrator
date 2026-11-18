//go:build linux

package verification

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func configureUnixWorkspaceProcess(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := cmd.Process.Signal(syscall.SIGTERM)
		if errors.Is(err, os.ErrProcessDone) {
			return os.ErrProcessDone
		}
		return err
	}
	if cmd.WaitDelay < linuxSupervisorStopWait+time.Second {
		cmd.WaitDelay = linuxSupervisorStopWait + time.Second
	}
}

func configureUnixProcessStartGate(cmd *exec.Cmd, gateFD, cleanupFD int) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	originalPath := cmd.Path
	originalArgs := append([]string(nil), cmd.Args...)
	passthroughFiles := gateFD - 3
	cmd.Path = executable
	cmd.Args = []string{
		executable,
		linuxSupervisorArgument,
		strconv.Itoa(gateFD),
		strconv.Itoa(passthroughFiles),
		strconv.Itoa(cleanupFD),
		originalPath,
	}
	cmd.Args = append(cmd.Args, originalArgs...)
	return nil
}
