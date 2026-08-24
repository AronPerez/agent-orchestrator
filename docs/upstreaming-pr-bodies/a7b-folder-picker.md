## Ticket

No upstream issue. [RFC](https://github.com/AronPerez/agent-orchestrator/blob/plan/2026-08-24-wave3/docs/upstreaming-rfc-remote-hosts.md). Stacked on #4359, #4370.

## Problem

A user adding a project on a remote host has to type an absolute path blind, with no way to browse that machine's filesystem from the picker, even though the daemon now exposes a read-only directory listing for exactly this.

## Solution

Browse beside the remote path field walks the selected host's directories over `GET /api/v1/fs/dirs` and drops the chosen one into the field. Every path decision stays with the daemon; this dialog never joins, normalises or judges a path, since it may be looking at a different OS than the one it runs on. A typed path still wins if the host will not list.

## How Has This Been Tested?

`cd frontend && npm run typecheck && npm run typecheck:e2e && npx vitest run src/renderer/components/RemoteFolderPicker.test.tsx src/renderer/components/CreateProjectFlow.remote.test.tsx src/renderer/components/CreateProjectFlowHosts.test.tsx src/renderer/components/CreateProjectFlow.test.tsx` on the current `main` (`c9a0adb2`): green, 5 files / 45 tests. No Go or OpenAPI surface is touched by this PR.

## Artifacts (if appropriate):

![Browse demo-remote folder listing with Git-repo badges](https://raw.githubusercontent.com/AronPerez/agent-orchestrator/campaign-assets/qa-evidence/a7b-folder-picker.png)
![Path-on-demo-remote field with the Browse button](https://raw.githubusercontent.com/AronPerez/agent-orchestrator/campaign-assets/qa-evidence/a7b-folder-picker-browse.png)

*Captured on a dev build; `--disable-web-security` bridges the dev-origin CORS gap only (production origin `app://renderer` passes the same check natively) — the daemon, proxy, credential, and traffic are all real.*
