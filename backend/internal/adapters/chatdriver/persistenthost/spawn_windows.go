//go:build windows

package persistenthost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

func spawnDetached(ctx context.Context, cfg Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, args, err := hostExecutable(exe, hostArgs(cfg))
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...) //nolint:forbidigo // detached chat host has specialized lifetime and creation flags
	cmd.Dir = cfg.Workdir
	cmd.Env = envWithLaunchToken(cfg.Env, cfg.launchToken)
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn detached chat host: %w", err)
	}
	if err := publishLaunchReady(cfg.DataDir, cfg.SessionID, cfg.launchToken, cmd.Process.Pid); err != nil {
		if errors.Is(err, ErrOwnershipInconclusive) {
			// Readiness may already be visible. Preserve the detached host and its
			// launch record so recovery can fence the provider tree.
			releaseErr := cmd.Process.Release()
			return errors.Join(
				fmt.Errorf("publish detached chat host readiness: %w", err),
				releaseErr,
			)
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("publish detached chat host readiness: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		// Readiness already admitted the detached child, which may have started
		// its provider. Preserve the launch fence for recovery instead of turning
		// a process-handle failure into proof that no external effect occurred.
		return fmt.Errorf("%w: release detached chat host: %w", ErrOwnershipInconclusive, err)
	}
	return nil
}
