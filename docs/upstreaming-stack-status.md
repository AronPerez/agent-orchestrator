# Upstream remote-hosts stack — status and hand-off

Built by Plan 1 (`docs/superpowers/plans/2026-08-23-upstream-remote-hosts-foundation.md`, branches A1–A5), Plan 2 (`docs/superpowers/plans/2026-08-24-upstream-host-ui-fs.md`, branches A6–A7b) and Plan 3 (`docs/superpowers/plans/2026-08-24-upstream-hosts-wave3.md`, branches A8a–A11) against `upstream/main @ 6cba6344c` (2026-08-24). Branches live on `origin` (our fork); **no PR has been opened upstream**. The RFC text is `docs/upstreaming-rfc-remote-hosts.md`.

There are **sixteen** branches. The public names are the clean refs below (`up-a1-flag` … `up-a11-docs`). The
`ao/agent-orchestrator-96/up-a*` twins on origin point at the same commits and are AO
session bookkeeping only — never open an upstream PR from a namespaced ref.

## Branch topology

A1, A2 and A3 are cut independently from `upstream/main` and share no code — they can be opened, reviewed and merged **in any order**. A4 builds on A3. A5 builds on a local integration merge of A1+A2+A4 (tag `up-a5-base` in the stack worktree, `/Users/amongstar/dev/agent-orchestrator-up-stack`).

A6 builds on A5 and opens after it. **A7a is a second independent root**, cut from `upstream/main`: it is Go plus the two generated API files and shares no code with anything else in the stack, so it can be opened and merged at any point — including alongside A1–A5. A7b builds on a local integration merge of A6 + A7a (tag `up-a7b-base` in the same worktree).

Wave 3 forks into two lines that rejoin once. A8a builds on A5 — **not on A2**, as the RFC's dependency sketch has it: every converted read calls `clientFor`, which is A5, and A5 already contains A2, so nothing is lost. A8b and A8c chain from A8a; A9a is cut from A8a as well, so the write series does not carry the fan-out and terminal work through review, and A9b/A9c chain from A9a. A10 builds on a local integration merge of A6 + A8c + A9c (tag `up-a10-base`) — A8c transitively carries A8b and A8a, and A9c transitively carries A9a and A9b. **A11 is a third independent root**, cut from `upstream/main`: it is documentation and one `AGENTS.md` paragraph, so it imports nothing and can be opened whenever — but it *describes* behaviour that only exists once A10 merges, so open it last.

## Order of operations (human)

1. Post the RFC as an issue on `Untrivial-ai/agent-orchestrator`; ping Discord (daily sync 10:00 PM IST). Wait for a maintainer reaction — especially on its question 1, which decides whether the multi-host half (A2, A5 and later plans) proceeds as designed.
2. Once there is a reaction, open A1, A2 and A3 — any order, all three at once is fine; none depends on another and each is dark behind the flag.
3. Open A4 after A3 is squash-merged (rebase first, below). Open A5 after A1, A2 and A4 have all merged.
4. Open A6 after A5 is squash-merged. A7a may be opened at any time, in parallel with any of the above. Open A7b after **both** A6 and A7a have merged.
5. Open A8a after A5 merges. **A8a and A9a are the two PRs most exposed to upstream renderer churn** — each rewrites call sites across ~30 files. Ask for a quiet window and land them within 48h of opening; the rebase recipe below is `--onto`, never a merge.
6. Then: A8b after A8a; A8c after A8b; A9a after A8a; A9b after A9a; A9c after A9b. A8b/A8c and A9a/A9b/A9c are two independent chains and can be in review at the same time.
7. Open A10 after **A6, A8c and A9c** have all merged. Open A11 last.
8. Rebase recipes (run in the stack worktree; upstream squash-merges, so always `--onto` across a merged parent, never a merge):

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

       # A8a, after A5 merges:
       git rebase --onto upstream/main up-a5-clients up-a8a-refs
       git push --force-with-lease origin up-a8a-refs

       # A8b, after A8a merges:
       git rebase --onto upstream/main up-a8a-refs up-a8b-fanout
       git push --force-with-lease origin up-a8b-fanout

       # A8c, after A8b merges:
       git rebase --onto upstream/main up-a8b-fanout up-a8c-terminals
       git push --force-with-lease origin up-a8c-terminals

       # A9a, after A8a merges (A9a is cut from A8a, not from A8c):
       git rebase --onto upstream/main up-a8a-refs up-a9a-writes-sessions
       git push --force-with-lease origin up-a9a-writes-sessions

       # A9b, after A9a merges:
       git rebase --onto upstream/main up-a9a-writes-sessions up-a9b-writes-projects
       git push --force-with-lease origin up-a9b-writes-projects

       # A9c, after A9b merges:
       git rebase --onto upstream/main up-a9b-writes-projects up-a9c-writes-reviews
       git push --force-with-lease origin up-a9c-writes-reviews

       # A10, after A6 + A8c + A9c merge — pivot on the integration tag:
       git rebase --onto upstream/main up-a10-base up-a10-one-tree
       git push --force-with-lease origin up-a10-one-tree

       # A11 while waiting — plain drift, it sits directly on upstream/main:
       git rebase upstream/main up-a11-docs
       git push --force-with-lease origin up-a11-docs

   After A8a regenerates `routeTree.gen.ts`, a rebase that touches the routes tree must
   regenerate it again rather than resolve it by hand — it is tracked but written by the
   TanStack Vite plugin. There is no `router-cli` binary; drive `@tanstack/router-generator`
   from a short node script inside `frontend/`, or fall back to `vite build --config
   vite.renderer.config.ts`.

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
| 9 | `up-a8a-refs` | `2d58b559a` | `up-a5-clients` | refactor(hosts): thread Ref through reads and host-qualified routes | 50 | `src/renderer` 140 files / 2025 tests (base 139 / 2019) |
| 10 | `up-a8b-fanout` | `17e2e98f0` | `up-a8a-refs` | feat(hosts): fan out workspace queries and event streams per host | 21 | 142 / 2048; adds host-events ×8, fake-daemon ×7, rewritten useWorkspaceQuery |
| 11 | `up-a8c-terminals` | `330bcffc4` | `up-a8b-fanout` | feat(hosts): one terminal mux per host | 9 | 142 / 2051; adds mux-across-hosts ×3 |
| 12 | `up-a9a-writes-sessions` | `a2b10f97f` | `up-a8a-refs` | refactor(hosts): route session and terminal writes by Ref | 22 | 141 / 2030; adds session-writes-by-ref ×5 |
| 13 | `up-a9b-writes-projects` | `e93a11444` | `up-a9a-writes-sessions` | refactor(hosts): route project and orchestrator writes by Ref | 11 | 141 / 2030 |
| 14 | `up-a9c-writes-reviews` | `f5cdb2554` | `up-a9b-writes-projects` | refactor(hosts): route pull request and review writes by Ref | 12 | 141 / 2030 |
| 15 | `up-a10-one-tree` | `e16a55e49` | merge(A6, A8c, A9c), tag `up-a10-base` | feat(hosts): one tree across every connected host | 17 | 147 / 2113; adds Sidebar-across-hosts ×5, host_connect telemetry ×1 |
| 16 | `up-a11-docs` | `0dd65c265` | `upstream/main` | docs(remote-hosts): setup, trust boundary, ADR | 5 | none — documentation only |

Verified on the upstream base at build time: every listed suite green, `tsc --noEmit` and
`tsc -p tsconfig.e2e.json` clean on each branch; A5's full run was 72 files / 871 passed.
A6: 7 files / 72 tests green, including upstream's own `CreateProjectFlow.test.tsx` as the
flag-off regression check, plus product-ui's 10 files / 64 tests. A7a: `go build`, `go vet`,
`go test ./internal/httpd/...` all green, and `npm run api` idempotent. A7b: 5 files / 45
tests green with the Go side still green through the merge.

Wave 3 was verified branch by branch on its own base: `tsc --noEmit` and
`tsc -p tsconfig.e2e.json` clean, and `vitest run src/renderer` fully green with the counts
in the table. The A10 integration tag `up-a10-base` was verified before any A10 content was
written (2107 tests green) so a later failure could not be blamed on the merge.

**A7a is the only branch in the stack that touches Go or the OpenAPI surface**, so it is the
only one the `go` and `api-drift` CI jobs judge; the other fifteen touch neither.

A whole-suite `vitest run` is **not** a clean signal in this worktree: on untouched
`upstream/main` it reports `11 failed | 216 passed` files, every failure an
`ERR_MODULE_NOT_FOUND` in `src/landing/**` or `src/annotate-preload.test.ts` — the landing
app is a separate package this worktree never installed (there are no npm workspaces). A7b
was A/B'd against that baseline and the failure sets are identical.

## Opening a PR (A1/A2/A3, A7a and A11 in any order; the rest in sequence)

    gh pr create --repo Untrivial-ai/agent-orchestrator --base main \
      --head AronPerez:up-a1-flag \
      --title "feat(settings): add an experimental Remote hosts flag" \
      --body-file docs/upstreaming-pr-bodies/a1-flag.md

Bodies follow upstream's template (What / Why / How / Testing / Checklist) and are in `docs/upstreaming-pr-bodies/`. Each body's "Why" links the RFC issue number once it exists — fill `#RFC` in before opening.

**Every PR opens as a draft.** Nothing in this stack is merged by us.

## What a reviewer can verify with the flag off, on every branch

- The Settings modal shows one new row; nothing else in the UI differs (A1).
- `initHosts()` never calls `remotes.list` (`active-host.test.ts`, A5).
- Main opens no socket without an IPC call (`remote-registry.test.ts` "never connected is a no-op", A4).
- `connectedHosts()` is `[]` so every later fan-out is a loop of one (A5 onward).
- The Add-a-project flow renders exactly upstream's tree — no Host row, and `remotes.list`/`remotes.probe` are never called (`CreateProjectFlowHosts.test.tsx` "shows no host picker and contacts no saved host", A6).
- `GET /api/v1/fs/dirs` is additive and sits behind the existing connection credential; nothing calls it until a remote host is selected, which the flag gates (A7a/A7b).
- Every `Ref` carries `LOCAL_HOST` and `clientFor(LOCAL_HOST)` returns the client `apiClient` already was, so A8a and A9a–c are identity transformations: a reviewer can read any hunk and check that (A8a, A9a–c).
- The workspace fan-out is a loop of one, so the board issues exactly the queries and opens exactly the stream it does today (A8b); the mux pool holds one entry (A8c).
- The host switcher only renders with more than one host, so with none it is absent (A10).
- The legacy paths `/sessions/$sessionId`, `/projects/$projectId`, `/projects/$projectId/settings` and `/projects/$projectId/sessions/$sessionId` still resolve — they redirect to the local host, and `routes/-session-route.test.ts` proves each one lands with `hostId === LOCAL_HOST` and the id preserved (A8a).

## Deferred out of wave 2 — all discharged

Nothing on this list is still open. Each item is recorded with the branch that took it, so the
list does not read as outstanding work:

- `HostSwitcher.tsx` and its `hosts.viewing` / `hosts.allHosts` keys — **A10**.
- `lib/host-telemetry.ts`, the two `reportHostConnect` call sites in `useRemoteHosts.ts` / `AddRemoteHostDialog.tsx`, and the `AddRemoteHostDialog.test.tsx` case that asserts the event — **A10**. The file itself arrives earlier: **A8b** creates it for `reportHostStreamState` and `reportHostQueryFailed`, which its own code needs, and adds their `sanitizeRendererProperties` cases; A10 adds `reportHostConnect` and its case. All three events are now allowlisted, and `host_id` is hashed on the way out.
- `test/fake-daemon.ts`'s `slow` and `route-missing` behaviours and its `/api/v1/fs/dirs` healthy case — **A8b**. Note these are *restored* into the file A6 introduces; the two lines meet at the A10 merge, where the file conflicts add/add and is resolved as A6's formatting plus A8b's behaviours.
- The remaining twelve `hosts.*` keys — **A10**, all eight locales.

## Deferred out of wave 3

One item, recorded the same way:

- `components/chat/ChatWorkspace.tsx` still reads `inspectorSessions[snapshot.sessionId]` — a bare session id — because a conversation snapshot carries no host. It is a layout read (whether the inspector is open), so two hosts' same-id sessions share only that. Everything else in that store is keyed by `refKey` as of A9c. The fork has the same gap.
