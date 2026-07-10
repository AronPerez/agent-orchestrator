//go:build !windows

package tmux

import "syscall"

// paneProcessGroup returns the process-group id for the pane's lead pid. tmux
// runs each pane in its own process group, so signaling the negative pgid reaps
// the agent and every child it spawned in one call. ok is false when the process
// is already gone (Getpgid → ESRCH) or the group cannot be determined; Destroy
// then falls back to a plain kill-session rather than signaling a guessed group.
func paneProcessGroup(pid int) (int, bool) {
	pgid, err := syscall.Getpgid(pid)
	if err != nil || pgid <= 1 {
		return 0, false
	}
	return pgid, true
}

// processGroupAlive reports whether any process remains in the group. A signal-0
// probe returns nil (alive) or EPERM (alive but not signalable by us) for a live
// group, and ESRCH once every member has exited and been reaped.
func processGroupAlive(pgid int) bool {
	if pgid <= 1 {
		return false
	}
	err := syscall.Kill(-pgid, 0)
	return err == nil || err == syscall.EPERM
}

// terminateProcessGroup force-kills the whole process group with SIGKILL, which
// an agent cannot trap or ignore (unlike the SIGHUP tmux kill-session sends).
func terminateProcessGroup(pgid int) error {
	if pgid <= 1 {
		return nil
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}
