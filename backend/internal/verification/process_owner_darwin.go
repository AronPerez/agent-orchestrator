//go:build darwin

package verification

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	darwinZombieState = 5
	darwinLsofPath    = "/usr/sbin/lsof"
)

func prepareUnixProcessOwnership(ctx context.Context, cmd *exec.Cmd) (unixProcessOwnership, func() error, error) {
	reader, writer, err := os.Pipe()
	if err != nil {
		return unixProcessOwnership{}, nil, fmt.Errorf("create verification ownership pipe: %w", err)
	}
	closeOwnership := func() error { return errors.Join(writer.Close(), reader.Close()) }
	descriptor, err := darwinDescriptorName(ctx, os.Getpid(), int(writer.Fd()))
	if err != nil {
		return unixProcessOwnership{}, nil, errors.Join(
			fmt.Errorf("identify verification ownership pipe: %w", err), reader.Close(), writer.Close(),
		)
	}
	go func() { _, _ = io.Copy(io.Discard, reader) }()
	token := uuid.NewString()
	cmd.Env = newStepEnvironment(cmd.Env, map[string]string{verificationOwnerEnvironment: token}).list
	fd := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, writer)
	return unixProcessOwnership{
		token: token, descriptor: descriptor, fd: fd, daemonPID: os.Getpid(),
	}, closeOwnership, nil
}

func darwinDescriptorName(ctx context.Context, pid, fd int) (string, error) {
	cmd := aoprocess.CommandContext(
		ctx, darwinLsofPath, "-nP", "-a", "-p", strconv.Itoa(pid), "-d", strconv.Itoa(fd), "-F", "n",
	)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "n") && len(line) > 1 {
			return line[1:], nil
		}
	}
	return "", errors.New("ownership pipe was absent from lsof output")
}

func ownedVerificationProcesses(
	ctx context.Context,
	owner unixProcessOwnership,
	descendants *unixDescendantWatch,
) ([]unixOwnedProcess, error) {
	identities, tracked, err := sharedUnixDescendantObserver.current(ctx, descendants)
	if err != nil {
		return nil, err
	}
	descriptorPIDs, err := darwinDescriptorProcesses(ctx, owner.fd, owner.descriptor)
	if err != nil {
		return nil, err
	}
	marker := verificationOwnerEnvironment + "=" + owner.token
	owned := make(map[unixOwnedProcess]struct{}, len(tracked))
	for _, process := range tracked {
		if process.pid != owner.daemonPID {
			owned[process] = struct{}{}
		}
	}
	for pid, identity := range identities {
		if pid == owner.daemonPID {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		marked := descriptorPIDs[pid]
		if !marked {
			marked, err = darwinProcessHasEnvironmentEntry(pid, marker)
			if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read environment of process %d: %w", pid, err)
			}
		}
		if !marked {
			continue
		}
		if !identity.Known() {
			return nil, fmt.Errorf("identity of verification process %d is unknown", pid)
		}
		process, current, startErr := unixProcessFromSnapshot(pid, identity.Start, currentVerificationProcessStart)
		if startErr != nil {
			return nil, fmt.Errorf("recheck verification process %d: %w", pid, startErr)
		}
		if !current {
			continue
		}
		owned[process] = struct{}{}
	}
	result := make([]unixOwnedProcess, 0, len(owned))
	for process := range owned {
		result = append(result, process)
	}
	return result, nil
}

func darwinDescriptorProcesses(ctx context.Context, fd int, descriptor string) (map[int]bool, error) {
	cmd := aoprocess.CommandContext(ctx, darwinLsofPath, "-nP", "-a", "-d", strconv.Itoa(fd), "-F", "pn")
	output, err := cmd.Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(output) == 0 {
		return map[int]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	owned := make(map[int]bool)
	pid := 0
	for _, line := range strings.Split(string(output), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case pid > 0 && strings.HasPrefix(line, "n") && line[1:] == descriptor:
			owned[pid] = true
		}
	}
	return owned, nil
}

func darwinProcessHasEnvironmentEntry(pid int, marker string) (bool, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return false, err
	}
	return darwinProcargsHasEnvironmentEntry(raw, marker), nil
}

func darwinProcargsHasEnvironmentEntry(raw []byte, marker string) bool {
	if len(raw) < 4 {
		return false
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return false
	}
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	for range argc {
		end = bytes.IndexByte(rest, 0)
		if end < 0 {
			return false
		}
		rest = rest[end+1:]
	}
	for len(rest) > 0 {
		end = bytes.IndexByte(rest, 0)
		if end < 0 {
			return string(rest) == marker
		}
		if end == 0 {
			return false
		}
		if string(rest[:end]) == marker {
			return true
		}
		rest = rest[end+1:]
	}
	return false
}

func signalOwnedVerificationProcess(process unixOwnedProcess, signal syscall.Signal) error {
	return signalUnixProcessIfCurrent(process, signal, currentVerificationProcessStart, syscall.Kill)
}

func currentVerificationProcessStart(pid int) (string, error) {
	return darwinVerificationProcessStart(pid, false)
}

func verificationProcessStartForWatch(pid int) (string, error) {
	return darwinVerificationProcessStart(pid, true)
}

func darwinVerificationProcessStart(pid int, includeZombie bool) (string, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EIO) {
		return "", errUnixProcessGone
	}
	if err != nil {
		return "", err
	}
	if !includeZombie && process.Proc.P_stat == darwinZombieState {
		return "", errUnixProcessGone
	}
	return darwinProcessIdentity(*process), nil
}

func darwinProcessIdentity(process unix.KinfoProc) string {
	started := process.Proc.P_starttime
	return strconv.FormatInt(started.Sec, 10) + "." + strconv.FormatInt(int64(started.Usec), 10)
}
