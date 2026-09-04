---
name: local-services
description: Use when starting, stopping, restarting, or debugging the local AO daemon or its LAN web UI, editing scripts/ service wrappers, deploying a rebuilt ao binary, or when the browser UI on the Connect Mobile port is down or terminals loop "disconnected, reattaching".
trigger: Anything involving the launchd jobs dev.agent-orchestrator.env / .mobile-web, scripts/*.sh, ao-svc, or "is the daemon/UI up?"
---

# Local Services

Repo `scripts/` is the source of truth; launchd runs **copies** under `~/.ao/`.
Deep runbook: `~/.ao/FEREADME.md`.

| Job (gui/$(id -u)/…)                | Runs                         | Serves                                      |
| ----------------------------------- | ---------------------------- | ------------------------------------------- |
| `dev.agent-orchestrator.env`        | one-shot at login            | `AO_KEEP_DAEMON=1` for the GUI session      |
| `dev.agent-orchestrator.mobile-web` | `~/.ao/mobile-web-server.sh` | Expo/Metro for `packages/mobile` on `:8081` |

Plists: `~/Library/LaunchAgents/dev.agent-orchestrator.{env,mobile-web}.plist`.
`~/.ao/ao-svc {up|down|status}` manages both.

**Neither the daemon nor the browser UI is a launchd job.** The desktop app spawns
the daemon on `127.0.0.1:3001`; the daemon serves the browser UI itself from its
Connect Mobile LAN listener. The retired jobs — `lan-web` (Vite on `:3000`) and
`phone-bridge` (`ao-phone-proxy` on `:3011`) — are booted out by
`scripts/dev-setup.sh`. The browser UI needs a daemon built at or after
`2399595db`; an older one answers a browser navigation with a JSON 401.

## Health check

```sh
ao status && ao session ls                       # daemon ready? sessions?
launchctl list | grep agent-orchestrator        # which jobs are loaded
# browser UI (host:port from Settings → Connect Mobile); 200 text/html = up:
curl -s -o /dev/null -w '%{http_code}\n' http://<lan-ip>:3011/
tail -5 ~/.ao/mobile-web.err.log ~/.ao/daemon.err.log
```

## Common operations

```sh
# Fresh machine (or drifted install) — full reproducible setup from the repo:
scripts/dev-setup.sh                             # idempotent; full path: skills/machine-setup/SKILL.md

# Jobs unloaded after a reboot:
~/.ao/ao-svc up

# Browser UI 401s a plain navigation → the daemon predates the embedded UI.
# Redeploy it: scripts/install-desktop-app.sh (app) or ao-svc reload (CLI copy).

# Deploy edited scripts (edit in repo scripts/, then):
cp -f scripts/{ao-svc,mobile-web-server.sh} ~/.ao/
# …then kickstart the affected job.

# Deploy a rebuilt daemon (launchd runs ~/.ao/bin/ao, NOT the PATH install).
# Install ATOMICALLY: an in-place `cp -f` over the running binary corrupts its
# code signature -> macOS SIGKILLs the daemon (OS_REASON_CODESIGNING) -> crash-loop.
# (`ao-svc reload` already does the .new+mv dance for you.)
scripts/daemon-build.sh
cp -f ~/.cache/aoagents/agent-orchestrator/bin/ao ~/.ao/bin/ao.new && mv -f ~/.ao/bin/ao.new ~/.ao/bin/ao
# The daemon is app-owned: relaunch the desktop app (or `scripts/install-desktop-app.sh`)
# to pick up a new binary. There is no daemon launchd job to kickstart.
```

## Daemon restart ≠ agent relaunch (verified 2026-07-02)

Running agents keep the argv they were launched with. **Neither `kickstart -k`
nor `ao stop` relaunches them**: the daemon exits (KeepAlive respawns it) and
the new daemon reattaches to the surviving tmux panes — zero disruption, zero
config pickup. Daemon restarts are only for daemon-binary/wrapper changes.

To make agents pick up new flags/config (e.g. a permission-mode change via
`ao project set-config`), cycle each session:

```sh
ao session kill <id> && ao session restore <id>   # relaunches with the project's CURRENT config
# whole project:
ao session ls   # then loop kill+restore over the live ids
# verify:
ps ax -o command | grep -c 'dangerously-skip-permissions'
```

`restore` relaunches `claude --resume <native-id>` — context survives, and it
resumes directly (no menu on current builds; if an older build parks at the
resume menu, pick option 2 / full transcript).

## Gotchas

- **Terminals loop "disconnected, reattaching"** — on the daemon-served UI this is
  no longer an Origin problem: the UI and API share an origin, so there is no
  allowlist to drift. Check the connection password instead (a rotate invalidates
  the browser's cookie) and re-login.
- **A failed login leaves NO line in `~/.ao/daemon.log`** — `POST
/api/v1/auth/login` is answered outside the request logger. Daemon-log silence
  is not evidence the request never arrived; read the browser's network tab.
- **TCC**: a launchd-spawned daemon cannot read repos under `~/Desktop`. Start the
  daemon from a real terminal (or the desktop app) for sessions needing those.
- **Two `ao` binaries**: PATH `ao` (`~/.local/bin` → `~/.cache/aoagents/...`) vs
  the daemon's `~/.ao/bin/ao`. Rebuilding via `daemon-build.sh` does **not**
  update the running daemon until you copy + kickstart.
- **"did not become ready within 10s"** on restart is usually false — restore-all
  worktree checkouts blow the window. Use `--timeout 30s`, check `ao status`.
- **`running.json`** (`~/.ao/running.json`) is regenerated every start. Never
  commit a copy (gitignored as `scripts/running.json`).
