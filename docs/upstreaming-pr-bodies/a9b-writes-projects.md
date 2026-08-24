## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4373.

## Problem

Project and orchestrator writes still go through `apiClient` by bare id, one level up from the session writes just converted. "Spawn an orchestrator for `agent-orchestrator`" has to name the machine, and a restart spinner or startup error on one host's project must not appear on the other's.

## Solution

`spawnOrchestrator(project: Ref, …)` and `restartProjectOrchestrator({project: Ref, …})`; the settings `PUT`, the project `DELETE`, the orchestrator-session `POST` and the task-composer `POST` all dispatch through `clientFor`. Per-project UI state moves to `refKey`. Project creation deliberately stays on `apiClient` — that is the folder-picker path, not a write conversion.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 142 files / 2045 tests, identical to base. This PR converts call sites; it does not add coverage, and the existing suites are the regression check.

## Artifacts (if appropriate):

![A project startup error while viewing that project's board](https://raw.githubusercontent.com/AronPerez/agent-orchestrator/campaign-assets/qa-evidence/a9b-writes-projects.png)

*Captured on a dev build; `--disable-web-security` bridges the dev-origin CORS gap only (production origin `app://renderer` passes the same check natively) — the daemon, proxy, credential, and traffic are all real.*
