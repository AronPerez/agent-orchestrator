//go:build !windows

package persistenthost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
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
	cmd := exec.Command(exe, args...) //nolint:forbidigo // detached chat host has specialized lifetime and session ownership
	cmd.Dir = cfg.Workdir
	cmd.Env = envWithLaunchToken(cfg.Env, cfg.launchToken)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn detached chat host: %w", err)
	}
	if err := publishLaunchReady(cfg.DataDir, cfg.SessionID, cfg.launchToken, cmd.Process.Pid); err != nil {
		if errors.Is(err, ErrOwnershipInconclusive) {
			// Readiness may already be visible. Preserve and reap the detached host;
			// its durable launch record lets recovery fence any surviving provider.
			go func() { _ = cmd.Wait() }()
			return fmt.Errorf("publish detached chat host readiness: %w", err)
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("publish detached chat host readiness: %w", err)
	}
	// Setsid detaches the host from our session, but it remains our child until
	// Wait reaps it. Release alone leaves a zombie after each hibernation.
	go func() { _ = cmd.Wait() }()
	return nil
}
