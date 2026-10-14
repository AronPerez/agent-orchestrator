## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4370, #4376, #4377 — cumulative diff over the merge of all three until they land.

## Problem

With multiple hosts connected, the sidebar still shows one machine's projects, and a failed host has no visible state: a failed remote silently blanks the tree, a failed *local* host is hidden the same way, and a hostile or outdated daemon answering with an HTML page or wrong-shape JSON can throw into the renderer.

## Solution

The sidebar draws every connected host as its own section; a host switcher filters the view once there is more than one, and is a view filter only — it never reconnects a host or changes where an action is sent. Each of the three failure modes above renders as a labelled, retryable section instead of blanking or throwing. Three telemetry events cover the ways the remote path goes dark, with `host_id` hashed before it leaves the machine.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer` on the current `main` (`c9a0adb2`): 147 files / 2109 tests, all green (integration base was 2107). New: a failed host keeps the other's rows; a failed local host is reported as loudly as remote; a dropped stream announces itself without accusing the healthy host; all eight locales gain the same fourteen keys.

## Artifacts (if appropriate):

Evidence pending — opens draft ahead of capture; a screenshot of the multi-host sidebar with one host in a failed, retryable state lands here.
