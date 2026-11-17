//go:build !windows

package verification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
)

const (
	unixProcessPoll       = 50 * time.Millisecond
	unixProcessStopWait   = 2 * time.Second
	maxUnixFreezeRounds   = 32
	unixRootCaptureWindow = time.Second
)

type unixCapturedProcess struct {
	pid       int
	start     string
	startedAt time.Time
}

type unixProcessHooks struct {
	identities func(context.Context) (map[int]procmem.Identity, error)
	signal     func(int, syscall.Signal) error
	wait       time.Duration
	poll       time.Duration
}

func defaultUnixProcessHooks() unixProcessHooks {
	return unixProcessHooks{
		identities: procmem.Identities,
		signal:     syscall.Kill,
		wait:       unixProcessStopWait,
		poll:       unixProcessPoll,
	}
}

type unixWorkspaceProcessTree struct {
	ctx     context.Context
	cmd     *exec.Cmd
	root    unixCapturedProcess
	hooks   unixProcessHooks
	status  *os.File
	release *os.File

	mu          sync.Mutex
	captured    map[int]unixCapturedProcess
	monitorErr  error
	monitorEnd  context.CancelFunc
	monitorDone chan struct{}
	stopMonitor sync.Once
}

func startWorkspaceProcessTree(ctx context.Context, cmd *exec.Cmd) (workspaceProcessTree, error) {
	hooks := defaultUnixProcessHooks()
	startReader, startWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create verification start gate: %w", err)
	}
	statusReader, statusWriter, err := os.Pipe()
	if err != nil {
		_ = startReader.Close()
		_ = startWriter.Close()
		return nil, fmt.Errorf("create verification status pipe: %w", err)
	}
	releaseReader, releaseWriter, err := os.Pipe()
	if err != nil {
		_ = startReader.Close()
		_ = startWriter.Close()
		_ = statusReader.Close()
		_ = statusWriter.Close()
		return nil, fmt.Errorf("create verification release gate: %w", err)
	}
	closeChildFiles := func() {
		_ = startReader.Close()
		_ = statusWriter.Close()
		_ = releaseReader.Close()
	}
	closeParentFiles := func() {
		_ = startWriter.Close()
		_ = statusReader.Close()
		_ = releaseWriter.Close()
	}

	aoprocess.ConfigureTreeCancellation(cmd)
	// ExtraFiles become descriptors 3, 4, and 5 in unixWorkspaceWrapper.
	cmd.ExtraFiles = []*os.File{startReader, statusWriter, releaseReader}
	if err = cmd.Start(); err != nil {
		closeChildFiles()
		closeParentFiles()
		return nil, err
	}
	closeChildFiles()

	root, err := captureUnixRoot(ctx, cmd.Process.Pid, hooks)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		closeParentFiles()
		return nil, fmt.Errorf("capture verification process identity: %w", err)
	}
	monitorCtx, monitorEnd := context.WithCancel(context.Background())
	tree := &unixWorkspaceProcessTree{
		ctx: ctx, cmd: cmd, root: root, hooks: hooks,
		status: statusReader, release: releaseWriter,
		captured:   map[int]unixCapturedProcess{root.pid: root},
		monitorEnd: monitorEnd, monitorDone: make(chan struct{}),
	}
	go tree.monitor(monitorCtx)
	if _, err = startWriter.WriteString("start\n"); err != nil {
		tree.endMonitor()
		_ = cmd.Process.Kill()
		waitErr := cmd.Wait()
		_ = startWriter.Close()
		closeErr := tree.Close()
		return nil, errors.Join(fmt.Errorf("release verification start gate: %w", err), waitErr, closeErr)
	}
	// The byte already released the child. A close error cannot revoke that
	// handoff, so normal tree cleanup remains the only safe ownership path.
	_ = startWriter.Close()
	return tree, nil
}

func captureUnixRoot(ctx context.Context, pid int, hooks unixProcessHooks) (unixCapturedProcess, error) {
	deadline := time.Now().Add(unixRootCaptureWindow)
	for {
		identities, err := hooks.identities(ctx)
		if err != nil {
			return unixCapturedProcess{}, err
		}
		if identity, ok := identities[pid]; ok {
			if !identity.Known() {
				return unixCapturedProcess{}, fmt.Errorf("identity of process %d is unknown", pid)
			}
			return unixCapturedProcess{pid: pid, start: identity.Start, startedAt: identity.StartedAt}, nil
		}
		if !time.Now().Before(deadline) {
			return unixCapturedProcess{}, fmt.Errorf("process %d did not appear", pid)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return unixCapturedProcess{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (t *unixWorkspaceProcessTree) monitor(ctx context.Context) {
	defer close(t.monitorDone)
	ticker := time.NewTicker(t.hooks.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			identities, err := t.hooks.identities(ctx)
			t.mu.Lock()
			if err != nil {
				if ctx.Err() != nil {
					t.mu.Unlock()
					return
				}
				if t.monitorErr == nil {
					t.monitorErr = err
				}
				t.mu.Unlock()
				continue
			}
			tree, extendErr := extendUnixTree(capturedUnixProcesses(t.captured), identities)
			if extendErr != nil && t.monitorErr == nil {
				t.monitorErr = extendErr
			}
			for _, process := range tree {
				t.captured[process.pid] = process
			}
			t.mu.Unlock()
		}
	}
}

func (t *unixWorkspaceProcessTree) endMonitor() {
	t.stopMonitor.Do(func() {
		t.monitorEnd()
		<-t.monitorDone
	})
}

type unixStatusRead struct {
	n   int
	err error
}

func (t *unixWorkspaceProcessTree) Wait() (bool, error) {
	statusDone := make(chan unixStatusRead, 1)
	go func() {
		var marker [1]byte
		n, err := t.status.Read(marker[:])
		statusDone <- unixStatusRead{n: n, err: err}
	}()
	waitDone := make(chan error, 1)
	go func() { waitDone <- t.cmd.Wait() }()
	var status unixStatusRead
	var waitErr error
	waited := false
	select {
	case status = <-statusDone:
	case waitErr = <-waitDone:
		waited = true
		_ = t.status.Close()
		status = <-statusDone
	case <-t.ctx.Done():
		_ = t.status.Close()
		status = <-statusDone
	}

	t.endMonitor()
	t.mu.Lock()
	captured := capturedUnixProcesses(t.captured)
	monitorErr := t.monitorErr
	t.mu.Unlock()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), t.hooks.wait+time.Second)
	survived, stopErr := stopCapturedUnixProcesses(cleanupCtx, t.root, captured, t.hooks)
	cancel()

	var releaseErr error
	if _, err := t.release.WriteString("release\n"); err != nil && status.n > 0 {
		releaseErr = fmt.Errorf("release verification process anchor: %w", err)
	}
	if err := t.release.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		releaseErr = errors.Join(releaseErr, err)
	}
	if !waited {
		waitErr = <-waitDone
	}
	if status.n == 0 && status.err != nil && !errors.Is(status.err, io.EOF) && t.ctx.Err() == nil {
		waitErr = errors.Join(waitErr, fmt.Errorf("read verification completion marker: %w", status.err))
	}
	if monitorErr != nil {
		stopErr = errors.Join(stopErr, fmt.Errorf("monitor verification processes: %w", monitorErr))
	}
	return survived, errors.Join(waitErr, stopErr, releaseErr)
}

func (t *unixWorkspaceProcessTree) Close() error {
	t.endMonitor()
	var closeErr error
	for _, file := range []*os.File{t.status, t.release} {
		if file == nil {
			continue
		}
		if err := file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}

func capturedUnixProcesses(processes map[int]unixCapturedProcess) []unixCapturedProcess {
	out := make([]unixCapturedProcess, 0, len(processes))
	for _, process := range processes {
		out = append(out, process)
	}
	return out
}

// stopCapturedUnixProcesses freezes every still-matching descendant, rescans
// until none can fork, then kills and confirms them gone. Signals always target
// an exact pid/start identity. The root is left alive as the ownership anchor
// until its caller releases and reaps it after this function returns.
func stopCapturedUnixProcesses(
	ctx context.Context,
	root unixCapturedProcess,
	tree []unixCapturedProcess,
	hooks unixProcessHooks,
) (bool, error) {
	tree = mergeUnixCaptured(tree, root)
	frozen := make(map[unixCapturedProcess]bool)
	survived := false
	var signalErr error
	settled := false
	for range maxUnixFreezeRounds {
		identities, err := hooks.identities(ctx)
		if err != nil {
			return survived, errors.Join(signalErr, fmt.Errorf("read verification process identities: %w", err))
		}
		tree, err = extendUnixTree(tree, identities)
		if err != nil {
			return survived, errors.Join(signalErr, err)
		}
		froze := false
		for _, process := range tree {
			if process.pid == root.pid {
				continue
			}
			current, ok := identities[process.pid]
			if !ok || current.Known() && current.Start != process.start {
				continue
			}
			if !current.Known() {
				return survived, errors.Join(signalErr, fmt.Errorf("identity of verification process %d is unknown", process.pid))
			}
			survived = true
			if frozen[process] {
				continue
			}
			frozen[process] = true
			froze = true
			if err := hooks.signal(process.pid, syscall.SIGSTOP); err != nil && !errors.Is(err, syscall.ESRCH) {
				signalErr = errors.Join(signalErr, fmt.Errorf("freeze verification process %d: %w", process.pid, err))
			}
		}
		if !froze {
			settled = true
			break
		}
	}
	if !settled {
		return survived, errors.Join(signalErr, errors.New("verification process tree kept growing while being frozen"))
	}

	deadline := time.Now().Add(hooks.wait)
	killed := make(map[unixCapturedProcess]bool)
	for {
		identities, err := hooks.identities(ctx)
		if err != nil {
			return survived, errors.Join(signalErr, fmt.Errorf("read verification process identities: %w", err))
		}
		tree, err = extendUnixTree(tree, identities)
		if err != nil {
			return survived, errors.Join(signalErr, err)
		}
		remaining := make([]unixCapturedProcess, 0, len(tree))
		for _, process := range tree {
			if process.pid == root.pid {
				continue
			}
			current, ok := identities[process.pid]
			switch {
			case !ok:
				continue
			case !current.Known():
				remaining = append(remaining, process)
				continue
			case current.Start != process.start:
				continue
			}
			survived = true
			remaining = append(remaining, process)
			if killed[process] {
				continue
			}
			killed[process] = true
			if err := hooks.signal(process.pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				signalErr = errors.Join(signalErr, fmt.Errorf("kill verification process %d: %w", process.pid, err))
			}
		}
		if len(remaining) == 0 {
			return survived, signalErr
		}
		if !time.Now().Before(deadline) {
			pids := make([]int, 0, len(remaining))
			for _, process := range remaining {
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

func mergeUnixCaptured(tree []unixCapturedProcess, process unixCapturedProcess) []unixCapturedProcess {
	for _, current := range tree {
		if current == process {
			return tree
		}
	}
	return append(tree, process)
}

func extendUnixTree(tree []unixCapturedProcess, identities map[int]procmem.Identity) ([]unixCapturedProcess, error) {
	members := make(map[int]unixCapturedProcess, len(tree))
	for _, process := range tree {
		members[process.pid] = process
	}
	for {
		added := false
		for pid, identity := range identities {
			if _, ok := members[pid]; ok {
				continue
			}
			parent, ok := members[identity.PPID]
			if !ok || !unixChildOf(parent, identity, identities) {
				continue
			}
			if !identity.Known() {
				return tree, fmt.Errorf("identity of verification process %d is unknown", pid)
			}
			child := unixCapturedProcess{pid: pid, start: identity.Start, startedAt: identity.StartedAt}
			members[pid] = child
			tree = append(tree, child)
			added = true
		}
		if !added {
			return tree, nil
		}
	}
}

func unixChildOf(parent unixCapturedProcess, child procmem.Identity, identities map[int]procmem.Identity) bool {
	parentNow, present := identities[parent.pid]
	if present && parentNow.Start == parent.start {
		return true
	}
	if child.StartedAt.IsZero() || parent.startedAt.IsZero() || child.StartedAt.Before(parent.startedAt) {
		return false
	}
	if !present {
		return true
	}
	return !parentNow.StartedAt.IsZero() && child.StartedAt.Before(parentNow.StartedAt)
}
