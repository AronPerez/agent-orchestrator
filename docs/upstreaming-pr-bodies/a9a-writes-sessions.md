## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4372.

## Problem

Session and terminal writes still go through `apiClient`, addressed by a bare id. Two hosts holding sessions that share a name — both call it `agent-orchestrator-1`, since a project id is `filepath.Base(path)` on every machine — have no way to route a rename, kill or agent-switch to the right one.

## Solution

Session and terminal writes take a `Ref = {host, id}` and dispatch through `clientFor(ref.host)`: rename, terminate, pin, restore, switch-agent, interface transition, conversation, and the browser link. Mutation state and conversation caches move to `refKey` too, so a "Killing…" indicator cannot appear on the wrong machine's row.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 142 files / 2045 tests, all green. The five new tests pin two hosts holding a same-named session and assert zero cross-host requests; pointing a write at the wrong client fails exactly one case.

## Artifacts (if appropriate):

![Before: local and remote hosts each holding a session named scratch-session-1](https://raw.githubusercontent.com/AronPerez/agent-orchestrator/campaign-assets/qa-evidence/a9a-writes-sessions-before.png)
![After: only the local session renamed to renamed-this-mac; the remote one is untouched](https://raw.githubusercontent.com/AronPerez/agent-orchestrator/campaign-assets/qa-evidence/a9a-writes-sessions.png)

*Captured on a dev build; `--disable-web-security` bridges the dev-origin CORS gap only (production origin `app://renderer` passes the same check natively) — the daemon, proxy, credential, and traffic are all real.*
