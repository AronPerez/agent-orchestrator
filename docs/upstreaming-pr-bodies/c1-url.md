## What

Every `ao` command talks to the local daemon by reading `running.json`, checking the PID is alive and calling `127.0.0.1`. The new persistent `--url` flag (env `AO_URL`) points the same commands at another machine's LAN listener instead, skipping the run-file and the local liveness check entirely, so it works on a machine that has never run AO.

## Why

Part of the CLI half of the remote-hosts series proposed in #RFC. Today there is no way to run `ao` against a daemon on another machine: `ao status`, `ao agent ls`, `ao spawn` and everything else are hard-wired to the loopback run-file handshake, so a second machine can only be driven by SSHing into it. The CLI stays a thin HTTP client — this changes which base URL it talks to, not what it does.

## How

The credential is the daemon's connection password, sent as `Authorization: Bearer` — the same channel the mobile client uses ([ADR 0001](docs/adr/0001-lan-listener-for-mobile.md)). It comes from `AO_TOKEN`, or from `~/.ao/remotes.json`, which mirrors the mobile app's saved-node list and must be mode 0600 (the CLI refuses to read it otherwise; Windows is exempt, where the mode bits carry no meaning). An entry whose URL does not parse is skipped rather than fatal, so a hand-edited or SSH-style entry cannot break every other one.

The URL must not carry userinfo. It is rejected textually, before `url.Parse`, so no error path can echo the password back — and the rejection is checked first, so a malformed URL cannot smuggle a credential into a parse error. The scheme-less form (`user:pw@host:3011`) is caught too; an `@` in the path or query is not authority and still parses.

`ao stop` refuses a remote target outright, including one that names loopback, so the single destructive verb never changes behaviour based on how a URL happens to look. CLI telemetry on `/internal/*` is dropped rather than sent off-box — those routes are 404'd at the LAN socket by design, and invocation counts have no business reaching someone else's daemon. A 429 from the LAN listener's per-source lockout reports as `locked_out` rather than `unhealthy`: the daemon is fine, the operator mistyped a password, and reporting `unhealthy` inverts the diagnosis. That state is unreachable against a local daemon, because the lockout lives in the authenticated listener's middleware.

With no `--url` and no `AO_URL`, `resolveRemoteTarget` returns `nil` and every path above is the one it has always been. `docs/cli/README.md` gains a section covering the flag, the credential, the 0600 requirement and the plaintext-HTTP-by-design caveat.

## Testing

`cd backend && go build ./... && go vet ./... && go test ./... && go test -race ./internal/cli/ ./internal/config/`; `gofmt -l` clean. 15 new tests in `internal/cli/remote_test.go` covering scheme validation, userinfo rejection (including that the error never echoes the credential), the local default, `AO_TOKEN` precedence over `remotes.json`, loose-permission refusal, run-file use on the local path and its absence on the remote one, remote `ao status` including the foreign-service and lockout cases, that local `ao status` is unchanged for the same HTTP status, both `ao stop` refusals, and the telemetry drop.

No frontend file and no OpenAPI surface is touched, so the `frontend`, `renderer-smoke` and `api-drift` CI jobs are unaffected.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
