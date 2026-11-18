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
	waitErr = normalizeWorkspaceWaitError(
		waitErr,
		cmd.ProcessState != nil && cmd.ProcessState.Success(),
		ctx.Err(),
	)
	closeErr := tree.Close()
	if survived {
		waitErr = errors.Join(waitErr, errLingeringProcessTree)
	}
	return errors.Join(waitErr, closeErr)
}

func normalizeWorkspaceWaitError(waitErr error, processSucceeded bool, contextErr error) error {
	if waitErr == nil || !processSucceeded || contextErr == nil || !errors.Is(waitErr, contextErr) {
		return waitErr
	}
	return errors.Join(cleanupUncertaintyComponents(waitErr)...)
}

func cleanupUncertaintyComponents(err error) []error {
	if err == nil {
		return nil
	}
	if _, ok := err.(interface{ cleanupUncertain() }); ok {
		return []error{err}
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var components []error
		for _, child := range joined.Unwrap() {
			components = append(components, cleanupUncertaintyComponents(child)...)
		}
		return components
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return cleanupUncertaintyComponents(wrapped.Unwrap())
	}
	return nil
}
