## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4358.

## Problem

The LAN listener blocks loopback-only control prefixes (`/shutdown`, `/internal/`, `/api/v1/mobile`, `/api/v1/dev`, `/api/v1/system/install`) before auth, but answers with the same code that means "this endpoint does not exist." An operator who hits a deliberate policy block goes and audits daemon builds and finds nothing wrong — a misdiagnosis now reachable by anyone running `ao … --url`.

## Solution

The block now answers `ROUTE_LOOPBACK_ONLY` at the same 404 status, naming the diagnosis and where to run the command instead. The block stays outermost, in front of auth, so no code below runs for a blocked path regardless of forged headers; an unauthenticated caller learns only a constant of the AO build, never a fact about this machine.

## How Has This Been Tested?

`cd backend && go build ./... && go vet ./... && go test ./... && go test -race ./internal/httpd/ ./internal/cli/` on the current `main` (`c9a0adb2`): 158 packages ok, `gofmt -l` clean. Four new tests: a blocked route vs. a genuinely absent route vs. an ordinary 200; unchanged behaviour over the loopback listener; indistinguishability of blocked-vs-absent to an unauthenticated caller; and one end-to-end case driving the real CLI through `--url` against the real LAN listener.

## Artifacts (if appropriate):

No renderable surface: Go daemon code only, no files under `frontend/src`. Behaviour is covered by the tests above.
