# Upstream remote-hosts stack — status and hand-off

Two independent stacks: **Track A** (desktop multi-host, TypeScript) below, and **Track C**
(the CLI's `--url` / `AO_URL` remote target, Go) at the end of this document. They share no
code — only reviewers — and can be opened in parallel.

Built by Plan 1 (`docs/superpowers/plans/2026-08-23-upstream-remote-hosts-foundation.md`, branches A1–A5), Plan 2 (`docs/superpowers/plans/2026-08-24-upstream-host-ui-fs.md`, branches A6–A7b) and Plan 3 (`docs/superpowers/plans/2026-08-24-upstream-hosts-wave3.md`, branches A8a–A11) against `upstream/main @ 6cba6344c` (2026-08-24). Branches live on `origin` (our fork); **no PR has been opened upstream**. The RFC text is `docs/upstreaming-rfc-remote-hosts.md`.

There are **sixteen** branches in Track A. The public names are the clean refs below (`up-a1-flag` … `up-a11-docs`). The
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

## Upstream drift — rebased and closed out (2026-08-24)

The stack was originally cut against `upstream/main @ 6cba6344c`. By the time the campaign
began executing this runbook, upstream had moved to **`c9a0adb21`** — 9 commits ahead, not
the 4 recorded above when this note was first written (upstream kept moving between
hand-offs):

    c9a0adb21 fix(desktop): bundle tmux on macOS and Linux (#4293)
    1880498c8 fix(session): escape inconclusive agent switch recovery (#4269)
    75dfa21e8 feat(observability): Sentry-ready error capture + triage classification (mobile) (#4181)
    9df8862dc feat(observability): daemon Sentry — capture 5xx and panics with Go stacks (#4346)
    48045a10f fix(desktop): make startup layout render together (#4330)
    346b53336 fix(usage): accept switch-sized Codex metadata (#4345)
    2fa9672c7 feat: add native Kimi ACP chat driver (#4193)
    f20411d14 feat: add OMP ACP chat driver (#4196)
    0fed9c46a fix: align sidebar status dots with board lanes (#4026)

**All 21 branches have been rebased onto `c9a0adb21`, re-verified, and pushed.** The SHAs in
the tables below are current as of that rebase. Full detail — every conflict and its
resolution, the re-verification methodology (including two load-flake false failures caught
by A/B and fully-isolated re-runs before trusting a "clean" result), and the scrub pass that
removed `Claude-Session:` trailers from every commit message — is in the campaign ledger:
`/Users/amongstar/.ao/data/briefs/agentic-devops/upstream-campaign-ledger.md`, Entries 3–4.
Three integration tags were rebuilt: `up-a5-base` → `71a515d85`, `up-a7b-base` → `43b217500`,
`up-a10-base` → `6e0a0c175`.

The predicted `Sidebar.tsx` conflict on A10 did not occur — A8a and A9a absorbed that file
first via their own conflicts, and by the time A10's rebase ran, `0fed9c46a`'s change and the
host-section rewrite no longer touched the same lines. The one substantive resolution worth
knowing about: A9c's rebase dropped a one-line edit inside `routes/_shell.tsx`'s
`agentCatalogPortRef` effect, because upstream's `48045a10f` deleted that effect entirely —
A9c's edit is obsolete by construction, not lost.

## Second cascade — SSE header-flush fix (2026-08-24, after Phase 2 opened)

QA-evidence capture against two real daemons found `frontend/src/main/remote-proxy.ts` never
flushed SSE response headers before the first body byte (`res.writeHead(...)` then
`upstreamRes.pipe(res)`, no `res.flushHeaders()`), so a remote host's live-updates stream sat
in `CONNECTING` forever. Fixed on A4 with one `res.flushHeaders()` call plus a pinned timing
regression test, then cascaded through every descendant a second time: rebuilt `up-a5-base`
(now `e84c1a44c`), `up-a7b-base` (now `52cad6dfa`), `up-a10-base` (now `9dd6718e2`), and
re-rebased A5 → A6/A8a → A7b/A8b/A9a → A8c/A9b → A9c → A10 in dependency order. Full detail —
the false-positive regression test that needed its own fix before it caught the bug, the
minimal repro proving the fix, and every ground-truth-verified push — is in the campaign
ledger, Entries 13–16. A4's own PR body carries an "Update" note disclosing the fix.

A second, larger bug surfaced during the same QA pass: `SessionView.tsx` and `ShellTopbar.tsx`
resolve a session by bare id across every host, with no `Ref` check — a gap in A8a's read
conversion. Disclosed as a "Known gap" on A8a (#4372), A8c (#4376) and A9c (#4377); a
dedicated fix worker is TDD-ing both call sites on a branch cut from `up-a8a-refs`, to be
folded in via one more deliberate cascade once delivered. **A8c and A9c hold their QA
evidence** until that lands — screenshots are not produced for a claim already known to be
false.

## Open PRs (draft, `AronPerez` → `Untrivial-ai/agent-orchestrator`, stacked-drafts mode)

All 21 are open as drafts, in dependency order, using cumulative diffs for chained branches
(each stacked PR's body names "Stacked on #N" for its parent(s) rather than waiting for the
parent to merge, per the human's explicit stacked-drafts choice). Frontend-surface PRs open
before their QA-evidence screenshot is captured and are held from ready/review-request until
it is embedded — see the ledger for the evidence-capture plan and status per PR.

**QA-evidence images live on the orphan branch `campaign-assets`** (this fork, no shared
history with `main` — `git checkout --orphan`), under `qa-evidence/<pr-slug>.png`, linked
into each PR's Artifacts section via `raw.githubusercontent.com/AronPerez/agent-orchestrator/
campaign-assets/qa-evidence/<file>.png`. **`campaign-assets` must never be deleted while any
of #4358–#4378 are open** — every embedded screenshot resolves through it, and deleting the
branch breaks every image in every open PR's body simultaneously.

| Branch | PR |
| --- | --- |
| `up-a1-flag` | [#4366](https://github.com/Untrivial-ai/agent-orchestrator/pull/4366) |
| `up-a2-hosts` | [#4365](https://github.com/Untrivial-ai/agent-orchestrator/pull/4365) |
| `up-a3-store` | [#4367](https://github.com/Untrivial-ai/agent-orchestrator/pull/4367) |
| `up-a4-proxy` | [#4368](https://github.com/Untrivial-ai/agent-orchestrator/pull/4368) |
| `up-a5-clients` | [#4369](https://github.com/Untrivial-ai/agent-orchestrator/pull/4369) |
| `up-a6-host-ui` | [#4370](https://github.com/Untrivial-ai/agent-orchestrator/pull/4370) |
| `up-a7a-fs-dirs` | [#4359](https://github.com/Untrivial-ai/agent-orchestrator/pull/4359) |
| `up-a7b-folder-picker` | [#4371](https://github.com/Untrivial-ai/agent-orchestrator/pull/4371) |
| `up-a8a-refs` | [#4372](https://github.com/Untrivial-ai/agent-orchestrator/pull/4372) |
| `up-a8b-fanout` | [#4374](https://github.com/Untrivial-ai/agent-orchestrator/pull/4374) |
| `up-a8c-terminals` | [#4376](https://github.com/Untrivial-ai/agent-orchestrator/pull/4376) |
| `up-a9a-writes-sessions` | [#4373](https://github.com/Untrivial-ai/agent-orchestrator/pull/4373) |
| `up-a9b-writes-projects` | [#4375](https://github.com/Untrivial-ai/agent-orchestrator/pull/4375) |
| `up-a9c-writes-reviews` | [#4377](https://github.com/Untrivial-ai/agent-orchestrator/pull/4377) |
| `up-a10-one-tree` | [#4378](https://github.com/Untrivial-ai/agent-orchestrator/pull/4378) |
| `up-a11-docs` | [#4360](https://github.com/Untrivial-ai/agent-orchestrator/pull/4360) |
| `up-c1-url` | [#4358](https://github.com/Untrivial-ai/agent-orchestrator/pull/4358) |
| `up-c2a-refuse-local` | [#4361](https://github.com/Untrivial-ai/agent-orchestrator/pull/4361) |
| `up-c2b-remote-path` | [#4362](https://github.com/Untrivial-ai/agent-orchestrator/pull/4362) |
| `up-c2c-name-daemon` | [#4363](https://github.com/Untrivial-ai/agent-orchestrator/pull/4363) |
| `up-c3-route-loopback` | [#4364](https://github.com/Untrivial-ai/agent-orchestrator/pull/4364) |

PR bodies now follow the skyvern-cloud template (Ticket / Problem / Solution / How Has This
Been Tested / Artifacts) instead of the What/Why/How/Testing/Checklist shape below — the
files in `docs/upstreaming-pr-bodies/` are the current, validated versions; treat the
descriptions in this document as historical design record, not the literal PR text.

## The branches

| # | Branch (on `origin`) | SHA | Base | Upstream title | Non-test files | Tests it carries |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `up-a1-flag` | `4a2970469` | `upstream/main` | feat(settings): add an experimental Remote hosts flag | 10 | ui-store ×3, settings switch ×1 |
| 2 | `up-a2-hosts` | `881a65c78` | `upstream/main` | feat(hosts): host identity primitives | 1 | hosts ×5 |
| 3 | `up-a3-store` | `0d94b9e92` | `upstream/main` | feat(remotes): saved-host store, authenticated requests, password-free IPC | 9 | store + request ×30, ipc ×8, remotes-main ×4 |
| 4 | `up-a4-proxy` | `67c76da38` | `up-a3-store` | feat(remotes): token-gated loopback proxy for remote daemons | 9 | proxy ×15, registry ×7, remotes-main ×6 |
| 5 | `up-a5-clients` | `3f93b7a32` | merge(A1, A2, A4), tag `up-a5-base` | feat(hosts): per-host API clients and flag-gated host boot | 3 | host-clients ×9, active-host ×6 |
| 6 | `up-a6-host-ui` | `13125a9c3` | `up-a5-clients` | feat(hosts): add, edit and remove remote hosts | 14 | useRemoteHosts ×5, HostSelect ×12, AddRemoteHostDialog ×20, CreateProjectFlowHosts ×6, fake-daemon ×5 |
| 7 | `up-a7a-fs-dirs` | `eb9f8dd8c` | `upstream/main` | feat(daemon): read-only directory listing at GET /api/v1/fs/dirs | 6 | fs ×4 (Go), LAN policy assertion |
| 8 | `up-a7b-folder-picker` | `70b778f6f` | merge(A6, A7a), tag `up-a7b-base` | feat(projects): browse a remote host's folders when adding a project | 11 | RemoteFolderPicker ×9, CreateProjectFlow.remote ×8 |
| 9 | `up-a8a-refs` | `758a05347` | `up-a5-clients` | refactor(hosts): thread Ref through reads and host-qualified routes | 50 | `src/renderer` 140 files / 2025 tests (base 139 / 2019) |
| 10 | `up-a8b-fanout` | `05888d052` | `up-a8a-refs` | feat(hosts): fan out workspace queries and event streams per host | 21 | 142 / 2048; adds host-events ×8, fake-daemon ×7, rewritten useWorkspaceQuery |
| 11 | `up-a8c-terminals` | `b32688663` | `up-a8b-fanout` | feat(hosts): one terminal mux per host | 9 | 142 / 2051; adds mux-across-hosts ×3 |
| 12 | `up-a9a-writes-sessions` | `5ddd1203c` | `up-a8a-refs` | refactor(hosts): route session and terminal writes by Ref | 22 | 141 / 2030; adds session-writes-by-ref ×5 |
| 13 | `up-a9b-writes-projects` | `d2f0afff4` | `up-a9a-writes-sessions` | refactor(hosts): route project and orchestrator writes by Ref | 11 | 141 / 2030 |
| 14 | `up-a9c-writes-reviews` | `f8deecf15` | `up-a9b-writes-projects` | refactor(hosts): route pull request and review writes by Ref | 12 | 141 / 2030 |
| 15 | `up-a10-one-tree` | `5a8193c5d` | merge(A6, A8c, A9c), tag `up-a10-base` | feat(hosts): one tree across every connected host | 17 | 147 / 2113; adds Sidebar-across-hosts ×5, host_connect telemetry ×1 |
| 16 | `up-a11-docs` | `79ba70131` | `upstream/main` | docs(remote-hosts): setup, trust boundary, ADR | 5 | none — documentation only |

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

---

# Track C — CLI remote target (`--url` / `AO_URL`)

Built by Plan C (`docs/superpowers/plans/2026-08-24-upstream-cli-url.md`) against the same `upstream/main @ 6cba6344c` (2026-08-24). Go only: `backend/internal/cli/**` plus one function in `backend/internal/httpd/lan_listener.go`. It shares no code with Track A and can be opened in parallel with it — same reviewers, different files. Branches live on `origin`; **no PR has been opened upstream**.

The branch names below are the only refs — there are no AO session-namespaced twins for Track C, because `plan/2026-08-24-cli` exists as a branch and git refuses a nested ref under it (`directory file conflict`).

## Branch topology

`up-c1-url` is the root. `up-c2a-refuse-local`, `up-c2b-remote-path`, `up-c2c-name-daemon` and `up-c3-route-loopback` are cut from it as **independent siblings** — no branch calls another's helpers, and each is separately reviewable as "no `--url` ⇒ no change". C3 sits on C1 rather than on `upstream/main` because its end-to-end test drives the real LAN listener through the real CLI with `--url`; the daemon hunk itself is independent.

## Order of operations (human)

1. Open C1 with (or just after) the RFC. It is the only Track C PR that adds a surface; the other four are bug fixes in behaviour it creates, and reviewing them before C1 exists reads as noise.
2. Once C1 is squash-merged, rebase all four siblings onto `upstream/main` and open them — any order, all four at once is fine.
3. Rebase recipe (upstream squash-merges, so always `--onto` across a merged parent, never a merge):

       git fetch upstream
       git rebase --onto upstream/main up-c1-url up-c2a-refuse-local
       git push --force-with-lease origin up-c2a-refuse-local
       # (same two lines for up-c2b-remote-path, up-c2c-name-daemon, up-c3-route-loopback)

4. **Expected conflicts between the siblings, and how to resolve them.** C2a, C2b and C2c each append helpers to `remote.go` immediately above `// authorize presents the remote connection password.`, and each appends tests to the end of `remote_test.go`. Once one merges, the next one's rebase conflicts there. Every such conflict is **additive — keep both sides**; no two siblings modify the same lines. The one exception worth reading carefully: C2b and C2c both touch `project.go`'s `project add` success line. C2c owns it (it appends `ctx.resolvedBySuffix()`); C2b deliberately leaves it at upstream's form. If C2c merges first, C2b's rebase must keep C2c's version.

## The branches

| # | Branch (on `origin`) | SHA | Base | Upstream title | Non-test files | Tests it carries |
| --- | --- | --- | --- | --- | --- | --- |
| C1 | `up-c1-url` | `6edda5648` | `upstream/main` | feat(cli): target a remote daemon with --url / AO_URL | 6 + `docs/cli/README.md` | remote ×15 |
| C2a | `up-c2a-refuse-local` | `75accf4a7` | `up-c1-url` | fix(cli): refuse --url on commands that only ever act on this machine | 9 | remote ×3, callbacks ×4, dev ×1 |
| C2b | `up-c2b-remote-path` | `ee42b9114` | `up-c1-url` | fix(cli): judge a remote --path and a remote project by the remote host's rules | 4 | remote ×3, project ×1, pr_ref ×4 |
| C2c | `up-c2c-name-daemon` | `ac5982054` | `up-c1-url` | fix(cli): name the daemon in destructive prompts and success lines | 3 | remote ×1, project ×1, session ×2 |
| C3 | `up-c3-route-loopback` | `399525e27` | `up-c1-url` | fix(daemon): distinguish a LAN policy block from a missing route | 1 | lan_listener ×3, cli end-to-end ×1 |

Verified on the upstream base at build time, on every branch: `go build ./...`, `go vet ./...`, `go test ./...` and `go test -race` over the touched packages all green, `gofmt -l` empty, and the branch's own named tests confirmed RED before the implementation went in.

**One upstream flake to expect, not ours.** `TestSessionSwitchAgentCancellationAndOverallTimeout/overall_timeout_includes_recovery_command` (`internal/cli/session_switch_test.go`) sets a 20 ms overall wait; under machine load the first HTTP round trip can exceed it and the raw `context deadline exceeded` surfaces instead of the expected message. Measured at ~2 failures per 300 runs **on clean `upstream/main`** as well as on these branches, so it is not a Track C regression. Nothing in this stack touches `session_switch.go`.

## Running the Go suite on a machine with an AO session

`internal/cli` reads ambient `AO_*` environment. On a box with a live AO session (`AO_SESSION_ID`, `AO_PROJECT_ID`, `AO_DATA_DIR`, `AO_RUN_FILE`, `AO_RUNTIME_LAUNCH_ID`, `AO_BROWSER_CAPABILITY`, …) that produces ~15 failures that are pure harness leakage — CI is unaffected. Clear all of it, and note that **zsh does not word-split an unquoted parameter**, so `env $UNSET go test` silently clears nothing and the array form is required:

    args=(); for v in $(env | grep -o '^AO_[A-Z_0-9]*'); do args+=(-u "$v"); done
    env "${args[@]}" env | grep -c '^AO_'      # must print 0
    env "${args[@]}" go test ./...

## Opening a PR (C1 first; the other four in any order after it merges)

    gh pr create --repo Untrivial-ai/agent-orchestrator --base main --draft \
      --head AronPerez:up-c1-url \
      --title "feat(cli): target a remote daemon with --url / AO_URL" \
      --body-file docs/upstreaming-pr-bodies/c1-url.md

`--draft` per the 2026-08-24 directive: this is a stack, and nothing signals mergeable until
the order is settled. Mark ready with `gh pr ready <n>` when a PR is genuinely up for review.

Bodies follow upstream's template (What / Why / How / Testing / Checklist) and are in `docs/upstreaming-pr-bodies/`. Each body's "Why" links the RFC issue number once it exists — fill `#RFC` in before opening.

## What a reviewer can verify with no `--url` and no `AO_URL`

- `resolveRemoteTarget("")` returns `nil` and every daemon call goes through the run-file path it always did (`TestResolveRemoteTargetDefaultsToLocal`, `TestDaemonBaseLocalUsesRunFile`, C1).
- Every guard added by C2a/C2b/C2c returns `nil` on its first line (`TestLocalOnlyCommandsUnchangedWithoutRemoteTarget`, `TestDaemonLocalCallbacksUnchangedWithoutRemoteTarget`, `TestResolvedBySuffix`, and the local halves of `TestCheckRemoteProjectPathRefusesHostRelative` / `TestCheckRemoteImplicitProjectRefusesLocalSignals`).
- Local output is asserted as exact literals, not substrings, wherever a suffix could have crept in (C2c).
- `ao status` against a local daemon still reports `unhealthy` for the same HTTP status a remote target reports `locked_out` for (`TestStatusLocalUnchangedForSameHTTPStatus`, C1).
- The loopback listener is untouched by C3 (`TestLoopbackStillServesBlockedRoute`).
