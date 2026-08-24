## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4375.

## Problem

Pull request, review, diff and panel writes are the last surface still on `apiClient` by bare id. Two hosts' sessions sharing an id must not share an open inspector, a selected tab, or a browser-unseen badge — the same collision the session and project writes already fixed at the request layer, still open at the UI layer.

## Solution

PR/review actions, diff-selection "send to agent" calls, the palette's review trigger, and the browser panel's annotation queue take a `Ref` and dispatch through `clientFor`. `inspectorSessions` and `visibleTerminalKindBySession` move to `refKey`. One bug fixed in passing: `useBrowserAnnotationQueue` now resets on session identity, not a fresh prop object.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 142 files / 2045 tests, identical to base. Existing suites are the regression check; changed tests assert a call shape or a store key.

## Known gap

**The claim above does not fully hold yet.** QA against two real daemons found that `SessionView.tsx` still resolves "the open session" by bare id across every host (`workspaces.flatMap(w => w.sessions).find(s => s.id === sessionId)`, no host check) — a gap in A8a's read conversion that survived because `.id === sessionId` typechecks without a `Ref`. With two hosts holding a same-id session, opening the remote one currently renders the local one's content, which this PR's own `inspectorSessions`/`refKey` keying inherits. A fix is in progress on top of A8a; this PR will drop this note once it lands and evidence is captured.

## Artifacts (if appropriate):

Held — will not screenshot inspector independence while the identity bug above makes it false. Lands once the fix is folded in.
