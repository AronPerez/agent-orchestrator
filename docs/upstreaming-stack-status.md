# Upstream remote-hosts stack — status and hand-off

Two independent stacks: **Track A** (desktop multi-host, TypeScript) below, and **Track C**
(the CLI's `--url` / `AO_URL` remote target, Go) at the end of this document. They share no
code — only reviewers — and can be opened in parallel.

Built by Plan 1 (`docs/superpowers/plans/2026-08-23-upstream-remote-hosts-foundation.md`) against `upstream/main @ 6cba6344c` (2026-08-24). Branches live on `origin` (our fork); **no PR has been opened upstream**. The RFC text is `docs/upstreaming-rfc-remote-hosts.md`.

The public branch names are the clean refs below (`up-a1-flag` … `up-a5-clients`). The
`ao/agent-orchestrator-96/up-a*` twins on origin point at the same commits and are AO
session bookkeeping only — never open an upstream PR from a namespaced ref.

## Branch topology

A1, A2 and A3 are cut independently from `upstream/main` and share no code — they can be opened, reviewed and merged **in any order**. A4 builds on A3. A5 builds on a local integration merge of A1+A2+A4 (tag `up-a5-base` in the stack worktree, `/Users/amongstar/dev/agent-orchestrator-up-stack`) and opens last.

## Order of operations (human)

1. Post the RFC as an issue on `Untrivial-ai/agent-orchestrator`; ping Discord (daily sync 10:00 PM IST). Wait for a maintainer reaction — especially on its question 1, which decides whether the multi-host half (A2, A5 and later plans) proceeds as designed.
2. Once there is a reaction, open A1, A2 and A3 — any order, all three at once is fine; none depends on another and each is dark behind the flag.
3. Open A4 after A3 is squash-merged (rebase first, below). Open A5 after A1, A2 and A4 have all merged.
4. Rebase recipes (run in the stack worktree; upstream squash-merges, so always `--onto` across a merged parent, never a merge):

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

## The branches

| # | Branch (on `origin`) | SHA | Base | Upstream title | Non-test files | Tests it carries |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | `up-a1-flag` | `616dd08af` | `upstream/main` | feat(settings): add an experimental Remote hosts flag | 10 | ui-store ×3, settings switch ×1 |
| 2 | `up-a2-hosts` | `9c0010aad` | `upstream/main` | feat(hosts): host identity primitives | 1 | hosts ×5 |
| 3 | `up-a3-store` | `4c4e7e9d1` | `upstream/main` | feat(remotes): saved-host store, authenticated requests, password-free IPC | 9 | store + request ×30, ipc ×8, remotes-main ×4 |
| 4 | `up-a4-proxy` | `825dfde92` | `up-a3-store` | feat(remotes): token-gated loopback proxy for remote daemons | 9 | proxy ×15, registry ×7, remotes-main ×6 |
| 5 | `up-a5-clients` | `39fa64f23` | merge(A1, A2, A4), tag `up-a5-base` | feat(hosts): per-host API clients and flag-gated host boot | 3 | host-clients ×9, active-host ×6 |

Verified on the upstream base at build time: every listed suite green, `tsc --noEmit` and
`tsc -p tsconfig.e2e.json` clean on each branch; A5's full run was 72 files / 871 passed.

## Opening a PR (A1/A2/A3 in any order; A4 and A5 in sequence)

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
