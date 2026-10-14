## What

The board reads every connected host at once. `useWorkspaceQuery` becomes
one query per host under `["workspaces", host]`, combined into one section
per host; each host also gets its own `EventSource` at its own base, and
an event from host B invalidates only host B's key.

## Why

Part of the remote-hosts series proposed in #RFC. With the flag off `connectedHosts()` is `[]`, so the fan-out is a loop of one: the board issues exactly the queries and opens exactly the stream it does today.

Three failure modes are the reason this is not simply a loop:

- **One sleeping host must not serialise or fail the rest.** Each host is its own query, so a host that never answers leaves every other host's section rendering. Getting this wrong is a regression that ships silently — the board simply stops showing anything.
- **A busy remote must not make the local board refetch.** Per-host invalidation is what stops one machine's event storm from spending another machine's requests.
- **A host connected after first paint must not stay invisible.** Queries are registered per host from the live host list, not captured once.

## How

`useQueries` over `[LOCAL_HOST, ...connectedHosts()]`, combined into `HostSection[]` — each section carrying its host, label, status, stream state, workspaces, and failure. A failed host reports its last good workspaces with `status: "failed"` rather than throwing, so failure is data the UI can render rather than an exception that unmounts a tree.

`lib/host-events.ts` owns the streams: one `EventSource` per host, re-synced when the host list changes, and a connection-state transition reported once per transition rather than once per `onerror` (the browser fires that repeatedly while it retries).

Two signals ride along because the remote path had none at all. A host whose event stream drops, and a host whose workspace fetch fails, were both invisible: remote clients are plain `openapi-fetch` clients and never reach `api-client`'s `ao.renderer.api_error`. A host id is a URL, so it reaches PostHog only as a digest — `sanitizeRendererProperties` gains an explicit allowlist case per event rather than passing properties through, since an event with no case emits with every property stripped.

`test/fake-daemon.ts` regains its `slow` and `route-missing` behaviours and its `/api/v1/fs/dirs` healthy case. `slow` is what makes "one sleeping host cannot stall the others" falsifiable — without it a slow host is indistinguishable from a fast one in a test. (That file is introduced by the host-UI PR; this one restores behaviours trimmed from it.)

## Testing

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` — 142 files, 2048 tests, against 140 / 2025 on the base.

New coverage: `lib/host-events.test.ts` (one stream per host at each host's own base; an event reports which host it came from; one host's stream failing leaves the others connected; dropping a host closes only its stream; re-syncing does not churn live streams; a host that never opened a stream is distinguished from one whose stream dropped) and a rewritten `useWorkspaceQuery.test.tsx` driving the fan-out against `fake-daemon`, including hostile-daemon shapes.

No Go or OpenAPI surface is touched.

## Reviewer focus

The one thing worth checking in every invalidation site: the key moved from `["workspaces"]` to `["workspaces", host]`. `invalidateQueries({queryKey: ["workspaces"]})` is left in place deliberately — it is a *prefix* match, so a write still refreshes every host, which is what a write wants.
