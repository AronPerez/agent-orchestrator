## What

Project and orchestrator writes take a `Ref = {host, id}` and dispatch
through `clientFor(ref.host)`: spawning and restarting an orchestrator,
saving project settings, deleting a project, and submitting a task. The
per-project UI state that tracks them is keyed by `refKey`.

## Why

Part of the remote-hosts series proposed in #RFC — the second of three write PRs, split by area.

Same reason as the session writes, one level up: a project id is `filepath.Base(path)` on every machine, so "spawn an orchestrator for `agent-orchestrator`" has to name the machine. The UI state matters as much as the request — a restart spinner or a startup error on one host's project must not appear on the other's.

The colliding-id test that the series exists for is in the session-writes PR; this one converts the project surface behind it. With the flag off it is an identity transformation.

## How

`spawnOrchestrator(project: Ref, …)` and `restartProjectOrchestrator({project: Ref, …})`; the settings `PUT`, the project `DELETE`, the orchestrator-session `POST` and the task-composer delegate `POST` all dispatch through `clientFor`. `restartingProjectIds`, `orchestratorStartupErrors` and `orchestratorReplacementErrors` are keyed by `refKey`.

**Project creation deliberately stays on `apiClient`.** `POST /projects`, `/projects/clone` and `/projects/initialize` are local operations against the daemon this window booted; adding a project *on a remote host* is the folder-picker path, not a write conversion.

## Testing

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` — 141 files, 2030 tests, identical to the base. This PR converts; it does not add coverage, and the existing suites are the regression check. The tests that changed assert a call shape or a store key, both of which legitimately move.

No Go or OpenAPI surface is touched.
