//go:build windows

package tmux

// The tmux runtime never runs on Windows (runtimeselect dispatches to the conpty
// runtime there), but the package is imported unconditionally, so these stubs
// keep it building. They are never called: Destroy's pane-pid resolution fails
// on Windows and it falls back to a plain kill-session.

func paneProcessGroup(int) (int, bool) { return 0, false }

func processGroupAlive(int) bool { return false }

func terminateProcessGroup(int) error { return nil }
