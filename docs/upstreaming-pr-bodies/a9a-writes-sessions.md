## What

Session and terminal writes take a `Ref = {host, id}` and dispatch through
`clientFor(ref.host)`: rename, terminate, pin, restore, switch-agent,
interface transition, conversation, and the browser link. Mutation state
and conversation caches are keyed by `refKey` too.

## Why

Part of the remote-hosts series proposed in #RFC, and the first of three PRs that convert the write surface, split by area.

**The point is not tidiness.** A project id is `filepath.Base(path)` on every machine, so two machines that both cloned this repository both call the project `agent-orchestrator` and both number their sessions from one — `agent-orchestrator-1` exists on both. An id alone cannot say which daemon to ask, and the failure mode is acting on the wrong machine's session.

`session-writes-by-ref.test.tsx` pins exactly that: with both hosts holding a session called `agent-orchestrator-1`, every write against the remote one issues **zero** requests to the local daemon, and vice versa. It registers a real remote host base and stubs `fetch`, so it exercises the actual routing rather than a mock's shape.

With the flag off this is an identity transformation: `clientFor(LOCAL_HOST)` is the client `apiClient` already was.

## How

The same rule as the read PR, applied to mutations:

```ts
// before                                       // after
apiClient.POST(path, {                          clientFor(session.host).POST(path, {
  params: { path: { sessionId } } })              params: { path: { sessionId: session.id } } })
["conversation", sessionId]                     ["conversation", refKey(session)]
```

Pending-mutation state moves with it. `useTerminateSessionState` and the project-scoped summaries look up by `refKey`, so two hosts' pending kills cannot be mistaken for each other, and a "Killing…" indicator cannot appear on the wrong machine's row.

`tsc --noEmit` reaches zero with no cast, no `@ts-ignore` and no non-null assertion added.

## Testing

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` — 141 files, 2030 tests, against 140 / 2025 on the base. The five new tests are the colliding-id file described above.

That file is falsifiable, not decorative: pointing `renameSession` at `clientFor("local")` fails exactly one of its cases and passing again requires the routing back.

No Go or OpenAPI surface is touched.

## Note on size

22 non-test files, almost all of them one-line call-site updates the compiler enumerated. The substance is the eight converted modules; the rest is `session.id` becoming `session`.
