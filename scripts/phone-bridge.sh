#!/bin/zsh
# Durable LAN bridge for the AO mobile app on a physical phone. launchd KeepAlive
# supervises it. Companion to mobile-web-server.sh.
#
# The daemon binds 127.0.0.1:3001 with no auth, so a phone can't reach it directly.
# This forwards ONE LAN port :3011 -> 127.0.0.1:3001, trust-on-first-connect: the
# first device to connect is pinned (state in ~/.ao/phone-allow.json); others are
# refused. Put Host=<lan-ip>, Port=3011 in the app's Settings.
#
# Re-pair a different phone (env can't be passed to a launchd job, so do it by hand):
#   rm ~/.ao/phone-allow.json && launchctl kickstart -k "gui/$(id -u)/dev.agent-orchestrator.phone-bridge"
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH"

# node via nvm (the proxy is pure stdlib, but launchd's PATH lacks nvm's node).
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh" >/dev/null 2>&1
_latest_node_bin=$(ls -d "$NVM_DIR"/versions/node/*/bin 2>/dev/null | sort -V | tail -1)
[ -n "$_latest_node_bin" ] && export PATH="$_latest_node_bin:$PATH"

exec node "$HOME/dev/ag-orc/packages/mobile/scripts/ao-phone-proxy.js"
