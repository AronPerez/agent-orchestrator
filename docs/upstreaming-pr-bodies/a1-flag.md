## Ticket

No upstream issue yet. Design note: [remote hosts RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md).

## Problem

The remote-hosts series below this PR needs a way to land its early, dark slices safely: primitives, a saved-host store, and per-host clients all have to merge before there is anything a user can see or reach, and there is currently no flag to keep that work invisible while it lands in small, reviewable pieces.

## Solution

A Remote hosts switch directly below Developer Mode in Settings, modelled on it: `remoteHosts` in `ui-store`, persisted at `ao.remoteHosts`, default off. Nothing reads the flag yet — every later PR in the series checks it, so with it off there is no behaviour change at all beyond the one new settings row.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npx vitest run src/renderer/stores/ui-store.test.ts src/renderer/components/GeneralSettingsSection.test.tsx` on the current `main` (`c9a0adb2`): both suites green. Full `vitest run src/renderer` on this base: 2013/2014 passing, one unrelated failure in `Sidebar.test.tsx` reproduced identically on unmodified `main` under the same machine load — not caused by this change. No Go or OpenAPI surface is touched.

## Artifacts (if appropriate):

Evidence pending — opens draft ahead of capture; a screenshot of the new Settings row with the flag on lands here before review is requested.
