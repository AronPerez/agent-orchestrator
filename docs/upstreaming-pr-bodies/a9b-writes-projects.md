## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4373.

## Problem

Project and orchestrator writes still go through `apiClient` by bare id, one level up from the session writes just converted. "Spawn an orchestrator for `agent-orchestrator`" has to name the machine, and a restart spinner or startup error on one host's project must not appear on the other's.

## Solution

`spawnOrchestrator(project: Ref, …)` and `restartProjectOrchestrator({project: Ref, …})`; the settings `PUT`, the project `DELETE`, the orchestrator-session `POST` and the task-composer `POST` all dispatch through `clientFor`. Per-project UI state moves to `refKey`. Project creation deliberately stays on `apiClient` — that is the folder-picker path, not a write conversion.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 142 files / 2045 tests, identical to base. This PR converts call sites; it does not add coverage, and the existing suites are the regression check.

## Known gap

**The "must not appear on the other's" half of the claim above does not hold for the startup-error banner.** QA against two real daemons (both holding a project id'd `demo-project`, the normal case — a project id is `filepath.Base(path)`) found `SessionsBoard.tsx`'s spawn-error state resets on the bare `projectId` (`useEffect(() => { setSpawnError(null); ... }, [projectId])`), not a `Ref`. A real remote-side spawn failure, once shown, stays visible after navigating to the *local* board of the identically-id'd project — reproduced twice with a same-id control (clears immediately) proving it. The file's own neighbouring effect (`orchestratorStartupError`) already compares host and id correctly; this one dependency array is the exception. Not fixed here — a fourth site for the same fix worker that closed A8a's/A8c's gaps; disclosed rather than shipped as true.

## Artifacts (if appropriate):

![A project startup error while viewing that project's board](https://raw.githubusercontent.com/AronPerez/agent-orchestrator/campaign-assets/qa-evidence/a9b-writes-projects.png)

The captured half is accurate — a real remote-side failure shown on that project's board. The "not on the other host" half is not shown, because it isn't true yet (see Known gap).

*Captured on a dev build; `--disable-web-security` bridges the dev-origin CORS gap only (production origin `app://renderer` passes the same check natively) — the daemon, proxy, credential, and traffic are all real.*
