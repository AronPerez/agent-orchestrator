package verification

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func runWorkspaceStep(ctx context.Context, workspacePath, command string, env map[string]string, output io.Writer) error {
	cmd := workspaceStepCommand(ctx, command)
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.Dir = workspacePath
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		// A successful shell left a descendant holding the output pipes. It is
		// not a successful verification: stop the tree before the project gate
		// is released, then preserve ErrWaitDelay for the caller.
		return errors.Join(err, aoprocess.KillProcessTree(cmd))
	}
	if err != nil && cmd.ProcessState != nil && cmd.ProcessState.Success() && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return nil
	}
	return err
}
