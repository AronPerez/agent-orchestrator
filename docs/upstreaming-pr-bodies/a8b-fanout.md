## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4372.

## Problem

The board only ever reads one host. With more than one connected, a sleeping host must not stall the others, a busy remote must not make the local board refetch, and a host connected after first paint must not stay invisible — none of that is true yet because there is one query and one event stream, not one per host.

## Solution

`useWorkspaceQuery` becomes one query per host under `["workspaces", host]`, combined into one board section per host; each host gets its own `EventSource` at its own base, and an event from host B invalidates only host B's key. A failed host reports its last good workspaces with `status: "failed"` rather than throwing.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 143 files / 2063 tests, all green (base was 141/2040). New: `lib/host-events.test.ts` and a rewritten `useWorkspaceQuery.test.tsx` driving the fan-out against hostile-daemon shapes.

## Artifacts (if appropriate):

![Board showing both hosts' sections fanned out side by side, each with its own sessions](https://raw.githubusercontent.com/AronPerez/agent-orchestrator/campaign-assets/qa-evidence/a8b-fanout.png)

*Captured on a dev build; `--disable-web-security` bridges the dev-origin CORS gap only (production origin `app://renderer` passes the same check natively) — the daemon, proxy, credential, and traffic are all real.*
