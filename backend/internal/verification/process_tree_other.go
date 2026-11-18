//go:build !windows

package verification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	unixProcessStopWait          = 2 * time.Second
	unixProcessStopPoll          = 20 * time.Millisecond
	maxUnixFreezeRounds          = 32
	verificationOwnerEnvironment = "AO_VERIFICATION_OWNER"
)

var errUnixProcessGone = errors.New("verification process is gone")

var verificationProcessStartForWatchFn = verificationProcessStartForWatch

type unixOwnedProcess struct {
	pid   int
	start string
}

type unixProcessOwnership struct {
	token      string
	descriptor string
	fd         int
	daemonPID  int
}

type unixProcessHooks struct {
	owned  func(context.Context) ([]unixOwnedProcess, error)
	signal func(unixOwnedProcess, syscall.Signal) error
	wait   time.Duration
	poll   time.Duration
}

func defaultUnixProcessHooks(ownership unixProcessOwnership, descendants *unixDescendantWatch) unixProcessHooks {
	return unixProcessHooks{
		owned: func(ctx context.Context) ([]unixOwnedProcess, error) {
			return ownedVerificationProcesses(ctx, ownership, descendants)
		},
		signal: signalOwnedVerificationProcess,
		wait:   unixProcessStopWait,
		poll:   unixProcessStopPoll,
	}
}

type unixWorkspaceProcessTree struct {
	cmd            *exec.Cmd
	descendants    *unixDescendantWatch
	hooks          unixProcessHooks
	closeOwnership func() error
	closeOnce      sync.Once
	closeErr       error
}

func startWorkspaceProcessTree(ctx context.Context, cmd *exec.Cmd) (workspaceProcessTree, error) {
	ownership, closeOwnership, err := prepareUnixProcessOwnership(ctx, cmd)
	if err != nil {
		return nil, err
	}
	gateReader, gateWriter, err := prepareUnixProcessStartGate(cmd)
	if err != nil {
		return nil, errors.Join(err, closeOwnership())
	}
	closeGate := func() error { return errors.Join(gateReader.Close(), gateWriter.Close()) }
	configureUnixWorkspaceProcess(cmd)
	if err = cmd.Start(); err != nil {
		return nil, errors.Join(err, closeGate(), closeOwnership())
	}
	_ = gateReader.Close()
	start, err := verificationProcessStartForWatchFn(cmd.Process.Pid)
	if err != nil {
		_ = gateWriter.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.Join(
			fmt.Errorf("identify verification process: %w", err),
			closeOwnership(),
		)
	}
	descendants := sharedUnixDescendantObserver.watch(unixOwnedProcess{pid: cmd.Process.Pid, start: start})
	if err = ctx.Err(); err == nil {
		_, err = gateWriter.Write([]byte{'\n'})
	}
	_ = gateWriter.Close()
	if err != nil {
		sharedUnixDescendantObserver.unwatch(descendants)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.Join(
			fmt.Errorf("release verification process: %w", err),
			closeOwnership(),
		)
	}
	return &unixWorkspaceProcessTree{
		cmd: cmd, descendants: descendants,
		hooks: defaultUnixProcessHooks(ownership, descendants), closeOwnership: closeOwnership,
	}, nil
}

func prepareUnixProcessStartGate(cmd *exec.Cmd) (*os.File, *os.File, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("create verification start gate: %w", err)
	}
	fd := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, reader)
	if err = configureUnixProcessStartGate(cmd, fd); err != nil {
		return nil, nil, errors.Join(err, reader.Close(), writer.Close())
	}
	return reader, writer, nil
}

func (t *unixWorkspaceProcessTree) Wait() (bool, error) {
	waitErr := t.cmd.Wait()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), t.hooks.wait+time.Second)
	survived, stopErr := stopOwnedUnixProcesses(cleanupCtx, t.hooks)
	cancel()
	return survived, errors.Join(waitErr, stopErr)
}

func (t *unixWorkspaceProcessTree) Close() error {
	t.closeOnce.Do(func() {
		if t.descendants != nil {
			sharedUnixDescendantObserver.unwatch(t.descendants)
		}
		if t.closeOwnership != nil {
			t.closeErr = t.closeOwnership()
		}
	})
	return t.closeErr
}

// stopOwnedUnixProcesses freezes every process still carrying owner, rescans
// until none can fork, then kills and confirms them gone. A shared descendant
// observer and independent environment/descriptor markers retain ownership
// across normal reparenting and environment or descriptor sanitization.
func stopOwnedUnixProcesses(
	ctx context.Context,
	hooks unixProcessHooks,
) (survived bool, resultErr error) {
	frozen := make(map[unixOwnedProcess]struct{})
	cleanupComplete := false
	defer func() {
		if cleanupComplete {
			return
		}
		// A later scan may fail after an earlier one stopped a process. Never
		// strand it in SIGSTOP: first make a final identity-checked kill attempt;
		// if that cannot be sent, resume the same identity before returning.
		for process := range frozen {
			if err := hooks.signal(process, syscall.SIGKILL); err != nil {
				resumeErr := hooks.signal(process, syscall.SIGCONT)
				resultErr = errors.Join(resultErr,
					fmt.Errorf("finalize verification process %d: %w", process.pid, err),
					resumeErr,
				)
			}
		}
	}()

	settled := false
	for range maxUnixFreezeRounds {
		processes, err := hooks.owned(ctx)
		if err != nil {
			return survived, fmt.Errorf("find verification processes: %w", err)
		}
		froze := false
		for _, process := range processes {
			survived = true
			if _, ok := frozen[process]; ok {
				continue
			}
			frozen[process] = struct{}{}
			froze = true
			if err := hooks.signal(process, syscall.SIGSTOP); err != nil {
				return survived, fmt.Errorf("freeze verification process %d: %w", process.pid, err)
			}
		}
		if !froze {
			settled = true
			break
		}
	}
	if !settled {
		return survived, errors.New("verification process set kept growing while being frozen")
	}

	var signalErr error
	for process := range frozen {
		if err := hooks.signal(process, syscall.SIGKILL); err != nil {
			signalErr = errors.Join(signalErr, fmt.Errorf("kill verification process %d: %w", process.pid, err))
		}
	}
	deadline := time.Now().Add(hooks.wait)
	for {
		processes, err := hooks.owned(ctx)
		if err != nil {
			return survived, errors.Join(signalErr, fmt.Errorf("confirm verification process exit: %w", err))
		}
		if len(processes) == 0 {
			cleanupComplete = true
			return survived, signalErr
		}
		for _, process := range processes {
			survived = true
			if _, ok := frozen[process]; !ok {
				frozen[process] = struct{}{}
				if err := hooks.signal(process, syscall.SIGSTOP); err != nil {
					signalErr = errors.Join(signalErr, fmt.Errorf("freeze late verification process %d: %w", process.pid, err))
				}
			}
			if err := hooks.signal(process, syscall.SIGKILL); err != nil {
				signalErr = errors.Join(signalErr, fmt.Errorf("kill verification process %d: %w", process.pid, err))
			}
		}
		if !time.Now().Before(deadline) {
			pids := make([]int, 0, len(processes))
			for _, process := range processes {
				pids = append(pids, process.pid)
			}
			return survived, errors.Join(signalErr, fmt.Errorf("verification processes not confirmed gone: %v", pids))
		}
		timer := time.NewTimer(hooks.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return survived, errors.Join(signalErr, ctx.Err())
		case <-timer.C:
		}
	}
}

func signalUnixProcessIfCurrent(
	process unixOwnedProcess,
	signal syscall.Signal,
	currentStart func(int) (string, error),
	kill func(int, syscall.Signal) error,
) error {
	start, err := currentStart(process.pid)
	if errors.Is(err, errUnixProcessGone) {
		return nil
	}
	if err != nil {
		return err
	}
	if start != process.start {
		return nil
	}
	if err := kill(process.pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func unixProcessFromSnapshot(
	pid int,
	start string,
	currentStart func(int) (string, error),
) (unixOwnedProcess, bool, error) {
	process := unixOwnedProcess{pid: pid, start: start}
	current, err := currentStart(pid)
	if errors.Is(err, errUnixProcessGone) {
		return unixOwnedProcess{}, false, nil
	}
	if err != nil {
		return unixOwnedProcess{}, false, err
	}
	if current != process.start {
		return unixOwnedProcess{}, false, nil
	}
	return process, true, nil
}
