## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4369.

## Problem

A session or project object carries a bare id with no way to say which daemon it lives on. Two machines that both cloned this repository both number their sessions from one, so a bare id cannot say which machine to ask, and the rest of the renderer has no way to route a read to the right host.

## Solution

Every read now takes a `Ref = {host, id}` and dispatches through `clientFor(ref.host)`; every query key carries `refKey(ref)`; routes become `/host/$hostId/session/$sessionId` and `/host/$hostId/project/$projectId`, with the four legacy paths kept as redirects to the local host. Mutations are deliberately left on `apiClient` for follow-up PRs — that seam is what keeps this one readable.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 141 files / 2040 tests, all green. `tsc --noEmit` reaches zero with no cast, `@ts-ignore`, or non-null assertion added. Existing suites are the regression check — this is an identity transformation with one host.

## Artifacts (if appropriate):

Evidence pending — opens draft ahead of capture; a screenshot proving a legacy `/sessions/$id` deep link still resolves lands here before review.
