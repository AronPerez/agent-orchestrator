## What

The terminal mux pool is keyed by host instead of holding one current
socket, and each socket's URL is derived from that host's own base.
Retained terminals are keyed by ref, and shell terminals become per host.

## Why

Part of the remote-hosts series proposed in #RFC. With one host this is what the app already did: one pool entry, one socket, one shell-terminals query.

Two cases make it worth the change:

- **Two hosts need two sockets.** A single "current" connection means opening a terminal on the second machine silently retires the first machine's.
- **A remote host's mux URL must keep its path prefix.** A remote's base is the loopback proxy's `http://127.0.0.1:<port>/<token>/`. Dropping the prefix sends the socket to a 404 and — worse — drops the token the proxy authenticates on.

## How

`createTerminalMuxPool` holds a `Map<HostId, Connection>` rather than one `current`, and `acquire(host)` defaults to `LOCAL_HOST`. A socket-level failure retires only that host's client, so reconnecting leases for that host converge on one replacement socket and other hosts are untouched.

`muxUrlForHost(host)` derives from `baseUrlFor(host)`, which is the whole point: the token is in the path, so the path survives.

The terminal cache records the host each entry belongs to, and keys sessions by `refKey`. Only a host that actually answered is authoritative about its own sessions and shells — a failed section no longer evicts terminals it could not report on.

`useShellTerminals` becomes per host in full, mutations included. That is not scope creep: `shellTerminalsQueryKey` becomes `(host) => [...]`, so every invalidation has to name a host, and `ShellTerminal` gains `host`, so closing or renaming one can no longer be addressed by a bare handle id.

## Testing

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` — 142 files, 2051 tests, against 142 / 2048 on the base.

The three new cases are the ones that matter: each host's mux URL is built from that host's base with the token segment intact (`ws://127.0.0.1:9999/tok/mux`); the pool keeps one live socket per host rather than one globally; and closing one host's mux leaves the other open.

No Go or OpenAPI surface is touched.
