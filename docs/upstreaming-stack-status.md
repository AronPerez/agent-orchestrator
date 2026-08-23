# Upstream remote-hosts stack — status and hand-off

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
