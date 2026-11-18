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
	initialWindowsJobProcessCapacity = 16
	maxWindowsJobProcessCapacity     = 4096
	windowsProcessStopWait           = 5 * time.Second
)

var isProcessInJobProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

type windowsWorkspaceProcessTree struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	job    windows.Handle
	closed bool
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

type jobBasicProcessIDList struct {
	NumberOfAssignedProcesses uint32
	NumberOfProcessIDsInList  uint32
	ProcessIDList             [1]uintptr
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
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return cleanup(fmt.Errorf("open suspended verification process: %w", err))
	}
	err = windows.AssignProcessToJobObject(job, process)
	_ = windows.CloseHandle(process)
	if err != nil {
		return cleanup(fmt.Errorf("assign verification process to job: %w", err))
	}
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
	waitErr := t.cmd.Wait()
	survived, stopErr := t.stopSurvivors()
	return survived, errors.Join(waitErr, stopErr)
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
	processes, err := t.liveProcesses()
	if err != nil {
		return false, errors.Join(err, windows.TerminateJobObject(t.job, 1))
	}
	if len(processes) == 0 {
		return false, nil
	}
	defer func() {
		for _, process := range processes {
			_ = windows.CloseHandle(process)
		}
	}()
	terminateErr := windows.TerminateJobObject(t.job, 1)
	waitErr := waitForWindowsProcesses(processes, windowsProcessStopWait)
	return true, errors.Join(terminateErr, waitErr)
}

// liveProcesses returns identity-bound handles for running descendants in the
// job. cmd.Wait has already proved that the root shell exited, but Windows may
// continue reporting that PID until every kernel reference to it is released.
// Checking each listed member's process handle avoids treating that accounting
// lag (or a completed foreground child) as a surviving background process.
func (t *windowsWorkspaceProcessTree) liveProcesses() ([]windows.Handle, error) {
	pids, err := windowsJobProcessIDs(t.job)
	if err != nil {
		return nil, fmt.Errorf("list verification job processes: %w", err)
	}
	processes := make([]windows.Handle, 0, len(pids))
	closeProcesses := func() {
		for _, process := range processes {
			_ = windows.CloseHandle(process)
		}
	}
	for _, pid := range pids {
		if pid == uintptr(t.cmd.Process.Pid) || pid > uintptr(^uint32(0)) {
			continue
		}
		process, openErr := windows.OpenProcess(
			windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
			false,
			uint32(pid),
		)
		if errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) {
			continue
		}
		if openErr != nil {
			closeProcesses()
			return nil, fmt.Errorf("open verification job process %d: %w", pid, openErr)
		}
		inJob, membershipErr := isProcessInJob(process, t.job)
		if membershipErr != nil {
			_ = windows.CloseHandle(process)
			closeProcesses()
			return nil, fmt.Errorf("check verification job process %d: %w", pid, membershipErr)
		}
		if !inJob {
			_ = windows.CloseHandle(process)
			continue
		}
		status, waitErr := windows.WaitForSingleObject(process, 0)
		if waitErr != nil {
			_ = windows.CloseHandle(process)
			closeProcesses()
			return nil, fmt.Errorf("check verification job process %d state: %w", pid, waitErr)
		}
		switch status {
		case windows.WAIT_OBJECT_0:
			_ = windows.CloseHandle(process)
		case uint32(windows.WAIT_TIMEOUT):
			processes = append(processes, process)
		default:
			_ = windows.CloseHandle(process)
			closeProcesses()
			return nil, fmt.Errorf("check verification job process %d state: unexpected wait status %d", pid, status)
		}
	}
	return processes, nil
}

func windowsJobProcessIDs(job windows.Handle) ([]uintptr, error) {
	capacity := initialWindowsJobProcessCapacity
	headerSize := unsafe.Offsetof(jobBasicProcessIDList{}.ProcessIDList)
	processIDSize := unsafe.Sizeof(uintptr(0))
	for {
		buffer := make([]byte, headerSize+uintptr(capacity)*processIDSize)
		list := (*jobBasicProcessIDList)(unsafe.Pointer(&buffer[0]))
		err := windows.QueryInformationJobObject(
			job,
			windows.JobObjectBasicProcessIdList,
			uintptr(unsafe.Pointer(list)),
			uint32(len(buffer)),
			nil,
		)
		assigned := int(list.NumberOfAssignedProcesses)
		listed := int(list.NumberOfProcessIDsInList)
		if assigned > maxWindowsJobProcessCapacity || listed > capacity {
			return nil, fmt.Errorf(
				"verification job contains %d processes, limit is %d",
				assigned,
				maxWindowsJobProcessCapacity,
			)
		}
		if err == nil && assigned <= listed {
			pids := unsafe.Slice(&list.ProcessIDList[0], listed)
			return append([]uintptr(nil), pids...), nil
		}
		if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
			return nil, err
		}
		needed := assigned
		if needed <= capacity {
			needed = capacity * 2
		}
		if needed > maxWindowsJobProcessCapacity {
			return nil, fmt.Errorf("verification job contains %d processes, limit is %d", needed, maxWindowsJobProcessCapacity)
		}
		capacity = needed
	}
}

func isProcessInJob(process, job windows.Handle) (bool, error) {
	var inJob int32
	result, _, callErr := isProcessInJobProc.Call(
		uintptr(process),
		uintptr(job),
		uintptr(unsafe.Pointer(&inJob)),
	)
	if result == 0 {
		return false, callErr
	}
	return inJob != 0, nil
}

func waitForWindowsProcesses(processes []windows.Handle, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for _, process := range processes {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.New("verification job processes did not stop before the deadline")
		}
		milliseconds := uint32((remaining + time.Millisecond - 1) / time.Millisecond)
		status, err := windows.WaitForSingleObject(process, milliseconds)
		if err != nil {
			return err
		}
		if status == uint32(windows.WAIT_TIMEOUT) {
			return errors.New("verification job processes did not stop before the deadline")
		}
		if status != windows.WAIT_OBJECT_0 {
			return fmt.Errorf("wait for verification job process: unexpected status %d", status)
		}
	}
	return nil
}

func (t *windowsWorkspaceProcessTree) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.job == 0 {
		return nil
	}
	t.closed = true
	err := windows.CloseHandle(t.job)
	t.job = 0
	return err
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
