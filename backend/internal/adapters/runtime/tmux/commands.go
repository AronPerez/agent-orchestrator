package tmux

import (
	"fmt"
	"strconv"
)

// newSessionArgs builds args for `tmux new-session -d -s <id> -x 220 -y 50
// -c <cwd> <shell> -c <launchCmd>`. The shell -c form runs the launch command
// inside the configured shell so exported env vars and quoting work correctly.
func newSessionArgs(id, cwd, shellPath, launchCmd string) []string {
	return []string{
		"new-session", "-d",
		"-s", id,
		"-x", "220",
		"-y", "50",
		"-c", cwd,
		shellPath, "-c", launchCmd,
	}
}

// respawnPaneArgs replaces the process in the session's only pane while keeping
// the tmux session and terminal handle intact.
func respawnPaneArgs(id, cwd, shellPath, launchCmd string) []string {
	return []string{
		"respawn-pane", "-k",
		"-t", id + ":0.0",
		"-c", cwd,
		shellPath, "-c", launchCmd,
	}
}

// setStatusOffArgs hides the tmux status bar for the given session.
// set-option uses pane-targeting syntax which does not accept the `=` prefix,
// so we pass the session name directly.
func setStatusOffArgs(id string) []string {
	return []string{"set-option", "-t", id, "status", "off"}
}

// setMouseOnArgs enables tmux mouse mode so the terminal's SGR mouse-wheel
// reports scroll the pane via copy-mode; without it, wheel scrolling no-ops.
// Pane-targeting, so no `=` prefix (see setStatusOffArgs).
func setMouseOnArgs(id string) []string {
	return []string{"set-option", "-t", id, "mouse", "on"}
}

// setWindowSizeLargestArgs makes tmux size the session's window to the LARGEST
// attached client rather than the most recently active one (the default is
// "latest"). A session can be viewed by several clients at once — e.g. the
// desktop app and the phone. Under "latest", a small phone attaching (or
// becoming active on a session switch) shrinks the shared window for the desktop
// too, giving the desktop a stripped-down view. "largest" ignores smaller
// viewers while a bigger one is attached, so a secondary client can never strip
// down the primary's view; when the big client detaches, tmux recomputes and the
// window follows the remaining largest client. Pane-targeting, so no `=` prefix
// (see setStatusOffArgs).
func setWindowSizeLargestArgs(id string) []string {
	return []string{"set-option", "-t", id, "window-size", "largest"}
}

// historyLimitLines is the scrollback AO pins per session. It is tmux's own
// default, chosen so this is purely a hardening: on a host that has not
// overridden the option nothing changes, and on a host that has, AO stops
// inheriting the override. Cost is driven by cells (cols x history-limit), not
// by ANSI density: at AO's birth width of 220 columns a session filled to this
// cap measured ~2.8 MB, versus ~24 KB idle and ~1.1 MB average across a real
// 17-session server. Raising it to match xterm's 5000-line client buffer would
// cost ~2.5x that per capped session, which is a product decision rather than a
// hardening, so it is deliberately not made here.
const historyLimitLines = 2000

// setHistoryLimitArgs pins the session's scrollback instead of inheriting the
// host's. Note the tmux manual describes history-limit as applying to new
// windows; measured on tmux 3.7b that is not the whole story — setting it after
// new-session raises the EXISTING pane's retention (a pane created under a
// global limit of 10 held 92 lines, then grew to 593 after this option was set
// to 1000). So it does not need to precede pane creation, and Create sets it
// alongside the other session options. Pane-targeting, so no `=` prefix (see
// setStatusOffArgs).
func setHistoryLimitArgs(id string) []string {
	return []string{"set-option", "-t", id, "history-limit", strconv.Itoa(historyLimitLines)}
}

// setDestroyUnattachedOffArgs pins destroy-unattached off for the session.
// AO shares the operator's tmux server (there is no -L/-S anywhere in this
// adapter), so every survival property it depends on is otherwise an unset
// default on a machine AO does not control. A host whose tmux config sets
// `destroy-unattached on` destroys the session when the last client detaches —
// the exact event a background agent session exists to survive. Measured on
// tmux 3.7b against a server with that global set: without this option the
// session is destroyed on detach; with it, the session survives.
// Pane-targeting, so no `=` prefix (see setStatusOffArgs).
func setDestroyUnattachedOffArgs(id string) []string {
	return []string{"set-option", "-t", id, "destroy-unattached", "off"}
}

// panePIDArgs returns the pid of tmux's direct pane process. AO walks its
// descendants to find the exact supervisor for the current launch, and Destroy
// uses it to resolve the agent's process group before teardown. display-message
// is pane-targeting, so it takes a plain session name (no `=` prefix; see
// setStatusOffArgs).
func panePIDArgs(id string) []string {
	return []string{"display-message", "-p", "-t", id + ":0.0", "#{pane_pid}"}
}

// paneCurrentPathArgs prints tmux's cwd for the session's active pane. Create
// uses this after new-session so a poisoned tmux server that ignores -c fails
// loudly instead of silently starting the agent in the wrong directory.
func paneCurrentPathArgs(id string) []string {
	return []string{"display-message", "-p", "-t", id, "#{pane_current_path}"}
}

// killSessionArgs builds args for `tmux kill-session -t =<id>`. The `=` prefix
// requests exact-name matching so a session "foo" does not accidentally match
// "foobar" (tmux otherwise does unique-prefix matching).
func killSessionArgs(id string) []string {
	return []string{"kill-session", "-t", exactSessionTarget(id)}
}

// hasSessionArgs builds args for `tmux has-session -t =<id>`. The `=` prefix
// requests exact-name matching (see killSessionArgs).
func hasSessionArgs(id string) []string {
	return []string{"has-session", "-t", exactSessionTarget(id)}
}

// exactSessionTarget wraps id in tmux's exact-match prefix `=` so session-
// selection commands (-t) target only the session with that precise name.
// Session-selection commands like kill-session, has-session, and list-panes
// support this prefix; pane-targeting commands (send-keys, capture-pane,
// set-option) use a plain session name.
func exactSessionTarget(id string) string {
	return "=" + id
}

// listPanePIDsArgs builds args for `tmux list-panes -s -t =<id> -F #{pane_pid}`.
// -s lists every pane in the whole session (not just the active window); the
// exact-match target `=` avoids prefix collisions (see killSessionArgs). Each
// #{pane_pid} is the pane's session-leader pid, used to reap the pane's
// descendants when the session is destroyed.
func listPanePIDsArgs(id string) []string {
	return []string{"list-panes", "-s", "-t", exactSessionTarget(id), "-F", "#{pane_pid}"}
}

// sendKeysLiteralArgs builds args for `tmux send-keys -t <id> -l <chunk>`.
// The -l flag stops tmux interpreting words like "Enter" as key names so the
// text is sent verbatim.
func sendKeysLiteralArgs(id, chunk string) []string {
	return []string{"send-keys", "-t", id, "-l", chunk}
}

// sendEnterArgs builds args for `tmux send-keys -t <id> Enter` to submit the
// queued input.
func sendEnterArgs(id string) []string {
	return []string{"send-keys", "-t", id, "Enter"}
}

// sendInterruptArgs builds args for `tmux send-keys -t <id> C-c` to interrupt
// the foreground process without killing the terminal session.
func sendInterruptArgs(id string) []string {
	return []string{"send-keys", "-t", id, "C-c"}
}

// capturePaneArgs builds args for `tmux capture-pane -t <id> -p -S -<lines>`.
// -p prints to stdout; -S -<n> starts n lines back in history.
func capturePaneArgs(id string, lines int) []string {
	return []string{"capture-pane", "-t", id, "-p", "-S", fmt.Sprintf("-%d", lines)}
}
