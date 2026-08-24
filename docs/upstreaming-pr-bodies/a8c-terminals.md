## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4374.

## Problem

The terminal mux pool holds one current socket for the whole app. With two hosts, opening a terminal on the second machine silently retires the first machine's connection, and a remote host's mux URL loses its loopback-proxy path prefix — dropping the token the proxy authenticates on.

## Solution

`createTerminalMuxPool` holds a `Map<HostId, Connection>` instead of one `current`; `muxUrlForHost(host)` derives from that host's own base, so the proxy token survives in the path. Retained terminals are keyed by `Ref`, and `useShellTerminals` becomes per host in full, mutations included.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 143 files / 2077 tests, all green (base was 143/2063). New cases: each host's mux URL keeps its token segment; the pool keeps one live socket per host; closing one host's mux leaves the other open.

**Update:** QA found "`useShellTerminals` becomes per host in full" overstated the UI's reach — `useConnectedShellTerminals()` existed but had zero consumers, so no surface actually showed a remote host's shell terminal. Fixed: `ShellTerminalsView` now uses it for the standalone list, and `SessionView` calls `useShellTerminals(sessionRef.host)`. Two lines of production code; the test count above includes the RED-then-GREEN coverage.

## Artifacts (if appropriate):

![Standalone terminals list, remote host's shell active — cwd is the remote checkout](https://raw.githubusercontent.com/AronPerez/agent-orchestrator/campaign-assets/qa-evidence/a8c-terminals.png)
![Same tab strip, local shell active — cwd is the local checkout](https://raw.githubusercontent.com/AronPerez/agent-orchestrator/campaign-assets/qa-evidence/a8c-terminals-2.png)

Real PTYs on two real daemons (`cwd` in-frame is the discriminator). One caveat visible above: the tab strip doesn't name which host a shell belongs to — both default to the project title. Real UX gap, not a capture artifact.

*Captured on a dev build; `--disable-web-security` bridges the dev-origin CORS gap only (production origin `app://renderer` passes the same check natively) — the daemon, proxy, credential, and traffic are all real.*
