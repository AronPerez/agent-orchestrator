## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4375.

## Problem

Pull request, review, diff and panel writes are the last surface still on `apiClient` by bare id. Two hosts' sessions sharing an id must not share an open inspector, a selected tab, or a browser-unseen badge — the same collision the session and project writes already fixed at the request layer, still open at the UI layer.

## Solution

PR/review actions, diff-selection "send to agent" calls, the palette's review trigger, and the browser panel's annotation queue take a `Ref` and dispatch through `clientFor`. `inspectorSessions` and `visibleTerminalKindBySession` move to `refKey`. One bug fixed in passing: `useBrowserAnnotationQueue` now resets on session identity, not a fresh prop object.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 142 files / 2052 tests. Existing suites are the regression check; changed tests assert a call shape or a store key.

**Update:** QA against two real daemons found the claim above didn't fully hold — `SessionView.tsx` still resolved "the open session" by bare id, a gap in A8a's read conversion, which this PR's `inspectorSessions`/`refKey` keying inherited. Fixed on A8a (RED confirmed the collision, GREEN after scoping to `sessionRef.host`); the test count above includes it.

## Artifacts (if appropriate):

Evidence pending — the fix above just landed; a screenshot of two hosts' inspectors staying independent for a shared session id lands here once captured against this build.
