//go:build !windows

package verification

import (
	"context"
	"errors"
	"os/exec"
	"syscall"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

type unixWorkspaceProcessTree struct {
	pgid int
}

func startWorkspaceProcessTree(_ context.Context, cmd *exec.Cmd) (workspaceProcessTree, error) {
	aoprocess.ConfigureTreeCancellation(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &unixWorkspaceProcessTree{pgid: cmd.Process.Pid}, nil
}

func (t *unixWorkspaceProcessTree) StopSurvivors() (bool, error) {
	err := syscall.Kill(-t.pgid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false, err
	}
	err = syscall.Kill(-t.pgid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		err = nil
	}
	return true, err
}

func (*unixWorkspaceProcessTree) Close() error { return nil }
