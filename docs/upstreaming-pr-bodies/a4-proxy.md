## What

The renderer cannot authenticate to a remote daemon itself: EventSource
and WebSocket cannot set Authorization, and app://renderer has no CORS
standing there. Main starts one loopback proxy per connected host bound
to 127.0.0.1 on an ephemeral port; the renderer addresses it as
http://127.0.0.1:<port>/<128-bit token>/, the proxy strips the token and
the renderer Origin, injects the saved Bearer credential, restores the
host's path prefix, speaks TLS to an https host, and streams SSE and
WebSocket frames as they arrive. A request without the token is answered
404 and forwarded nowhere. Proxies are torn down on disconnect and on
quit (tunnelled sockets included, so quit cannot hang on one).

## Why

Part of the remote-hosts series proposed in #RFC. This slice lands dark: with the Remote hosts flag off there is no behaviour change.

## How

Still dark: nothing in the renderer connects a host yet.

## Testing

`cd frontend && npm run typecheck && npx vitest run src/main/remote-proxy.test.ts src/main/remote-registry.test.ts src/main/remotes-main.test.ts` — counts as in the table in `docs/upstreaming-stack-status.md`. No Go or OpenAPI surface is touched, so the `go` and `api-drift` CI jobs are unaffected.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
