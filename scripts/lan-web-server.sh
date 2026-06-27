#!/bin/zsh
# Durable LAN web UI for Agent Orchestrator.
# Ensures the daemon is up with the LAN CORS origin allowlisted, then runs the
# Vite renderer dev server bound to the LAN on :3000. launchd supervises this
# (KeepAlive), so it survives shell death, crashes, and reboots.
#
# Operational runbook (daemon restart, the AO_ALLOWED_ORIGINS / 403 mux gotcha,
# session resume behavior, file map): see ~/.ao/README.md
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH"

# node lives under nvm, which is initialized in ~/.zshrc (interactive only, not
# sourced by a `zsh -lc` login shell). Resolve it explicitly: source nvm, and
# fall back to the newest installed nvm node bin on PATH.
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh" >/dev/null 2>&1
_latest_node_bin=$(ls -d "$NVM_DIR"/versions/node/*/bin 2>/dev/null | sort -V | tail -1)
[ -n "$_latest_node_bin" ] && export PATH="$_latest_node_bin:$PATH"

# NOTE: this LaunchAgent intentionally does NOT start the daemon. The skyvern /
# skyvern-cloud project repos live under ~/Desktop, which macOS TCC blocks for
# launchd-spawned processes — a daemon started here couldn't access them. Start
# the daemon from a terminal (which has Desktop access) instead:
#   AO_ALLOWED_ORIGINS=http://192.168.1.250:3000 ao start

cd "$HOME/dev/agent-orchestrator/frontend" || exit 1
exec env VITE_NO_ELECTRON=1 VITE_AO_API_BASE_URL=http://192.168.1.250:3000 \
  ./node_modules/.bin/vite --config vite.renderer.config.ts --host 0.0.0.0 --port 3000 --strictPort
