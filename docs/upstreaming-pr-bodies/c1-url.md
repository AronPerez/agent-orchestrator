## Ticket

No upstream issue yet. Design note: [remote hosts RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md).

## Problem

Every `ao` command talks to the daemon on the machine it runs on: it reads `running.json`, checks the PID is alive, and calls `127.0.0.1`. There is no way to drive a daemon on another machine, so a second box can only be operated by opening an SSH session to it first. Anyone running agents across more than one machine pays that tax on every command.

## Solution

A persistent `--url` flag (env `AO_URL`) points the same commands at another machine's LAN listener, skipping the run-file and the local liveness check, so it works from a machine that has never run AO. The credential is the daemon's existing connection password sent as a Bearer token. With no `--url` and no `AO_URL`, every path is the one it has always been.

## How Has This Been Tested?

`cd backend && go build ./... && go vet ./... && go test ./...` on the current `main` (`c9a0adb2`): 158 packages ok, 0 failures, `gofmt -l` empty. 15 new tests in `internal/cli/remote_test.go` cover scheme validation, userinfo rejection (including that the error never echoes the credential), the local default, `AO_TOKEN` precedence over `remotes.json`, loose-permission refusal, run-file use on the local path and its absence on the remote one, remote `ao status` including the lockout case, both `ao stop` refusals, and the telemetry drop. No frontend file and no OpenAPI surface is touched.

## Artifacts (if appropriate):

No renderable surface: this PR is Go CLI code only, no files under `frontend/src`. Behaviour is covered by the tests above.

## Implementation notes

The credential comes from `AO_TOKEN`, or from `~/.ao/remotes.json`, which mirrors the mobile app's saved-node list and must be mode 0600 — the CLI refuses to read it otherwise (Windows is exempt, where the mode bits carry no meaning). An entry whose URL does not parse is skipped rather than fatal, so a hand-edited or SSH-style entry cannot break every other one.

The URL must not carry userinfo. It is rejected textually, before `url.Parse`, so no error path can echo the password back — and the rejection is checked first, so a malformed URL cannot smuggle a credential into a parse error. The scheme-less form (`user:pw@host:3011`) is caught too; an `@` in the path or query is not authority and still parses.

`ao stop` refuses a remote target outright, including one that names loopback, so the single destructive verb never changes behaviour based on how a URL happens to look. CLI telemetry on `/internal/*` is dropped rather than sent off-box — those routes are 404'd at the LAN socket by design, and invocation counts have no business reaching someone else's daemon. A 429 from the LAN listener's per-source lockout reports as `locked_out` rather than `unhealthy`: the daemon is fine, the operator mistyped a password, and reporting `unhealthy` inverts the diagnosis.

`docs/cli/README.md` gains a section covering the flag, the credential, the 0600 requirement and the plaintext-HTTP-by-design caveat.

Trade-off worth naming: this is plain HTTP against a LAN listener, matching the existing mobile client's trust boundary. It is not a substitute for a tunnel over untrusted networks, and the docs say so.
