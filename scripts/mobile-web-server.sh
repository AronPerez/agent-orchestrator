#!/bin/zsh
# Durable mobile FE (Expo/Metro) for Agent Orchestrator, bound to the LAN on :8081.
# launchd KeepAlive supervises it across crashes and reboots. Companion to
# lan-web-server.sh. Serves packages/mobile: web at http://<lan-ip>:8081 and the
# Expo Go manifest at exp://<lan-ip>:8081. The app talks to the daemon on :3001
# (a physical phone reaches it via the ao-phone-proxy bridge on :3011).
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH"

# node lives under nvm (interactive-only in ~/.zshrc); resolve it explicitly like
# lan-web-server.sh: source nvm, then fall back to the newest installed node bin.
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh" >/dev/null 2>&1
_latest_node_bin=$(ls -d "$NVM_DIR"/versions/node/*/bin 2>/dev/null | sort -V | tail -1)
[ -n "$_latest_node_bin" ] && export PATH="$_latest_node_bin:$PATH"

export EXPO_NO_TELEMETRY=1
cd "$HOME/dev/ag-orc/packages/mobile" || exit 1
# --lan serves both the browser (localhost:8081) and Expo Go over the LAN.
# --port is explicit so a busy 8081 fails loudly instead of silently using 8082
# (which would break the phone's saved exp:// URL).
exec ./node_modules/.bin/expo start --lan --port 8081
