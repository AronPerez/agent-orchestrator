## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4358.

## Problem

`ao doctor`, `preview`, `import`, `start`, `dev import-projects` and the hidden `daemon` command do not fail against `--url`: they succeed, on the wrong machine, and say nothing about it. `ao doctor --url` reports the laptop's git and data dir; `ao import --url` opens the laptop's database. A command that acted on the wrong host cannot be caught after the fact — the output looks exactly like success.

## Solution

Each of those commands now refuses a remote target outright, naming the flag, the URL, and where to run it instead, at exit code 2. `ao hooks` and `ao agent-process supervise` are daemon-local callbacks and pin to the local daemon regardless of an exported `AO_URL`, since they report activity for a session that only exists on this machine.

## How Has This Been Tested?

`cd backend && go build ./... && go vet ./... && go test ./... && go test -race ./internal/cli/` on the current `main` (`c9a0adb2`): 158 packages ok, `gofmt -l` clean. Eight new tests cover the message shape, exit code, end-to-end refusals asserting an empty request log, and the daemon-local-callback split between an ignored `AO_URL` and a refused `--url`.

## Artifacts (if appropriate):

No renderable surface: Go CLI code only, no files under `frontend/src`. Behaviour is covered by the tests above.
