## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4374.

## Problem

The terminal mux pool holds one current socket for the whole app. With two hosts, opening a terminal on the second machine silently retires the first machine's connection, and a remote host's mux URL loses its loopback-proxy path prefix — dropping the token the proxy authenticates on.

## Solution

`createTerminalMuxPool` holds a `Map<HostId, Connection>` instead of one `current`; `muxUrlForHost(host)` derives from that host's own base, so the proxy token survives in the path. Retained terminals are keyed by `Ref`, and `useShellTerminals` becomes per host in full, mutations included.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 143 files / 2066 tests, all green (base was 143/2063). New cases: each host's mux URL keeps its token segment; the pool keeps one live socket per host; closing one host's mux leaves the other open.

## Known gap

**"`useShellTerminals` becomes per host in full" overstates the UI's current reach.** The hook itself does take a host, and `useConnectedShellTerminals()` exists for the multi-host case — but neither of this PR's own two consumers (`ShellTerminalsView`, `SessionView`) calls it; both call `useShellTerminals()` with no host, which defaults to local. `useConnectedShellTerminals()` has zero call sites anywhere in `src/renderer`. So today there is no UI surface that displays a remote host's shell terminal at all, colliding session ids or not. A colliding-id session additionally fails outright — "+ New terminal" errors `shell target hosts do not match` — because of the `SessionView.tsx` gap tracked on A8a. Both are being addressed together.

## Artifacts (if appropriate):

Held — will not screenshot "two hosts each holding an open terminal" while no consumer wires the multi-host hook up. Lands once the fix is folded in.
