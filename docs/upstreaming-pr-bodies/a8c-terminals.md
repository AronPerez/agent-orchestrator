## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4374.

## Problem

The terminal mux pool holds one current socket for the whole app. With two hosts, opening a terminal on the second machine silently retires the first machine's connection, and a remote host's mux URL loses its loopback-proxy path prefix — dropping the token the proxy authenticates on.

## Solution

`createTerminalMuxPool` holds a `Map<HostId, Connection>` instead of one `current`; `muxUrlForHost(host)` derives from that host's own base, so the proxy token survives in the path. Retained terminals are keyed by `Ref`, and `useShellTerminals` becomes per host in full, mutations included.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 143 files / 2066 tests, all green (base was 143/2063). New cases: each host's mux URL keeps its token segment; the pool keeps one live socket per host; closing one host's mux leaves the other open.

## Artifacts (if appropriate):

Evidence pending — opens draft ahead of capture; a screenshot of two hosts each holding an open terminal lands here before review.
