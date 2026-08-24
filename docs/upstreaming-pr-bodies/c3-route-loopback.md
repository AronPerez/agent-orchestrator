## What

The LAN listener blocks loopback-only control prefixes (`/shutdown`, `/internal/`, `/api/v1/mobile`, `/api/v1/dev`, `/api/v1/system/install`) before auth and before the router. It answered `ROUTE_NOT_FOUND`; it now answers `ROUTE_LOOPBACK_ONLY`, still at status 404.

## Why

Part of the CLI half of the remote-hosts series proposed in #RFC. A deliberate policy block was reported with the code that means "this endpoint does not exist" — so an operator who hits one goes and audits daemon builds and finds nothing wrong, because nothing is wrong. With the CLI able to target a daemon over the network, that misdiagnosis is now reachable by anyone running `ao … --url`.

## How

`loopbackOnlyJSON` writes the same locked envelope at the same status with a code and a message that say the route is served on the loopback listener only and where to run the command instead. The status stays 404 deliberately: the route genuinely is not mounted on this listener — the LAN handler chain is a different one — and holding the status keeps every existing client's error handling working. The code and the message carry the diagnosis.

The block stays outermost, in front of `authMiddleware`, and that placement is load-bearing: no code below — not auth, not the router's middleware stack — runs for a blocked path, whatever headers the caller forged. A message only an authenticated caller could see would mean moving the block inside auth or evaluating the credential twice, trading a structural guarantee for wording.

What an unauthenticated caller learns is a constant of the AO build (the prefix list, which is in the source), never a fact about this machine. Every path under a blocked prefix gets the identical response whether or not a handler exists behind it, so the block is not a route-table oracle for anyone who can reach the socket. A test pins exactly that: a registered blocked route and an absent path under the same prefix differ only where the message echoes the caller's own path back.

The loopback listener serves the shared router directly and is untouched.

`ao preview` also renders this block; the one-sentence comment update in `cli/preview.go` rides with the `--url` refusal PR instead, so this PR stays daemon-side.

## Testing

`cd backend && go build ./... && go vet ./... && go test ./... && go test -race ./internal/httpd/ ./internal/cli/`; `gofmt -l` clean. Four new tests: a blocked route answers 404 `ROUTE_LOOPBACK_ONLY` while a genuinely absent route still answers 404 `ROUTE_NOT_FOUND` and an ordinary route still answers 200; the same route over the loopback listener is unchanged; an unauthenticated caller cannot tell a registered blocked route from an absent path under the same prefix, and outside the prefixes is still 401; and one end-to-end case that stands up the real LAN listener and drives the real CLI through `--url`, so the daemon's wire format and the CLI's rendering cannot drift apart — it also asserts the blocked request never reached the router. The existing `TestLANManagerBlocksLoopbackOnlyControlRoutes` continues to pass unchanged.

No frontend file and no OpenAPI surface is touched, so the `frontend`, `renderer-smoke` and `api-drift` CI jobs are unaffected.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
