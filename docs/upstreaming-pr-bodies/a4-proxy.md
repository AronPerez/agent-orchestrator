## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4367.

## Problem

The renderer cannot authenticate to a remote daemon on its own: `EventSource` and `WebSocket` cannot set an `Authorization` header, and `app://renderer` has no CORS standing with a remote host, so there is no way for the UI to stream from or call a saved host once one exists.

## Solution

Main starts one loopback proxy per connected host bound to `127.0.0.1` on an ephemeral port; the renderer addresses it as `http://127.0.0.1:<port>/<token>/`, and the proxy strips the token, injects the saved Bearer credential, and streams SSE/WebSocket frames as they arrive. A request without the token gets a 404. Proxies are torn down on disconnect and on quit, tunnelled sockets included. Still dark: nothing connects a host yet.

**Update:** QA against a real second daemon found SSE responses never delivered headers until the first event byte (Node holds them until a flush or a write), so a live-updates stream sat in `CONNECTING` forever. Fixed with one `res.flushHeaders()` call, pinned with a timing regression test (red ~500ms, green <30ms).

## How Has This Been Tested?

`cd frontend && npm run typecheck && npx vitest run src/main/remote-proxy.test.ts src/main/remote-registry.test.ts src/main/remotes-main.test.ts` on the current `main` (`c9a0adb2`): green, 29 tests (28 original + the new header-flush regression) including "never connected is a no-op" and the token-mismatch 404 case. No Go or OpenAPI surface is touched.

## Artifacts (if appropriate):

No renderable surface: an Electron main-process loopback proxy with no renderer UI in this PR. Covered by the unit tests above.
