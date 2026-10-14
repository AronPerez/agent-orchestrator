## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4358.

## Problem

`ao session kill`, `ao session cleanup` and `ao project rm` hit the right daemon but say nothing about which one. Neither a session id nor a project id is host-qualified — the same id can exist on two daemons — so a destructive prompt like "Remove project `api`?" cannot answer "on whose machine?", and a successful `project add` echo looks identical whether it resolved locally or remotely.

## Solution

One helper, `resolvedBySuffix()`, returns `" on the remote daemon at <url>"` for a remote target and `""` for a local one. The three destructive prompts and their success lines, plus the `project add` path echo, carry it. Because the suffix is empty locally, local output is unchanged — tests assert the local forms as exact literals so a future change that appends anything fails here rather than drifting silently.

## How Has This Been Tested?

`cd backend && go build ./... && go vet ./... && go test ./... && go test -race ./internal/cli/` on the current `main` (`c9a0adb2`): 158 packages ok, `gofmt -l` clean. Four new tests cover the helper itself and the prompt-plus-output pairs for all three commands, each asserting both the remote form and the exact local literal.

## Artifacts (if appropriate):

No renderable surface: Go CLI code only, no files under `frontend/src`. Behaviour is covered by the tests above.
