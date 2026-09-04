## Ticket

No upstream issue yet. Design note: [remote hosts RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md).

## Problem

There is nowhere for the desktop app to keep a list of remote daemons a user has saved, no way to check whether one is reachable, and no safe channel for the renderer to talk to one — the renderer cannot hold a connection credential itself without every remote host's password becoming visible to any code running in that process.

## Solution

The desktop app reads and writes the CLI's `~/.ao/remotes.json` (mode 0600, refused if looser; Windows exempt), probes a host through `/healthz` with the saved connection password as a Bearer token, and exposes list/add/update/remove/probe/request over IPC. Only `{label, url}` ever crosses to the renderer; a request that would redirect the credential off-host is refused before anything is sent. Nothing in the renderer calls this yet — it lands dark.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npx vitest run src/main/remotes-store.test.ts src/main/remote-request.test.ts src/main/remotes-ipc.test.ts src/main/remotes-main.test.ts` on the current `main` (`c9a0adb2`): green, 47 tests across the four suites. No Go or OpenAPI surface is touched.

## Artifacts (if appropriate):

No renderable surface: Electron main-process store, request layer and IPC bridge, with no renderer UI in this PR. Covered by the unit tests above.
