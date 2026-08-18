#!/usr/bin/env bash
# One-shot, idempotent setup of the local AO dev services on a macOS machine.
# Makes a fresh machine reproducible from this repo alone:
#   clone → scripts/dev-setup.sh → mobile Expo web UI running under launchd.
#
# The browser UI is no longer a service here: the daemon serves it itself, from
# the LAN listener's own origin (Settings → Connect Mobile). See 4a-2.
#
# The AO daemon is NOT a launchd job: the desktop app spawns its bundled daemon
# on :3001 and replaces any running one on every launch (per-launch
# browser-runtime token, 2026-08 upstream sync). AO_KEEP_DAEMON=1 (set for the
# GUI session by dev.agent-orchestrator.env at login, and in ~/.zshrc for
# terminal launches) keeps it alive after the app quits. Never add a
# KeepAlive/RunAtLoad daemon job back: a colliding daemon falls back to an
# ephemeral port and clobbers ~/.ao/running.json.
#
# Installs/refreshes:
#   - ao CLI           (daemon-build.sh → PATH install)
#   - service scripts  (mobile-web-server.sh, ao-svc → ~/.ao/)
#   - launchd plists   (generated → ~/Library/LaunchAgents/)
#   - ~/dev/ag-orc     (symlink to this checkout; mobile-web serves from it)
#
# Safe to re-run: already-loaded jobs are left running (prints the kickstart
# command instead).
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
la_dir="${HOME}/Library/LaunchAgents"
dom="gui/$(id -u)"

# 0. git hooks: enable the tracked pre-commit (gofmt check)
"${script_dir}/install-hooks.sh"

# 1. ao CLI: build + PATH install; also the ~/.ao/bin/ao copy that live worker
# sessions run (their PATH pins ~/.ao/bin first). Install atomically (cp to
# .new + mv) — an in-place `cp` over a running binary corrupts its code
# signature and macOS SIGKILLs it (OS_REASON_CODESIGNING).
"${script_dir}/daemon-build.sh"
mkdir -p "${HOME}/.ao/bin"
cp -f "${XDG_CACHE_HOME:-${HOME}/.cache}/aoagents/agent-orchestrator/bin/ao" "${HOME}/.ao/bin/ao.new"
mv -f "${HOME}/.ao/bin/ao.new" "${HOME}/.ao/bin/ao"
echo "Installed ~/.ao/bin/ao"

# 2. service scripts (deploy-by-copy; launchd runs the ~/.ao copies)
cp -f "${script_dir}/mobile-web-server.sh" "${script_dir}/ao-svc" "${HOME}/.ao/"
echo "Installed ~/.ao/{mobile-web-server.sh,ao-svc}"

# 3. ~/dev/ag-orc → this checkout (mobile-web-server.sh serves its packages/mobile/)
if [[ -e "${HOME}/dev/ag-orc" && ! -L "${HOME}/dev/ag-orc" ]]; then
  echo "⚠ ~/dev/ag-orc exists and is not a symlink — leaving it alone" >&2
else
  mkdir -p "${HOME}/dev"
  ln -sfn "${repo_root}" "${HOME}/dev/ag-orc"
  echo "Linked ~/dev/ag-orc → ${repo_root}"
fi

# 4. launchd plists (generated here so the repo is the source of truth)
mkdir -p "${la_dir}"

# 4a. migration: retire the old KeepAlive daemon job (daemon is app-owned now)
if launchctl print "${dom}/dev.agent-orchestrator.daemon" >/dev/null 2>&1; then
  launchctl bootout "${dom}/dev.agent-orchestrator.daemon" || true
  echo "Retired launchd daemon job (daemon is app-owned now)"
fi
rm -f "${la_dir}/dev.agent-orchestrator.daemon.plist" "${HOME}/.ao/ao-daemon.sh"

# 4a-2. migration: retire lan-web and phone-bridge. The daemon serves the browser
# UI from its own LAN origin now, so the second Vite server on :3000 (a
# cross-origin UI needing AO_ALLOWED_ORIGINS) and the Origin-laundering phone
# proxy on :3011 both have nothing left to do — the LAN listener binds :3011
# itself. Booting them out is the point, not a courtesy: a stale lan-web keeps
# serving a UI against a daemon nobody allowlists for it any more, and the proxy
# would fight the daemon for :3011.
for retired in dev.agent-orchestrator.lan-web dev.agent-orchestrator.phone-bridge; do
  if launchctl print "${dom}/${retired}" >/dev/null 2>&1; then
    launchctl bootout "${dom}/${retired}" || true
    echo "Retired ${retired} (the daemon serves the web UI itself now)"
  fi
  rm -f "${la_dir}/${retired}.plist"
done
rm -f "${HOME}/.ao/lan-web-server.sh" "${HOME}/.ao/phone-bridge.sh"

# 4b. one-shot login job: AO_KEEP_DAEMON=1 for the GUI session, so Dock/Finder
# app launches spawn a persistent daemon (survives app quit; stops on `ao stop`).
cat > "${la_dir}/dev.agent-orchestrator.env.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.agent-orchestrator.env</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/launchctl</string>
		<string>setenv</string>
		<string>AO_KEEP_DAEMON</string>
		<string>1</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
PLIST


cat > "${la_dir}/dev.agent-orchestrator.mobile-web.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.agent-orchestrator.mobile-web</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/zsh</string>
		<string>-lc</string>
		<string>exec "\$HOME/.ao/mobile-web-server.sh"</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>${HOME}/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
		<key>LANG</key>
		<string>en_US.UTF-8</string>
	</dict>
	<key>WorkingDirectory</key>
	<string>${HOME}/.ao</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>${HOME}/.ao/mobile-web.out.log</string>
	<key>StandardErrorPath</key>
	<string>${HOME}/.ao/mobile-web.err.log</string>
</dict>
</plist>
PLIST

echo "Wrote launchd plists to ~/Library/LaunchAgents/"

# 5. load jobs — leave already-running ones alone
for job in dev.agent-orchestrator.env dev.agent-orchestrator.mobile-web; do
  if launchctl print "${dom}/${job}" >/dev/null 2>&1; then
    echo "loaded: ${job} (already running — to apply changes:"
    echo "         launchctl kickstart -k \"${dom}/${job}\")"
  else
    launchctl bootstrap "${dom}" "${la_dir}/${job}.plist"
    echo "loaded: ${job}"
  fi
done

# 6. non-fatal checks for the bits this script won't do for you
[[ -x "${repo_root}/packages/mobile/node_modules/.bin/expo" ]] \
  || echo "⚠ mobile-web needs expo: cd packages/mobile && npm install"
grep -q "ao-svc" "${HOME}/.zshrc" 2>/dev/null \
  || echo "⚠ add to ~/.zshrc:  alias ao-svc=\"\$HOME/.ao/ao-svc\""
grep -q "AO_KEEP_DAEMON" "${HOME}/.zshrc" 2>/dev/null \
  || echo "⚠ add to ~/.zshrc:  export AO_KEEP_DAEMON=1"
for tool in tmux claude; do
  command -v "${tool}" >/dev/null || echo "⚠ ${tool} not on PATH (sessions need it)"
done

echo "Done. Health: ao status"
echo "Browser UI: enable Settings → Connect Mobile, then open the host:port it shows."
