package verification

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

var errLingeringProcessTree = errors.New("verification command left background processes running")

type workspaceProcessTree interface {
	StopSurvivors() (bool, error)
	Close() error
}

func runWorkspaceStep(ctx context.Context, workspacePath, command string, env stepEnvironment, output io.Writer) error {
	cmd := workspaceStepCommand(ctx, command)
	cmd.Dir = workspacePath
	cmd.Env = append([]string(nil), env.list...)
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout, cmd.Stderr = output, output
	tree, err := startWorkspaceProcessTree(ctx, cmd)
	if err != nil {
		return err
	}
	waitErr := cmd.Wait()
	if waitErr != nil && cmd.ProcessState != nil && cmd.ProcessState.Success() && ctx.Err() != nil && errors.Is(waitErr, ctx.Err()) {
		waitErr = nil
	}
	survived, stopErr := tree.StopSurvivors()
	closeErr := tree.Close()
	if survived {
		waitErr = errors.Join(waitErr, errLingeringProcessTree)
	}
	return errors.Join(waitErr, stopErr, closeErr)
}
