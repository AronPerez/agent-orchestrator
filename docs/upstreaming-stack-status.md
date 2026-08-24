# Upstream remote-hosts stack — status and hand-off

Built by Plan 1 (`docs/superpowers/plans/2026-08-23-upstream-remote-hosts-foundation.md`, branches A1–A5) and Plan 2 (`docs/superpowers/plans/2026-08-24-upstream-host-ui-fs.md`, branches A6–A7b) against `upstream/main @ 6cba6344c` (2026-08-24). Branches live on `origin` (our fork); **no PR has been opened upstream**. The RFC text is `docs/upstreaming-rfc-remote-hosts.md`.

The public branch names are the clean refs below (`up-a1-flag` … `up-a7b-folder-picker`). The
`ao/agent-orchestrator-96/up-a*` twins on origin point at the same commits and are AO
session bookkeeping only — never open an upstream PR from a namespaced ref.

## Branch topology

A1, A2 and A3 are cut independently from `upstream/main` and share no code — they can be opened, reviewed and merged **in any order**. A4 builds on A3. A5 builds on a local integration merge of A1+A2+A4 (tag `up-a5-base` in the stack worktree, `/Users/amongstar/dev/agent-orchestrator-up-stack`).

A6 builds on A5 and opens after it. **A7a is a second independent root**, cut from `upstream/main`: it is Go plus the two generated API files and shares no code with anything else in the stack, so it can be opened and merged at any point — including alongside A1–A5. A7b builds on a local integration merge of A6 + A7a (tag `up-a7b-base` in the same worktree) and opens last of the eight.

## Order of operations (human)

1. Post the RFC as an issue on `Untrivial-ai/agent-orchestrator`; ping Discord (daily sync 10:00 PM IST). Wait for a maintainer reaction — especially on its question 1, which decides whether the multi-host half (A2, A5 and later plans) proceeds as designed.
2. Once there is a reaction, open A1, A2 and A3 — any order, all three at once is fine; none depends on another and each is dark behind the flag.
3. Open A4 after A3 is squash-merged (rebase first, below). Open A5 after A1, A2 and A4 have all merged.
4. Open A6 after A5 is squash-merged. A7a may be opened at any time, in parallel with any of the above. Open A7b after **both** A6 and A7a have merged.
5. Rebase recipes (run in the stack worktree; upstream squash-merges, so always `--onto` across a merged parent, never a merge):

       # A1/A2/A3 while waiting — plain drift, nothing of ours merged yet:
       git fetch upstream
       git rebase upstream/main up-a1-flag
       git push --force-with-lease origin up-a1-flag
       # (same two lines for up-a2-hosts and up-a3-store)

       # A4, after A3 merges:
       git rebase --onto upstream/main up-a3-store up-a4-proxy
       git push --force-with-lease origin up-a4-proxy

       # A5, after A1+A2+A4 merge:
       git rebase --onto upstream/main up-a5-base up-a5-clients
       git push --force-with-lease origin up-a5-clients

       # A7a while waiting — plain drift, it sits directly on upstream/main:
       git rebase upstream/main up-a7a-fs-dirs
       git push --force-with-lease origin up-a7a-fs-dirs

       # A6, after A5 merges:
       git rebase --onto upstream/main up-a5-clients up-a6-host-ui
       git push --force-with-lease origin up-a6-host-ui

       # A7b, after A6 + A7a merge:
       git rebase --onto upstream/main up-a7b-base up-a7b-folder-picker
       git push --force-with-lease origin up-a7b-folder-picker

   After rebasing A7a onto newer upstream, re-run the generators before pushing —
   `cd backend && go generate ./internal/httpd/apispec/... && cd ../frontend && npm run api:ts` —
   or `api-drift` fails on a spec built from an older tree.

## The branches

| # | Branch (on `origin`) | SHA | Base | Upstream title | Non-test files | Tests it carries |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `up-a1-flag` | `616dd08af` | `upstream/main` | feat(settings): add an experimental Remote hosts flag | 10 | ui-store ×3, settings switch ×1 |
| 2 | `up-a2-hosts` | `9c0010aad` | `upstream/main` | feat(hosts): host identity primitives | 1 | hosts ×5 |
| 3 | `up-a3-store` | `4c4e7e9d1` | `upstream/main` | feat(remotes): saved-host store, authenticated requests, password-free IPC | 9 | store + request ×30, ipc ×8, remotes-main ×4 |
| 4 | `up-a4-proxy` | `825dfde92` | `up-a3-store` | feat(remotes): token-gated loopback proxy for remote daemons | 9 | proxy ×15, registry ×7, remotes-main ×6 |
| 5 | `up-a5-clients` | `39fa64f23` | merge(A1, A2, A4), tag `up-a5-base` | feat(hosts): per-host API clients and flag-gated host boot | 3 | host-clients ×9, active-host ×6 |
| 6 | `up-a6-host-ui` | `d00d3cd2b` | `up-a5-clients` | feat(hosts): add, edit and remove remote hosts | 14 | useRemoteHosts ×5, HostSelect ×12, AddRemoteHostDialog ×20, CreateProjectFlowHosts ×6, fake-daemon ×5 |
| 7 | `up-a7a-fs-dirs` | `98604953b` | `upstream/main` | feat(daemon): read-only directory listing at GET /api/v1/fs/dirs | 6 | fs ×4 (Go), LAN policy assertion |
| 8 | `up-a7b-folder-picker` | `26d0c5db2` | merge(A6, A7a), tag `up-a7b-base` | feat(projects): browse a remote host's folders when adding a project | 11 | RemoteFolderPicker ×9, CreateProjectFlow.remote ×8 |

Verified on the upstream base at build time: every listed suite green, `tsc --noEmit` and
`tsc -p tsconfig.e2e.json` clean on each branch; A5's full run was 72 files / 871 passed.
A6: 7 files / 72 tests green, including upstream's own `CreateProjectFlow.test.tsx` as the
flag-off regression check, plus product-ui's 10 files / 64 tests. A7a: `go build`, `go vet`,
`go test ./internal/httpd/...` all green, and `npm run api` idempotent. A7b: 5 files / 45
tests green with the Go side still green through the merge.

**A7a is the only branch in the stack that touches Go or the OpenAPI surface**, so it is the
only one the `go` and `api-drift` CI jobs judge; the other seven touch neither.

A whole-suite `vitest run` is **not** a clean signal in this worktree: on untouched
`upstream/main` it reports `11 failed | 216 passed` files, every failure an
`ERR_MODULE_NOT_FOUND` in `src/landing/**` or `src/annotate-preload.test.ts` — the landing
app is a separate package this worktree never installed (there are no npm workspaces). A7b
was A/B'd against that baseline and the failure sets are identical.

## Opening a PR (A1/A2/A3 and A7a in any order; A4, A5, A6, A7b in sequence)

    gh pr create --repo Untrivial-ai/agent-orchestrator --base main \
      --head AronPerez:up-a1-flag \
      --title "feat(settings): add an experimental Remote hosts flag" \
      --body-file docs/upstreaming-pr-bodies/a1-flag.md

Bodies follow upstream's template (What / Why / How / Testing / Checklist) and are in `docs/upstreaming-pr-bodies/`. Each body's "Why" links the RFC issue number once it exists — fill `#RFC` in before opening.

## What a reviewer can verify with the flag off, on every branch

- The Settings modal shows one new row; nothing else in the UI differs (A1).
- `initHosts()` never calls `remotes.list` (`active-host.test.ts`, A5).
- Main opens no socket without an IPC call (`remote-registry.test.ts` "never connected is a no-op", A4).
- `connectedHosts()` is `[]` so every later fan-out is a loop of one (A5 onward).
- The Add-a-project flow renders exactly upstream's tree — no Host row, and `remotes.list`/`remotes.probe` are never called (`CreateProjectFlowHosts.test.tsx` "shows no host picker and contacts no saved host", A6).
- `GET /api/v1/fs/dirs` is additive and sits behind the existing connection credential; nothing calls it until a remote host is selected, which the flag gates (A7a/A7b).

## Deferred out of wave 2

Recorded so Plan 3 does not rediscover it. Each of these was cut from A6/A7b for a reason, not forgotten:

- `HostSwitcher.tsx` and its `hosts.viewing` / `hosts.allHosts` keys — it imports `HostSection` from `types/workspace.ts`, which A8a introduces and which exists on no wave-2 base. Ships with A10.
- `lib/host-telemetry.ts` and the two `reportHostConnect` call sites in `useRemoteHosts.ts` / `AddRemoteHostDialog.tsx`, plus the `AddRemoteHostDialog.test.tsx` case that asserted the event. Upstream's `sanitizeRendererProperties` is a per-event allowlist, so `ao.renderer.host_connect` would currently emit with every property stripped — adding its case is the A10 telemetry review.
- `test/fake-daemon.ts`'s `slow` and `route-missing` behaviours and its `/api/v1/fs/dirs` healthy case — no consumer before Plan 3's `useWorkspaceQuery.test.tsx`.
- Twelve further `hosts.*` keys belonging to the one-tree UI: `hosts.backToLocal`, `hosts.passwordChanged`, `hosts.on`, `hosts.remoteSection`, `hosts.open`, `hosts.unreachable`, `hosts.peekEmpty`, `hosts.qualified`, `hosts.sectionFailed`, `hosts.retry`, `hosts.liveUpdatesOffline`, `hosts.liveUpdatesOffline.hint`.
