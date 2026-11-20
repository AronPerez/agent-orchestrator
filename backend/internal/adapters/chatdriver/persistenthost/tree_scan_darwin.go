//go:build darwin

package persistenthost

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	darwinZombieState = 5
	darwinLsofPath    = "/usr/sbin/lsof"
	darwinLsofTimeout = 30 * time.Second
)

// providerTreeMember returns a live process of this user that may still belong
// to the provider tree of dead host hostPID: one in the host's session, one
// carrying the tree marker in its environment, or one working inside workdir.
// It returns 0 when there is none.
func providerTreeMember(hostPID int, marker, workdir string) (int, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return 0, fmt.Errorf("list processes: %w", err)
	}
	self := os.Getpid()
	uid := uint32(os.Getuid()) //nolint:gosec // uid is non-negative.
	entry := []byte(providerTreeEnv + "=" + marker)
	for i := range procs {
		p := &procs[i]
		pid := int(p.Proc.P_pid)
		if pid <= 0 || pid == self || p.Proc.P_stat == darwinZombieState {
			continue
		}
		// Another user's process cannot be part of a tree this user started.
		if p.Eproc.Pcred.P_ruid != uid && p.Eproc.Ucred.Uid != uid {
			continue
		}
		sid, err := unix.Getsid(pid)
		switch {
		case err == nil && sid == hostPID:
			return pid, nil
		case err == nil, errors.Is(err, unix.ESRCH):
		default:
			return 0, fmt.Errorf("read session of process %d: %w", pid, err)
		}
		raw, err := unix.SysctlRaw("kern.procargs2", pid)
		switch {
		case err == nil:
			if procargsHasEnvironmentEntry(raw, entry) {
				return pid, nil
			}
		case errors.Is(err, unix.ESRCH), errors.Is(err, unix.EINVAL), errors.Is(err, unix.EPERM):
			// Gone, a zombie, or a protected process whose environment the
			// kernel withholds; the session check above still applies to it.
		default:
			return 0, fmt.Errorf("read environment of process %d: %w", pid, err)
		}
	}
	return darwinWorktreeProcess(os.Getuid(), self, workdir)
}

// darwinWorktreeProcess returns a live process of uid, other than self, whose
// working directory is inside workdir. macOS exposes another process's
// working directory only through proc_pidinfo, which lsof wraps.
func darwinWorktreeProcess(uid, self int, workdir string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), darwinLsofTimeout)
	defer cancel()
	cmd := aoprocess.CommandContext(
		ctx, darwinLsofPath, "-nP", "-w", "-a", "-d", "cwd", "-u", strconv.Itoa(uid), "-F", "pn",
	)
	output, err := cmd.Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(output) == 0 {
		return 0, nil // lsof found no matching process
	}
	if err != nil {
		return 0, fmt.Errorf("list working directories: %w", err)
	}
	roots := worktreeRoots(workdir)
	pid := 0
	for _, line := range strings.Split(string(output), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ = strconv.Atoi(line[1:])
		case pid > 0 && pid != self && strings.HasPrefix(line, "n") && withinWorktree(roots, line[1:]):
			return pid, nil
		}
	}
	return 0, nil
}

// procargsHasEnvironmentEntry reports whether a KERN_PROCARGS2 buffer (argc,
// executable path, argv, then the environment) holds entry exactly.
func procargsHasEnvironmentEntry(raw, entry []byte) bool {
	if len(raw) < 4 {
		return false
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return false
	}
	rest = bytes.TrimLeft(rest[end:], "\x00")
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
			return bytes.Equal(rest, entry)
		}
		if end == 0 {
			return false
		}
		if bytes.Equal(rest[:end], entry) {
			return true
		}
		rest = rest[end+1:]
	}
	return false
}
