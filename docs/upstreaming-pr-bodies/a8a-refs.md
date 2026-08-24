## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4369.

## Problem

A session or project object carries a bare id with no way to say which daemon it lives on. Two machines that both cloned this repository both number their sessions from one, so a bare id cannot say which machine to ask, and the rest of the renderer has no way to route a read to the right host.

## Solution

Every read now takes a `Ref = {host, id}` and dispatches through `clientFor(ref.host)`; every query key carries `refKey(ref)`; routes become `/host/$hostId/session/$sessionId` and `/host/$hostId/project/$projectId`, with the four legacy paths kept as redirects to the local host. Mutations are deliberately left on `apiClient` for follow-up PRs — that seam is what keeps this one readable.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 141 files / 2047 tests, all green. `tsc --noEmit` reaches zero with no cast, `@ts-ignore`, or non-null assertion added. Existing suites are the regression check — this is an identity transformation with one host.

**Update:** QA against two real daemons found the conversion missed two call sites — `SessionView.tsx` and `ShellTopbar.tsx` both still resolved the open session by bare id across every host, since `.id === sessionId` typechecks without a `Ref`. Both fixed and TDD'd (RED confirmed a session collision, GREEN after scoping the lookup to `sessionRef.host`); the test count above includes the fix.

## Artifacts (if appropriate):

![Chat-mode session view after a legacy /sessions/demo-project-3 link resolved to /host/local/session/demo-project-3](https://raw.githubusercontent.com/AronPerez/agent-orchestrator/campaign-assets/qa-evidence/a8a-refs.png)
