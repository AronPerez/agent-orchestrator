//go:build linux

package persistenthost

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// providerTreeMember returns a live process that may still belong to the
// provider tree of dead host hostPID: one in the host's session, one carrying
// the tree marker in its environment, or one working inside workdir. It
// returns 0 when there is none.
func providerTreeMember(hostPID int, marker, workdir string) (int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("list processes: %w", err)
	}
	self := os.Getpid()
	entry := []byte(providerTreeEnv + "=" + marker)
	roots := worktreeRoots(workdir)
	for _, dirEntry := range entries {
		pid, err := strconv.Atoi(dirEntry.Name())
		if err != nil || pid == self {
			continue
		}
		stat, err := os.ReadFile("/proc/" + dirEntry.Name() + "/stat") //nolint:gosec // procfs path from a numeric pid.
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("read status of process %d: %w", pid, err)
		}
		session, alive, ok := parseProcStatSession(string(stat))
		if !ok {
			return 0, fmt.Errorf("parse status of process %d", pid)
		}
		if !alive {
			continue
		}
		if session == hostPID {
			return pid, nil
		}
		environ, err := os.ReadFile("/proc/" + dirEntry.Name() + "/environ") //nolint:gosec // procfs path from a numeric pid.
		switch {
		case err == nil:
			if environHasEntry(environ, entry) {
				return pid, nil
			}
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
			// Gone, or another user's process, which cannot be part of a tree
			// this user started.
		default:
			return 0, fmt.Errorf("read environment of process %d: %w", pid, err)
		}
		cwd, err := os.Readlink("/proc/" + dirEntry.Name() + "/cwd")
		switch {
		case err == nil:
			if withinWorktree(roots, cwd) {
				return pid, nil
			}
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
		default:
			return 0, fmt.Errorf("read working directory of process %d: %w", pid, err)
		}
	}
	return 0, nil
}

// parseProcStatSession reads the session id from /proc/<pid>/stat. The command
// name may hold spaces and parentheses, so fields are counted after its last
// ')': state is the first, session the fourth.
func parseProcStatSession(stat string) (session int, alive, ok bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, false, false
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 4 {
		return 0, false, false
	}
	session, err := strconv.Atoi(fields[3])
	if err != nil {
		return 0, false, false
	}
	state := fields[0]
	return session, state != "Z" && state != "X" && state != "x", true
}

func environHasEntry(environ, entry []byte) bool {
	for _, item := range bytes.Split(environ, []byte{0}) {
		if bytes.Equal(item, entry) {
			return true
		}
	}
	return false
}
