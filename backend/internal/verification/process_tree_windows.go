//go:build windows

package verification

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsProcessDrainWait = 100 * time.Millisecond
	windowsProcessPollWait  = 10 * time.Millisecond
	windowsProcessStopWait  = 5 * time.Second
)

type windowsWorkspaceProcessTree struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	job         windows.Handle
	rootProcess windows.Handle
	closed      bool
}

type jobBasicAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func startWorkspaceProcessTree(ctx context.Context, cmd *exec.Cmd) (workspaceProcessTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create verification job: %w", err)
	}
	tree := &windowsWorkspaceProcessTree{cmd: cmd, job: job}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		_ = tree.Close()
		return nil, fmt.Errorf("configure verification job: %w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW
	cmd.SysProcAttr.HideWindow = true
	cmd.Cancel = tree.terminate
	if err = cmd.Start(); err != nil {
		_ = tree.Close()
		return nil, err
	}
	assigned := false
	cleanup := func(cause error) (workspaceProcessTree, error) {
		if assigned {
			_ = tree.terminate()
		} else if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		return nil, errors.Join(cause, tree.Close())
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return cleanup(fmt.Errorf("open suspended verification process: %w", err))
	}
	err = windows.AssignProcessToJobObject(job, process)
	if err != nil {
		_ = windows.CloseHandle(process)
		return cleanup(fmt.Errorf("assign verification process to job: %w", err))
	}
	tree.rootProcess = process
	assigned = true
	if ctx.Err() != nil {
		_ = tree.terminate()
		return tree, nil
	}
	if err = resumeProcessThreads(uint32(cmd.Process.Pid)); err != nil {
		if ctx.Err() != nil {
			_ = tree.terminate()
			return tree, nil
		}
		return cleanup(fmt.Errorf("resume verification process: %w", err))
	}
	return tree, nil
}

func (t *windowsWorkspaceProcessTree) Wait() (bool, error) {
	// Cmd.Wait also waits for copied output pipes. A background child can keep
	// those pipes open after the shell exits, so anchor cleanup to the root
	// process handle before waiting for exec.Cmd to drain and reap it.
	rootWaitErr := waitForWindowsProcess(t.rootProcess)
	survived, stopErr := t.stopSurvivors()
	waitErr := t.cmd.Wait()
	return survived, errors.Join(rootWaitErr, waitErr, stopErr)
}

func waitForWindowsProcess(process windows.Handle) error {
	status, err := windows.WaitForSingleObject(process, windows.INFINITE)
	if err != nil {
		return err
	}
	if status != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("wait for verification root process: unexpected status %d", status)
	}
	return nil
}

func (t *windowsWorkspaceProcessTree) terminate() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.job == 0 {
		return nil
	}
	return windows.TerminateJobObject(t.job, 1)
}

func (t *windowsWorkspaceProcessTree) stopSurvivors() (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.job == 0 {
		return false, nil
	}
	drained, err := waitForWindowsJobEmpty(t.job, windowsProcessDrainWait)
	if err != nil {
		return false, errors.Join(err, windows.TerminateJobObject(t.job, 1))
	}
	if drained {
		return false, nil
	}
	terminateErr := windows.TerminateJobObject(t.job, 1)
	stopped, waitErr := waitForWindowsJobEmpty(t.job, windowsProcessStopWait)
	if waitErr == nil && !stopped {
		waitErr = errors.New("verification job processes did not stop before the deadline")
	}
	return true, errors.Join(terminateErr, waitErr)
}

// waitForWindowsJobEmpty uses the kernel-owned job handle as the identity
// anchor. It cannot accidentally observe or terminate a recycled process ID.
func waitForWindowsJobEmpty(job windows.Handle, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		var accounting jobBasicAccountingInformation
		err := windows.QueryInformationJobObject(
			job,
			windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&accounting)),
			uint32(unsafe.Sizeof(accounting)),
			nil,
		)
		if err != nil {
			return false, err
		}
		if accounting.ActiveProcesses == 0 {
			return true, nil
		}
		if !time.Now().Before(deadline) {
			return false, nil
		}
		time.Sleep(windowsProcessPollWait)
	}
}

func (t *windowsWorkspaceProcessTree) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	var jobErr, processErr error
	if t.job != 0 {
		jobErr = windows.CloseHandle(t.job)
	}
	t.job = 0
	if t.rootProcess != 0 {
		processErr = windows.CloseHandle(t.rootProcess)
	}
	t.rootProcess = 0
	return errors.Join(jobErr, processErr)
}

func resumeProcessThreads(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot) //nolint:errcheck // best-effort OS handle cleanup
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err = windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		if entry.OwnerProcessID == pid {
			thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if openErr != nil {
				return openErr
			}
			previous, resumeErr := windows.ResumeThread(thread)
			closeErr := windows.CloseHandle(thread)
			if resumeErr != nil || closeErr != nil {
				return errors.Join(resumeErr, closeErr)
			}
			if previous != 1 {
				return fmt.Errorf("initial verification thread suspension count was %d, want 1", previous)
			}
			return nil
		}
		err = windows.Thread32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return errors.New("suspended verification thread not found")
		}
		if err != nil {
			return err
		}
	}
}
