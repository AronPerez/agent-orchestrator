## Ticket

No upstream issue yet. Design note: [remote hosts RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md).

## Problem

Nothing in the codebase can name "which machine" a session or project lives on. A project id is `filepath.Base(path)` on every machine, so two machines that both cloned this repository both call the project `agent-orchestrator`, and there is no type that can qualify a bare id at the addressing boundary before the rest of the series needs one.

## Solution

`HostId`, `Ref = {host, id}`, `LOCAL_HOST`, and a composite `refKey`. Local is a host like any other, so no code path special-cases "is this remote?" — everything downstream can treat local and remote uniformly. No importer yet; this PR adds the primitives and nothing consumes them.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npx vitest run src/renderer/lib/hosts.test.ts` on the current `main` (`c9a0adb2`): green, 5 tests. No Go or OpenAPI surface is touched, so the `go` and `api-drift` CI jobs are unaffected. This file has no consumers yet, so the existing full-suite run is unaffected by construction.

## Artifacts (if appropriate):

No renderable surface: a headless type/data-structure module (`lib/hosts.ts`) with no UI consumer in this PR. Covered by the unit tests above.
