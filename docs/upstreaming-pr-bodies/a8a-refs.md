## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4369.

## Problem

A session or project object carries a bare id with no way to say which daemon it lives on. Two machines that both cloned this repository both number their sessions from one, so a bare id cannot say which machine to ask, and the rest of the renderer has no way to route a read to the right host.

## Solution

Every read now takes a `Ref = {host, id}` and dispatches through `clientFor(ref.host)`; every query key carries `refKey(ref)`; routes become `/host/$hostId/session/$sessionId` and `/host/$hostId/project/$projectId`, with the four legacy paths kept as redirects to the local host. Mutations are deliberately left on `apiClient` for follow-up PRs — that seam is what keeps this one readable.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 141 files / 2040 tests, all green. `tsc --noEmit` reaches zero with no cast, `@ts-ignore`, or non-null assertion added. Existing suites are the regression check — this is an identity transformation with one host.

## Known gap

**The conversion is incomplete in at least two call sites.** QA against two real daemons found `SessionView.tsx` (line ~306) still resolves the open session by bare id across every host — `workspaces.flatMap(w => w.sessions).find(s => s.id === sessionId)`, no `Ref`/host check — because `.id === sessionId` typechecks without one, exactly the blind spot noted above ("the compiler is not a complete oracle"). `ShellTopbar.tsx` has the identical pattern for its breadcrumb/orchestrator-badge lookup. With two hosts holding a same-id session, opening the remote one renders the local one's content. A fix (both sites, TDD'd) is in progress; this note is removed once it lands.

## Artifacts (if appropriate):

![Chat-mode session view after a legacy /sessions/demo-project-3 link resolved to /host/local/session/demo-project-3](https://raw.githubusercontent.com/AronPerez/agent-orchestrator/campaign-assets/qa-evidence/a8a-refs.png)

This capture does not exercise the gap above (no colliding ids in this flow); it stays accurate as evidence for the redirect behavior it documents.
