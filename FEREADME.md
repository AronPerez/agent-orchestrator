# ~/.ao — Agent Orchestrator runtime

Runtime state + logs for the **new** AO install (binary: `~/.local/bin/ao`).
The daemon listens on `127.0.0.1:3001` and, when Connect Mobile is enabled, serves
the web UI itself on its LAN listener (see "Web UI" below).

## Web UI: the daemon serves it — no second server, no AO_ALLOWED_ORIGINS

Enable **Settings → Connect Mobile**, then open the `host:port` it shows (`:3011`
by default) from any browser on the network. The daemon serves the UI from that
same origin and prompts for the connection password; the cookie it sets carries
REST, the SSE stream, and terminals.

Nothing else needs configuring. The UI and the API are the same origin now, so
`AO_ALLOWED_ORIGINS` is **not** involved — it is load-bearing only for a UI hosted
somewhere else (a Vite dev server, or a build served from another host). If a
daemon-served page ever needs an allowlist entry, that is a bug, not a setting.

**Requires a daemon built at or after `2399595db`.** An older one has no embedded
UI and answers a browser navigation with a JSON 401. After updating the repo,
redeploy the daemon (`scripts/install-desktop-app.sh`, or `ao-svc reload` for the
CLI) _before_ expecting the browser UI.

**Retired:** the `dev.agent-orchestrator.lan-web` Vite server on `:3000` and the
`dev.agent-orchestrator.phone-bridge` proxy on `:3011`. The first served a
cross-origin UI that needed the allowlist; the second laundered `Origin` headers so
a browser could reach the loopback daemon. The daemon binds `:3011` itself now and
authenticates with the connection password. `scripts/dev-setup.sh` boots both jobs
out and removes their plists on its next run.

### Verify the browser path

```sh
# host:port of the LAN listener (Settings → Connect Mobile shows both):
curl -s -o /dev/null -w '%{http_code} %{content_type}\n' http://<lan-ip>:3011/
#   200 text/html  → the daemon is serving the UI (login prompt)
#   401 application/json → the daemon predates 2399595db; redeploy it

# Data stays gated until you log in — 401 here is CORRECT, not a fault:
curl -s -o /dev/null -w '%{http_code}\n' http://<lan-ip>:3011/api/v1/sessions

# Terminals attaching, after logging in from the browser:
grep 'path=/mux' ~/.ao/daemon.log | grep 'status=101' | tail
```

A failed **login** leaves no line in `~/.ao/daemon.log` at all: `POST
/api/v1/auth/login` is answered outside the request logger. Silence there means
nothing — check the browser's network tab instead.

(A `/api/v1/notifications/stream` request stuck on **"pending"** in the network tab is
normal — it's a long-lived SSE stream.)

## Web UI (LAN)

Served by the daemon itself on the Connect Mobile listener — there is no separate
web-server job any more. See the section at the top for how to reach it.

The **mobile** Expo app is still a launchd job (`dev.agent-orchestrator.mobile-web`,
`~/.ao/mobile-web-server.sh`) on `:8081`; it points at the same LAN listener.

## What a daemon restart does to running sessions

`ao stop` tears down the session tmux panes; `ao start` runs restore-all, which
re-checks-out each session's git worktree and relaunches the agent as `claude --resume`:

- **"did not become ready within 10s" is often a false failure** — restore-all's worktree
  checkout on a big repo (skyvern-cloud ~13.5k files) blows past the 10s window. The daemon
  comes up a few seconds later; check `ao status` / the log for a later `daemon listening`
  line before re-running. `--timeout 30s` avoids the spurious non-zero exit.
- **Agents re-park at the `claude --resume` menu** (1. summary / 2. full / 3. don't ask).
  Until a choice is made the agent hasn't started, so `ao session ls` reads `[no_signal]`.
  To preserve a worker's full context pick option 2:
  `tmux send-keys -t <sess> Down; sleep 0.4; tmux send-keys -t <sess> Enter`.
  "Resume from summary" is lossy/irreversible for in-flight work.
- **`[no_signal]` ≠ broken** — it just means no heartbeat received since reboot; it clears
  on the agent's next turn.

## File map

| Path                       | What                                                      |
| -------------------------- | --------------------------------------------------------- |
| `bin/`                     | helper shims (e.g. `gh` wrapper used for git credential)  |
| `daemon.log`               | daemon HTTP + lifecycle log                               |
| `data/`                    | sqlite (`ao.db`), worktrees, session state, `hooks.log`   |
| `electron/`                | Electron desktop shell state                              |
| `mobile-web-server.sh`     | launchd-run Expo/Metro launcher for the mobile app (8081) |
| `mobile-web.{out,err}.log` | Expo server stdout/stderr                                 |
| `running.json`             | live daemon `{pid, port, startedAt}`                      |
| `mandates/`                | session mandate backups                                   |

## Health check

```sh
ao status      # daemon ready / pid / port
ao doctor      # core + tools + harness + GitHub token checks
ao session ls  # active sessions and their states
```
