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

## Upstream has moved since the stack was cut

Every branch here is built on `upstream/main @ 6cba6344c`. As of **2026-08-24**, upstream is at
`346b53336` — four commits ahead:

    346b53336 fix(usage): accept switch-sized Codex metadata (#4345)
    2fa9672c7 feat: add native Kimi ACP chat driver (#4193)
    f20411d14 feat: add OMP ACP chat driver (#4196)
    0fed9c46a fix: align sidebar status dots with board lanes (#4026)

Most of it is backend and does not touch this stack. **Three renderer files collide**, and one
of them is the file the one-tree PR rewrites most:

| File | Upstream change | Stack branch that also touches it |
| --- | --- | --- |
| `components/Sidebar.tsx` | status-dot alignment (`0fed9c46a`), ~17 lines | A10 (host sections, switcher, per-project host label) |
| `components/Sidebar.test.tsx` | the same change's tests, ~49 lines | A10 |
| `lib/session-presentation.test.ts` | new cases for the shared presentation helper | A8a (fixture `host` field only) |

Rebase A10 before opening it, and expect a real conflict in `Sidebar.tsx` — resolve it by
keeping both changes, never by taking a side. Nothing else in the stack is affected, and no
branch needs rebasing until it is opened.

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
| C1 | `up-c1-url` | `37ff47ed0` | `upstream/main` | feat(cli): target a remote daemon with --url / AO_URL | 6 + `docs/cli/README.md` | remote ×15 |
| C2a | `up-c2a-refuse-local` | `a54519dad` | `up-c1-url` | fix(cli): refuse --url on commands that only ever act on this machine | 9 | remote ×3, callbacks ×4, dev ×1 |
| C2b | `up-c2b-remote-path` | `5e952b35f` | `up-c1-url` | fix(cli): judge a remote --path and a remote project by the remote host's rules | 4 | remote ×3, project ×1, pr_ref ×4 |
| C2c | `up-c2c-name-daemon` | `6aa8b6352` | `up-c1-url` | fix(cli): name the daemon in destructive prompts and success lines | 3 | remote ×1, project ×1, session ×2 |
| C3 | `up-c3-route-loopback` | `69f68463c` | `up-c1-url` | fix(daemon): distinguish a LAN policy block from a missing route | 1 | lan_listener ×3, cli end-to-end ×1 |

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
