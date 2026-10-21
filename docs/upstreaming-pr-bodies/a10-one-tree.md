## What

The sidebar draws every connected host in one tree. Each host is its own
section, a host switcher filters the view once there is more than one, and
the three ways a host can go wrong are all visible instead of silent.

## Why

Part of the remote-hosts series proposed in #RFC. This is the PR where the feature becomes visible — and still only with the Remote hosts flag on, since with no saved host there is exactly one section and the switcher does not render.

The three failure modes are the reason this is not just a loop over hosts. Each is a bug we shipped and had to fix:

- **A failed host must not blank the tree.** It renders as a labelled section with its own retry, and every other host keeps its rows.
- **A failed *local* host must be reported as loudly as a remote one.** The first cut hid it, which is the same bug wearing a friendlier name.
- **A hostile or older daemon must never throw into the renderer.** An HTML catch-all, wrong-shape JSON, or a port that answers 200 to everything all resolve to a failed section.

## How

The switcher is a **view filter and nothing else**: choosing a row never reconnects a host and never changes where an action is sent, because every action is still routed by its own `Ref`. A filter naming a host that has since gone away falls back to showing everything rather than hiding the tree.

A host that still answers but whose event stream dropped says so and keeps showing its projects — the board falls back to the 15-second poll and stops being live, which otherwise reads as "my agent did nothing" rather than as a connection problem. `role="status"`, not `alert`: it is degraded, not broken. A host that *never opened* a stream stays quiet; "no stream" and "stream died" are deliberately different states.

Remote rows are host-qualified in the tree, and destructive controls carry the host they act on, so two unreachable hosts do not put two identical "Retry" buttons in the same tree.

**Telemetry.** Three events cover the three ways the remote path goes dark: a host that never connects, one whose stream stops, and one whose data stops loading. None of it existed — remote clients bypass `api-client`, so a remote failure reached nothing. A host id **is** a LAN address, so it never leaves the machine: `sanitizeRendererProperties` gains an explicit allowlist case per event and hashes `host_id` exactly as `project_id` is hashed. `host_kind` (`local`/`remote`) stays in the clear, because "the local daemon's stream dropped" and "a remote's did" are different bugs. The failure *message* is deliberately absent from `host_query_failed`: it is the daemon's own error text and can carry paths. An event with no case emits with every property stripped, which is why the allowlist is the feature rather than an afterthought.

## Testing

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` — 147 files, 2113 tests, against 2107 on the integration base.

New coverage: the filter changes the view without changing routing; a failed host keeps the other host's rows and retries only its own key; a failed local host is reported as loudly as a remote one; a dropped stream is announced while that host's projects still render, and the healthy host is not accused alongside it; a host that never opened a stream stays quiet. Plus one telemetry case asserting the connect event carries `source`, `result`, `host_kind` and a duration and **no** address or password.

All eight locale files gain the same fourteen keys.

No Go or OpenAPI surface is touched.
