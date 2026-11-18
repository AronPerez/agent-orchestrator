package verification

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

var errLingeringProcessTree = errors.New("verification command left background processes running")

type cleanupUncertainError struct{ cause error }

func (e cleanupUncertainError) Error() string   { return e.cause.Error() }
func (e cleanupUncertainError) Unwrap() error   { return e.cause }
func (cleanupUncertainError) cleanupUncertain() {}

func markCleanupUncertain(err error) error {
	if err == nil || isCleanupUncertain(err) {
		return err
	}
	return cleanupUncertainError{cause: err}
}

func isCleanupUncertain(err error) bool {
	var uncertain interface{ cleanupUncertain() }
	return errors.As(err, &uncertain)
}

type workspaceProcessTree interface {
	Wait() (bool, error)
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
	survived, waitErr := tree.Wait()
	if platformCleanupUncertain(waitErr) {
		waitErr = markCleanupUncertain(waitErr)
	}
	if waitErr != nil && cmd.ProcessState != nil && cmd.ProcessState.Success() && ctx.Err() != nil && errors.Is(waitErr, ctx.Err()) {
		waitErr = nil
	}
	closeErr := tree.Close()
	if survived {
		waitErr = errors.Join(waitErr, errLingeringProcessTree)
	}
	return errors.Join(waitErr, closeErr)
}
