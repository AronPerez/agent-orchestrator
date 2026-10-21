## What

Pull request, review, diff and panel writes take a `Ref = {host, id}` and
dispatch through `clientFor(ref.host)`: the inspector's PR and review
actions, the diff-selection and files-view "send to agent" calls, the
palette's review trigger, and the browser panel's annotation queue. The
per-session panel state moves with them.

## Why

Part of the remote-hosts series proposed in #RFC — the last of three write PRs, split by area.

Two hosts' sessions that happen to share an id must not share an open inspector, a selected tab, or a browser-unseen badge. That is the same collision the session and project writes fix at the request layer, fixed here at the UI layer: `inspectorSessions` and `visibleTerminalKindBySession` are keyed by `refKey`.

With the flag off this is an identity transformation.

## How

The routing follows the same rule as the other two write PRs. The store keying is the part worth reading: every writer and reader of the per-session panel state now computes `refKey(session)`, so the store's key space is host-qualified without the store itself learning about hosts.

After this PR, every `apiClient` write left in the renderer is host-agnostic by nature — the mobile bridge, the importer, system installs, settings, notifications, and project creation all belong to the daemon this window booted.

**One bug fixed in passing.** `useBrowserAnnotationQueue` reset on the session *prop object*, and the panel re-renders with a fresh `{...session, status}` object on every activity change — so a queued annotation was dropped whenever the agent's status changed mid-send. It now resets on the session's identity.

## Testing

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` — 141 files, 2030 tests, identical to the base. The existing suites are the regression check; the tests that changed assert a call shape or a store key.

No Go or OpenAPI surface is touched.

## Known gap

`components/chat/ChatWorkspace.tsx` still reads `inspectorSessions[snapshot.sessionId]` — a bare id — because a conversation snapshot carries no host. It is a layout read only (whether the inspector is open). Naming it rather than leaving it to be found.
