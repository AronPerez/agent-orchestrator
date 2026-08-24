## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4358.

## Problem

With `--url`, the daemon resolves `--path` against its own filesystem, and every implicit project signal — `AO_PROJECT_ID`, `AO_SESSION_ID`, the current directory — describes the machine running the CLI, not the remote one. Each is silently matched against the wrong host's projects, with output that looks like success, and a PR-ref lookup can fall back to running `gh` against a local checkout while addressing a remote daemon.

## Solution

Host-relative paths are refused for a remote target; absolute paths (including Windows drive-absolute and UNC forms) are judged for any host rather than the OS running the CLI. Implicit-project matching requires an explicit `--project` for a remote target. PR-ref resolution refuses its local-`gh` fallback and asks for a full URL instead. `project ls` gains a `PATH` column so "where does this project live" has an answer once the daemon isn't this machine.

## How Has This Been Tested?

`cd backend && go build ./... && go vet ./... && go test ./... && go test -race ./internal/cli/` on the current `main` (`c9a0adb2`): 158 packages ok, `gofmt -l` clean. Eight new tests cover path refusal across every host-relative/absolute form, implicit-project refusal with the explicit-`--project` escape, `ao spawn --url` refusing before any request, and four `resolvePRRef` cases.

## Artifacts (if appropriate):

No renderable surface: Go CLI code only, no files under `frontend/src`. Behaviour is covered by the tests above.
