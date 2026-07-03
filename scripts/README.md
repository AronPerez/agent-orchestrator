# scripts/

Source of truth for the local launchd service wrappers. These are **deployed by
copy** to `~/.ao/` — launchd runs the copies, not this checkout. After editing a
script here, re-copy and restart the job (see below).

| File                | What it is                                                                                                 |
| ------------------- | ---------------------------------------------------------------------------------------------------------- |
| `ao-daemon.sh`      | launchd wrapper for the AO daemon (`~/.ao/bin/ao daemon`); pins `AO_ALLOWED_ORIGINS` to the current LAN IP |
| `lan-web-server.sh` | launchd wrapper for the LAN web UI — Vite renderer dev server on `0.0.0.0:3000`                            |
| `ao-svc`            | `up/down/status` for the **lan-web job only** (the daemon has its own plist, managed via raw `launchctl`)  |
| `daemon-build.sh`   | builds `ao` from `backend/` and installs it on PATH (`~/.local/bin/ao` → `~/.cache/aoagents/.../ao`)       |

The plists live at `~/Library/LaunchAgents/dev.agent-orchestrator.{daemon,lan-web}.plist`
(reference copies in `~/.ao/`); they are not tracked here.

## Deploy after editing a script

```sh
cp -f scripts/{ao-svc,ao-daemon.sh,lan-web-server.sh} ~/.ao/
launchctl kickstart -k "gui/$(id -u)/dev.agent-orchestrator.lan-web"   # if lan-web changed
launchctl kickstart -k "gui/$(id -u)/dev.agent-orchestrator.daemon"    # if daemon wrapper changed — ⚠ disrupts sessions
```

## Deploy a new daemon binary

The launchd daemon runs `~/.ao/bin/ao`, a **separate copy** from the PATH install:

```sh
scripts/daemon-build.sh
cp -f ~/.cache/aoagents/agent-orchestrator/bin/ao ~/.ao/bin/ao
launchctl kickstart -k "gui/$(id -u)/dev.agent-orchestrator.daemon"    # ⚠ re-parks all sessions at claude --resume
```

Full runbook (403 mux gotcha, TCC/Desktop override, session-restore behavior):
`~/.ao/FEREADME.md`. Agent-facing guide: `skills/local-services/SKILL.md`.

`running.json` is daemon run-state (`~/.ao/running.json`), regenerated every
start — gitignored here; never commit a stray copy.
