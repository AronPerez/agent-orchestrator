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

var (
	isProcessInJobProc       = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")
	getProcessIDOfThreadProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessIdOfThread")
	suspendThreadProc        = windows.NewLazySystemDLL("kernel32.dll").NewProc("SuspendThread")

	windowsJobProcessSnapshotHookFn = func() {}
)

type windowsWorkspaceProcessTree struct {
	mu          sync.Mutex
	cmd         *exec.Cmd
	job         windows.Handle
	rootProcess windows.Handle
	closed      bool
	stopOnce    sync.Once
	stopResult  bool
	stopErr     error
}

type jobBasicProcessIDList struct {
	NumberOfAssignedProcesses uint32
	NumberOfProcessIDsInList  uint32
	ProcessIDList             [1]uintptr
}

type windowsProcessIdentity struct {
	pid     uint32
	created uint64
}

type windowsOwnedJobProcess struct {
	identity windowsProcessIdentity
	handle   windows.Handle
}

type windowsQuiescedJob struct {
	processes []windowsOwnedJobProcess
	threads   []windows.Handle
}

type windowsThreadIdentity struct {
	process windowsProcessIdentity
	tid     uint32
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
	_, err := t.stop()
	return err
}

func (t *windowsWorkspaceProcessTree) stopSurvivors() (bool, error) {
	return t.stop()
}

func (t *windowsWorkspaceProcessTree) stop() (bool, error) {
	t.stopOnce.Do(func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.closed || t.job == 0 {
			return
		}
		owned, captureErr := quiesceWindowsJobProcesses(t.job, windowsProcessStopWait)
		defer owned.close()
		t.stopResult = len(owned.processes) > 0
		if len(owned.processes) == 0 && captureErr == nil {
			return
		}
		terminateErr := windows.TerminateJobObject(t.job, 1)
		waitErr := waitForWindowsOwnedJobProcesses(owned.processes, windowsProcessStopWait)
		t.stopErr = errors.Join(captureErr, terminateErr, waitErr)
		if t.stopErr != nil {
			t.stopErr = markCleanupUncertain(t.stopErr)
		}
	})
	return t.stopResult, t.stopErr
}

func quiesceWindowsJobProcesses(job windows.Handle, timeout time.Duration) (windowsQuiescedJob, error) {
	deadline := time.Now().Add(timeout)
	processes := make(map[windowsProcessIdentity]windows.Handle)
	suspended := make(map[windowsThreadIdentity]windows.Handle)
	hookCalled := false
	for {
		active, err := liveWindowsJobProcesses(job)
		if err != nil {
			return windowsQuiescedJobFromMaps(processes, suspended), err
		}
		changed := false
		activeByPID := make(map[uint32]windowsProcessIdentity, len(active))
		for _, process := range active {
			activeByPID[process.identity.pid] = process.identity
			if _, ok := processes[process.identity]; ok {
				_ = windows.CloseHandle(process.handle)
				continue
			}
			processes[process.identity] = process.handle
			changed = true
		}
		if !hookCalled {
			hookCalled = true
			windowsJobProcessSnapshotHookFn()
		}
		threads, err := windowsJobThreads(activeByPID)
		if err != nil {
			return windowsQuiescedJobFromMaps(processes, suspended), err
		}
		for _, thread := range threads {
			if _, ok := suspended[thread]; ok {
				continue
			}
			process := processes[thread.process]
			threadHandle, suspendErr := suspendWindowsThread(process, thread.process.pid, thread.tid)
			if suspendErr != nil {
				return windowsQuiescedJobFromMaps(processes, suspended), suspendErr
			}
			if threadHandle != 0 {
				suspended[thread] = threadHandle
				changed = true
			}
		}
		if !changed {
			_, err = liveWindowsOwnedProcesses(processes)
			return windowsQuiescedJobFromMaps(processes, suspended), err
		}
		if !time.Now().Before(deadline) {
			return windowsQuiescedJobFromMaps(processes, suspended), errors.New("verification job did not quiesce before the deadline")
		}
	}
}

func liveWindowsJobProcesses(job windows.Handle) ([]windowsOwnedJobProcess, error) {
	pids, err := windowsJobProcessIDs(job)
	if err != nil {
		return nil, fmt.Errorf("list verification job processes: %w", err)
	}
	processes := make([]windowsOwnedJobProcess, 0, len(pids))
	for _, pid := range pids {
		if pid > uintptr(^uint32(0)) {
			continue
		}
		process, openErr := windows.OpenProcess(
			windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
			false,
			uint32(pid),
		)
		if errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) {
			continue
		}
		if openErr != nil {
			closeWindowsOwnedJobProcesses(processes)
			return nil, fmt.Errorf("open verification job process %d: %w", pid, openErr)
		}
		inJob, membershipErr := isProcessInJob(process, job)
		if membershipErr != nil {
			_ = windows.CloseHandle(process)
			closeWindowsOwnedJobProcesses(processes)
			return nil, fmt.Errorf("check verification job process %d: %w", pid, membershipErr)
		}
		if !inJob {
			_ = windows.CloseHandle(process)
			continue
		}
		status, waitErr := windows.WaitForSingleObject(process, 0)
		if waitErr != nil {
			_ = windows.CloseHandle(process)
			closeWindowsOwnedJobProcesses(processes)
			return nil, fmt.Errorf("check verification job process %d state: %w", pid, waitErr)
		}
		switch status {
		case windows.WAIT_OBJECT_0:
			_ = windows.CloseHandle(process)
		case uint32(windows.WAIT_TIMEOUT):
			identity, identityErr := windowsProcessIdentityForHandle(uint32(pid), process)
			if identityErr != nil {
				_ = windows.CloseHandle(process)
				closeWindowsOwnedJobProcesses(processes)
				return nil, identityErr
			}
			processes = append(processes, windowsOwnedJobProcess{identity: identity, handle: process})
		default:
			_ = windows.CloseHandle(process)
			closeWindowsOwnedJobProcesses(processes)
			return nil, fmt.Errorf("check verification job process %d state: unexpected wait status %d", pid, status)
		}
	}
	return processes, nil
}

func windowsProcessIdentityForHandle(pid uint32, process windows.Handle) (windowsProcessIdentity, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
		return windowsProcessIdentity{}, fmt.Errorf("identify verification job process %d: %w", pid, err)
	}
	return windowsProcessIdentity{
		pid:     pid,
		created: uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime),
	}, nil
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

func windowsJobThreads(active map[uint32]windowsProcessIdentity) ([]windowsThreadIdentity, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return nil, fmt.Errorf("snapshot verification job threads: %w", err)
	}
	defer windows.CloseHandle(snapshot) //nolint:errcheck // best-effort OS handle cleanup
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err = windows.Thread32First(snapshot, &entry); errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("list verification job threads: %w", err)
	}
	threads := make([]windowsThreadIdentity, 0)
	for {
		if process, ok := active[entry.OwnerProcessID]; ok {
			threads = append(threads, windowsThreadIdentity{process: process, tid: entry.ThreadID})
		}
		err = windows.Thread32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return threads, nil
		}
		if err != nil {
			return nil, fmt.Errorf("list verification job threads: %w", err)
		}
	}
}

func suspendWindowsThread(process windows.Handle, expectedPID, tid uint32) (windows.Handle, error) {
	status, err := windows.WaitForSingleObject(process, 0)
	if err != nil {
		return 0, fmt.Errorf("check verification process %d before suspending thread %d: %w", expectedPID, tid, err)
	}
	if status == windows.WAIT_OBJECT_0 {
		return 0, nil
	}
	if status != uint32(windows.WAIT_TIMEOUT) {
		return 0, fmt.Errorf("check verification process %d before suspending thread %d: unexpected status %d", expectedPID, tid, status)
	}
	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME|windows.THREAD_QUERY_LIMITED_INFORMATION, false, tid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open verification thread %d: %w", tid, err)
	}
	keepHandle := false
	defer func() {
		if !keepHandle {
			_ = windows.CloseHandle(thread)
		}
	}()
	owner, _, ownerErr := getProcessIDOfThreadProc.Call(uintptr(thread))
	if owner == 0 {
		return 0, fmt.Errorf("identify verification thread %d: %w", tid, ownerErr)
	}
	if uint32(owner) != expectedPID {
		return 0, nil
	}
	status, err = windows.WaitForSingleObject(process, 0)
	if err != nil {
		return 0, err
	}
	if status == windows.WAIT_OBJECT_0 {
		return 0, nil
	}
	if status != uint32(windows.WAIT_TIMEOUT) {
		return 0, fmt.Errorf("recheck verification process %d before suspending thread %d: unexpected status %d", expectedPID, tid, status)
	}
	previous, _, suspendErr := suspendThreadProc.Call(uintptr(thread))
	if uint32(previous) == ^uint32(0) {
		if errors.Is(suspendErr, windows.ERROR_INVALID_HANDLE) || errors.Is(suspendErr, windows.ERROR_INVALID_PARAMETER) {
			return 0, nil
		}
		return 0, fmt.Errorf("suspend verification thread %d: %w", tid, suspendErr)
	}
	keepHandle = true
	return thread, nil
}

func windowsQuiescedJobFromMaps(
	processes map[windowsProcessIdentity]windows.Handle,
	threads map[windowsThreadIdentity]windows.Handle,
) windowsQuiescedJob {
	result := windowsQuiescedJob{processes: windowsOwnedProcessesFromMap(processes)}
	result.threads = make([]windows.Handle, 0, len(threads))
	for _, handle := range threads {
		result.threads = append(result.threads, handle)
	}
	return result
}

func (j windowsQuiescedJob) close() {
	closeWindowsOwnedJobProcesses(j.processes)
	for _, thread := range j.threads {
		_ = windows.CloseHandle(thread)
	}
}

func windowsOwnedProcessesFromMap(processes map[windowsProcessIdentity]windows.Handle) []windowsOwnedJobProcess {
	result := make([]windowsOwnedJobProcess, 0, len(processes))
	for identity, handle := range processes {
		result = append(result, windowsOwnedJobProcess{identity: identity, handle: handle})
	}
	return result
}

func liveWindowsOwnedProcesses(processes map[windowsProcessIdentity]windows.Handle) ([]windowsOwnedJobProcess, error) {
	result := make([]windowsOwnedJobProcess, 0, len(processes))
	signaled := make([]windowsProcessIdentity, 0)
	for identity, handle := range processes {
		status, err := windows.WaitForSingleObject(handle, 0)
		if err != nil {
			return windowsOwnedProcessesFromMap(processes), fmt.Errorf("check quiesced verification process %d: %w", identity.pid, err)
		}
		switch status {
		case windows.WAIT_OBJECT_0:
			signaled = append(signaled, identity)
		case uint32(windows.WAIT_TIMEOUT):
			result = append(result, windowsOwnedJobProcess{identity: identity, handle: handle})
		default:
			return windowsOwnedProcessesFromMap(processes), fmt.Errorf("check quiesced verification process %d: unexpected status %d", identity.pid, status)
		}
	}
	for _, identity := range signaled {
		_ = windows.CloseHandle(processes[identity])
		delete(processes, identity)
	}
	return result, nil
}

func closeWindowsOwnedJobProcesses(processes []windowsOwnedJobProcess) {
	for _, process := range processes {
		_ = windows.CloseHandle(process.handle)
	}
}

func waitForWindowsOwnedJobProcesses(processes []windowsOwnedJobProcess, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for _, process := range processes {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errors.New("verification job processes did not stop before the deadline")
		}
		milliseconds := uint32((remaining + time.Millisecond - 1) / time.Millisecond)
		status, err := windows.WaitForSingleObject(process.handle, milliseconds)
		if err != nil {
			return err
		}
		if status == uint32(windows.WAIT_TIMEOUT) {
			return errors.New("verification job processes did not stop before the deadline")
		}
		if status != windows.WAIT_OBJECT_0 {
			return fmt.Errorf("wait for verification job process %d: unexpected status %d", process.identity.pid, status)
		}
	}
	return nil
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
