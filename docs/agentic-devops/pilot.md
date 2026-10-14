# Preflight pilot — re-verifying manager mechanics after an AO upgrade

Run this whenever `ao version` changes, before trusting an unattended epic to the playbook. It
proves, end to end and against real infrastructure, that a **worker-kind session can act as an
Epic Manager**: spawn a worker, drive it to a PR, get a reviewer verdict, resolve comments, merge,
and reclaim. It is deliberately cheap — one scratch base branch, one one-line docs file, one PR.

Target any low-stakes repo the AO project can reach. The 2026-08-23 run used skyvern-cloud
(PR [#15945](https://github.com/Skyvern-AI/skyvern-cloud/pull/15945)) before these artifacts were
rehomed to agent-orchestrator; its transcript is `pilot-report.md` in this directory.

**Nothing in this pilot touches `main`.** The PR's base is a scratch branch; `main` is verified
untouched afterwards.

---

## Part A — operator

- [ ] **A0. Health.** `ao doctor` shows **no FAILs**. WARNs (hooks-log, missing optional
      harnesses, gitlab token) are expected on a healthy install — review once, accept or fix.
      Record `ao version` and `ao status`.
- [ ] **A1. Baseline.** Record the target repo's `main` SHA:
      `git ls-remote origin refs/heads/main`. This is the "untouched" reference for B5.
- [ ] **A2. Scratch base, server-side.** Create the PR's base branch at that SHA without a local
      checkout:
      ```bash
      gh api repos/<owner>/<repo>/git/refs -f ref=refs/heads/scratch/ao-pilot-base \
        -f sha=<main-sha>
      ```
- [ ] **A3. Pre-measure the cleanup blast radius (read-only).**
      `ao session cleanup --dry-run -p <project>` — confirm no live session appears in the
      candidate set. The set is **project-wide**, not pilot-scoped.
- [ ] **A4. Spawn the pilot manager — worker-kind.** One inline prompt, **< 4096 bytes**
      (`PROMPT_TOO_LONG` above that), instructing it to run A5–A9 itself. Record the session id
      the spawn prints; display names are not addressable.
- [ ] **A5. (manager) Spawn one scratch worker** onto `scratch/ao-pilot-change`, told to add a
      single one-line file and open a PR **based on `scratch/ao-pilot-base`**. Record its id.
- [ ] **A6. (manager) Poll to PR.** The worker's `status` transitions to `pr_open`, but
      `ao session ls --json` carries **no PR fields** — get the number with
      `gh pr list --repo <owner>/<repo> --head scratch/ao-pilot-change --json number,baseRefName`.
- [ ] **A7. (manager) Review.** `ao review trigger <worker-session-id>` then
      `ao review ls <worker-session-id>` until a verdict lands. **Session ids only** — display
      names return `REVIEW_NOT_FOUND`. Send any standing constraint with `ao send` *before*
      triggering: AO delivers the review to the worker immediately and a corrective message can
      lose that race.
- [ ] **A8. (manager) Resolve comments.** `ao pr resolve-comments <pr>`. It prints
      `resolved 0 review thread(s)` when the worker already resolved them — informational, not a
      failure. Confirm the true end state via `gh`.
- [ ] **A9. (manager) Merge into the scratch base.** Attempt `ao pr merge <pr>` first — this is
      the command the pilot exists to re-test. If it still fails, fall back to the pinned form,
      which preserves the same merge-gate discipline:
      ```bash
      gh pr merge <pr> --repo <owner>/<repo> --squash --match-head-commit <head-sha>
      ```
      Record which path worked; a working `ao pr merge` retires the fallback from the playbook.
- [ ] **A10. (manager) Reclaim.** `ao session kill <worker-session-id>` **first** — cleanup
      reclaims only TERMINATED sessions — then `ao session cleanup -y -p <project>`.

## Part B — expected evidence

Every box needs transcript evidence (command + output), not an assertion.

- [ ] **B1. Spawn from a worker-kind session works.** The pilot manager's `ao spawn` returns a
      new session id, and that worker actually starts. (This is the whole point: managers are
      worker-kind because AO permits exactly one active orchestrator per project.)
- [ ] **B2. PR base is the scratch branch.** `gh pr view <pr> --json baseRefName` →
      `scratch/ao-pilot-base`. Never `main`.
- [ ] **B3. A reviewer verdict lands.** `ao review ls <worker-session-id>` reaches a terminal
      verdict (e.g. `approved`) with a run id.
- [ ] **B4. Comments are resolvable.** All review threads end `isResolved: true` (verify via `gh`,
      not via the resolve command's own output).
- [ ] **B5. The merge lands on the scratch base and `main` is untouched.** Verify all four:
      PR state `MERGED`; both pilot files present on `scratch/ao-pilot-base`; the pilot file
      **404s on `main`**; the merge commit is **not** an ancestor of `main`. If `main` moved,
      attribute every commit — unrelated coworker merges are expected and fine.
- [ ] **B6. Cleanup reclaims.** `ao session cleanup -y` completes and reports counts. Note which
      sessions it *skipped* and why — an `idle`/non-terminated pilot session will not be reclaimed
      until it is killed (B/A10).

## Teardown

- [ ] Delete both scratch branches:
      `gh api -X DELETE repos/<owner>/<repo>/git/refs/heads/scratch/ao-pilot-base` and
      `…/scratch/ao-pilot-change`. Leaving the base in place lets the scratch file ride silently
      into any future promotion of that branch.
- [ ] `ao session kill <pilot-manager-session-id>` to release its worktree.
- [ ] The merged PR record stays — PRs cannot be deleted. Note its number in the run record.

## PASS / ABORT

- **PASS** — every Part B box checked with transcript evidence. Record which of the Part A
  commands worked *as written* versus needed a fallback; those deltas are the playbook's edits.
- **ABORT** — any silent no-op (a command exits 0 having done nothing) or permission failure.
  **Stop and report verbatim. Do not improvise a workaround mid-run.** The 2026-08-23 run aborted
  its merge step this way and the fallback was taken only under explicit authorization.

## Known-broken as of AO build `f7637f78c` (re-test these first)

| Command | 2026-08-23 result |
| --- | --- |
| `ao pr merge <pr>` | **Unconditionally broken** — CLI never sends `prUrl`/`expectedHeadSha` and exposes no flag; the daemon's CI rollup also reports `state: failing` with zero failing checks on a clean PR (`PR_PRECONDITIONS_UNMET`) |
| `ao review trigger\|ls <name>` | `REVIEW_NOT_FOUND` — takes session ids only |
| `ao session ls --json` | No PR fields; PR detection needs `gh pr list --head` |
| `ao session cleanup` | Reclaims only TERMINATED sessions; candidate set is project-wide |
| `ao send` | Exits 0 with no output; no delivery confirmation |
