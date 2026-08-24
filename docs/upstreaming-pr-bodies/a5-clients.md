## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4366, #4365, #4368.

## Problem

The flag, the host primitives, and the saved-host store all exist, but nothing in the renderer boots a connection to a saved host or knows how to address one once connected — there is no per-host API client and no point in the app's lifecycle where a saved host actually gets opened.

## Solution

`clientFor(host)` binds an `openapi-fetch` client to each connected host's proxy base, and local keeps reading the live daemon base. `initHosts()` runs after first paint and connects every saved host, but only while the Remote hosts flag is on; off means the saved-host file is never read and no proxy starts, and turning the flag off tears every remote proxy down without a restart.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npx vitest run src/renderer/lib/host-clients.test.ts src/renderer/lib/active-host.test.ts` on the current `main` (`c9a0adb2`): green, 15 tests, including `initHosts()` never calling `remotes.list` with the flag off. No Go or OpenAPI surface is touched.

## Artifacts (if appropriate):

No renderable surface: a client-binding module and boot hook, no UI change. Reviewer-verifiable: `connectedHosts()` is `[]` with the flag off, so every later fan-out is a loop of one.
