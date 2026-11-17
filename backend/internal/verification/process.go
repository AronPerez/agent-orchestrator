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
	if errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil {
		return nil
	}
	if err != nil && cmd.ProcessState != nil && cmd.ProcessState.Success() && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return nil
	}
	return err
}
