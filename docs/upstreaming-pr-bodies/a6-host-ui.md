## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4369.

## Problem

With the flag, primitives, store, proxy and clients all merged, a user still has no way to reach any of it: there is no UI for adding, editing or removing a saved host, and no way to point a new project at a machine other than the one the app is running on.

## Solution

A Host dropdown in Add-a-project lists saved remote daemons beside This Mac with live reachability, and manages them in place — add, edit, remove — with the connection password never leaving the main process. Picking a remote host replaces the native folder dialog with an absolute-path field and registers the project against that daemon over REST; the daemon owns the verdict on its own filesystem.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer/hooks/useRemoteHosts.test.tsx src/renderer/components/HostSelect.test.tsx src/renderer/components/AddRemoteHostDialog.test.tsx src/renderer/components/CreateProjectFlowHosts.test.tsx src/renderer/components/CreateProjectFlow.test.tsx src/renderer/test/fake-daemon.test.ts` on the current `main` (`c9a0adb2`): green, 7 files / 72 tests, including the flag-off regression check. Plus `npm --prefix packages/product-ui test`: 10/64 green.

## Artifacts (if appropriate):

![Add-a-project Host dropdown listing "This Mac" and a saved, connected remote host](https://raw.githubusercontent.com/AronPerez/agent-orchestrator/campaign-assets/qa-evidence/a6-host-ui.png)

*Captured on a dev build; `--disable-web-security` bridges the dev-origin CORS gap only (production origin `app://renderer` passes the same check natively) — the daemon, proxy, credential, and traffic are all real.*
