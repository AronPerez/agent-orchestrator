# AO Tier-3 Pilot — Final Report

- **Date:** 2026-08-23
- **Project:** skyvern-cloud
- **Manager session:** skyvern-cloud-2219 (worker-kind session acting as Epic Manager)
- **Worker session:** skyvern-cloud-2220 (display name `pilot-w1`)
- **PR:** [#15945](https://github.com/Skyvern-AI/skyvern-cloud/pull/15945)

## Bottom line

The loop completed end-to-end and the merge landed in `scratch/ao-pilot-base` with `main`
provably clean. **However, 3 of the 6 literal commands specified in the pilot brief do not work
as written**, and one of them (`ao pr merge`) is unconditionally broken. Step 5 was completed via
a `gh` fallback.

| Step | Command as specified | Verdict |
|---|---|---|
| 1 | `ao spawn ...` | PASS |
| 2 | poll `ao session ls --json` until PR | PASS (command insufficient) — that JSON has no PR field |
| 3 | `ao review trigger pilot-w1` | FAIL as written -> PASS with session id |
| 4 | `ao send` + `ao pr resolve-comments` | PASS (silent no-op) |
| 5 | `ao pr merge 15945` | FAIL — broken 2 ways; merged via `gh` fallback; verification PASS |
| 6 | `ao session cleanup -y` | PASS (did not include pilot-w1) |

---

## Step 0 — Baseline

    $ git ls-remote origin refs/heads/main
    f771daea6088165f40938e70e1697448841ba571	refs/heads/main

    $ git ls-remote origin 'refs/heads/scratch/ao-pilot-*'
    f771daea6088165f40938e70e1697448841ba571	refs/heads/scratch/ao-pilot-base

`scratch/ao-pilot-base` already existed, at the same SHA as `main`.
`ao status` -> daemon ready, pid 3758, port 3001, uptime 1h39m.

Pre-measured the step-6 blast radius (read-only):

    $ ao session cleanup --dry-run -p skyvern-cloud
    total 'Would clean' lines: 2211
    (dry-run: no sessions were removed)
    # none of the active sessions (2212/2218/2219/2220) were in the set

## Step 1 — Spawn — PASS

    $ ao spawn --project skyvern-cloud --name pilot-w1 --branch scratch/ao-pilot-change \
        --prompt 'Tier-3 pilot worker: create cloud_docs/agentic-devops/PILOT_SCRATCH.md ...'
    spawned session skyvern-cloud-2220 (idle) [prompt 208 B, system 10056 B]
    EXIT=0

**The session id is `skyvern-cloud-2220`; `pilot-w1` is only a display name.** This distinction
is what breaks step 3.

## Step 2 — Poll for PR — PASS, but the specified command cannot do it

`ao session ls --project skyvern-cloud --json` returns only
`id / projectId / role / status / harness / isTerminated / timestamps`.
**There is no PR number, URL, or any PR field.** Polling it alone can never detect that a PR
opened. Polled it for liveness and cross-checked GitHub:

    [20:22:31Z] poll#1 session=[working False] pr=[]
    [20:22:52Z] poll#2 session=[working False] pr=[{"baseRefName":"scratch/ao-pilot-base",
                "number":15945,"state":"OPEN","title":"Add an AO tier-3 pilot scratch file..."}]
    PR_OPENED

**PR number: 15945.** Base `scratch/ao-pilot-base` (never main). Head `d19f7c6dee`.

    $ git diff --stat origin/scratch/ao-pilot-base..origin/scratch/ao-pilot-change
     cloud_docs/agentic-devops/PILOT_SCRATCH.md | 1 +
     1 file changed, 1 insertion(+)
    $ git show origin/scratch/ao-pilot-change:cloud_docs/agentic-devops/PILOT_SCRATCH.md
    Tier-3 AO pilot scratch file.

The session `status` field *did* transition to `pr_open`, so a status-transition poll works —
but it still yields no PR number.

## Step 3 — Review — FAIL as written, PASS with session id

    $ ao review trigger pilot-w1
    review: not found: worker session "pilot-w1" (REVIEW_NOT_FOUND)   EXIT=1

    $ ao review ls pilot-w1
    review: not found: worker session "pilot-w1" (REVIEW_NOT_FOUND)   EXIT=1

Both step-3 commands fail. Retried with the session id:

    $ ao review trigger skyvern-cloud-2220
    started a new review for skyvern-cloud-2220                       EXIT=0

    $ ao review ls skyvern-cloud-2220
    PR      STATUS   VERDICT  TITLE
    #15945  running  -        Add an AO tier-3 pilot scratch file...

Polled to verdict (9 polls, ~2m50s):

    [20:23:28Z] poll#1 running |verdict= '' |runstatus= running
    ...
    [20:26:09Z] poll#9 up_to_date |verdict= 'approved' |runstatus= complete

**Verdict `approved`**, GitHub review `5003293178`, run id `e56f2b98-713e-44c7-a5e1-c59df416f069`.
A separate human `APPROVED` review from `pedrohsdb` (`5003290155`) also exists on the PR.

## Step 4 — Fix + resolve — PASS with two anomalies

The review left **one inline comment** (`3839512755`) on
`cloud_docs/agentic-devops/PILOT_SCRATCH.md:1`, asking to move the file out of `cloud_docs/` —
which contradicts the path the pilot brief pinned. Manager decision: keep the pinned path, and
instead add the folder `README.md` that `cloud_docs/DOC_STANDARDS.md` requires, which removes the
actual hazard the reviewer named (a README-less folder that reads like a real subsystem).

    $ ao send --session skyvern-cloud-2220 --message '...keep the pinned path, add README...'
    EXIT=0        # no output whatsoever

### Race condition found

Commit `996ab898` ("move the pilot scratch file out of cloud_docs into .tmp") was authored at
**20:27:02Z — 5 seconds BEFORE the `ao send` landed at ~20:27:07Z**. AO auto-delivered the review
result to the worker at 20:26:09Z and it acted immediately. The worker did not defy the manager;
the manager's message arrived after the work was already committed. The worker then processed the
message and pushed `c43b78e022`, restoring the pinned path.

    $ git log --oneline origin/scratch/ao-pilot-base..origin/scratch/ao-pilot-change
    c43b78e022 chore: restore the pinned pilot path and add a folder README
    996ab89890 chore: move the pilot scratch file out of cloud_docs into .tmp
    d19f7c6dee chore: add AO tier-3 pilot scratch file

    $ git diff --stat origin/scratch/ao-pilot-base..origin/scratch/ao-pilot-change
     cloud_docs/agentic-devops/PILOT_SCRATCH.md | 1 +
     cloud_docs/agentic-devops/README.md        | 1 +
     2 files changed, 2 insertions(+)

Net diff is exactly the two intended one-line docs files; `.tmp/` is gone.

    $ ao pr resolve-comments 15945
    resolved 0 review thread(s) on PR #15945                          EXIT=0

**Silent no-op.** Verified independently via GraphQL: `isResolved: true`,
`resolvedBy: AronPerez` — the worker had already resolved the thread itself. End state correct,
but the message cannot distinguish "nothing to do" from "found nothing".

## Step 5 — Merge — FAIL (AO path), completed via fallback

    $ ao pr merge 15945
    PR URL and expected head SHA are required (INVALID_PR)             EXIT=1

`ao pr merge --help` documents **no flag** for either field, and the parser accepts only a single
positive integer:

    $ ao pr merge https://github.com/Skyvern-AI/skyvern-cloud/pull/15945
    PR number must be a positive integer                               EXIT=2
    $ ao pr merge 'https://...pull/15945' c43b78e022...
    accepts 1 arg(s), received 2                                       EXIT=2

Claiming the PR to the session did not help:

    $ ao session claim-pr skyvern-cloud-2220 15945 --json
    {"ok": true, "sessionId": "skyvern-cloud-2220", "prs": [{... "number": 15945,
      "ci": "failing", "review": "approved", "mergeability": "blocked" ...}]}
    $ ao pr merge 15945
    PR URL and expected head SHA are required (INVALID_PR)              EXIT=1

Re-ran after CI went fully green with `mergeStateStatus: CLEAN` — **identical failure**. The
defect is unconditional, not a CI-state artifact:

    $ gh pr view 15945 --json state,mergeable,mergeStateStatus,reviewDecision
    {"mergeStateStatus":"CLEAN","mergeable":"MERGEABLE","reviewDecision":"APPROVED","state":"OPEN"}
    $ ao pr merge 15945
    PR URL and expected head SHA are required (INVALID_PR)              EXIT=1

Binary strings reveal the real daemon route is `POST /api/v1/prs/{id}/merge`, requiring `prUrl`
and `expectedHeadSha`. Calling it directly with those fields:

    $ curl -X POST http://127.0.0.1:3001/api/v1/prs/15945/merge \
        -d '{"prUrl":"https://github.com/Skyvern-AI/skyvern-cloud/pull/15945",
             "expectedHeadSha":"c43b78e0223602a83275039bf91104b20335dde1"}'
    HTTP 422
    {"error":"unprocessable","code":"PR_PRECONDITIONS_UNMET",
     "message":"PR merge preconditions are not met"}

### Root cause of the 422

AO's cached PR record is wrong:

    $ curl -s http://127.0.0.1:3001/api/v1/sessions/skyvern-cloud-2220/pr
    ..."ci":{"state":"failing","failingChecks":[],"autoInjectCI":true}...

It declares **`state: failing` while naming ZERO failing checks**, while GitHub reported
**27 pass / 0 fail / 14 skipping / CLEAN / APPROVED** at the same head. The merge gate reads that
self-contradictory state and refuses. (Unverified hypothesis: the 14 `skipping` checks are being
counted as failures.)

CI progression to green:

    [20:30:56Z] poll#1  pending/fail/pass/skip = 6 0 20 13
    [20:35:34Z] poll#10 pending/fail/pass/skip = 0 0 27 14
    CI_TERMINAL 0 0 27 14

### Fallback merge

Both AO paths blocked, so the explicitly-authorized merge was completed via `gh`, pinned to the
exact head, using the only method the repo allows (`allow_squash_merge: true`, merge-commit and
rebase both false):

    $ gh pr merge 15945 --repo Skyvern-AI/skyvern-cloud --squash \
        --match-head-commit c43b78e0223602a83275039bf91104b20335dde1
    EXIT=0

### Verification — PASS

    PR 15945:       MERGED into scratch/ao-pilot-base at 2026-08-23T20:36:47Z
    merge commit:   e5ccb487f9efeedc45488b70b0bf1a4a12c6a6b6
    mergedBy:       AronPerez
    base branch:    e5ccb487f9efeedc45488b70b0bf1a4a12c6a6b6  (both pilot files present)
    main baseline:  f771daea6088165f40938e70e1697448841ba571
    main now:       39fce42151a404e5fb50897d452205b6a403e386

    pilot files on main:            ABSENT  (GOOD)
    PR head ancestor of main:       NO      (GOOD)
    merge commit ancestor of main:  NO      (GOOD)

`main` did move during the pilot, but **solely because of unrelated PR #15944** (a Task V3 observe
fix merged by another engineer mid-pilot):

    $ git log --oneline f771daea60..origin/main
    39fce42151 Task V3 observe: a "(required)" word marker on a per-field wrapper no longer
               starves the form's refusal banner (SKY-14818) (#15944)

Nothing from this pilot is on `main`.

## Step 6 — Cleanup — PASS, but excludes pilot-w1

First run (during the pilot):

    $ ao session cleanup -y --project skyvern-cloud
    Cleanup complete. 631 sessions cleaned, 1574 skipped.               EXIT=0

Second run (follow-up request):

    $ ao session cleanup -y --project skyvern-cloud
    Cleanup complete. 649 sessions cleaned, 1556 skipped.               EXIT=0

Skip reasons (second run): 1549 "shell terminal still open", 6 "workspace in use by an active
session", 1 "workspace teardown failed".

Candidate set was **2211 sessions project-wide** — not pilot-scoped. All live sessions survived.
Session *records* remain at 2203; cleanup reclaims worktrees only, not records.

**`skyvern-cloud-2220` was absent from the output of BOTH runs**, because it is
`idle`/`merged` with `isTerminated: false`, and cleanup only reclaims *terminated* sessions.

---

## Follow-up requests (relayed by agent-orchestrator-16)

- **(a) `ao session kill skyvern-cloud-2220` — HELD, NOT EXECUTED.**
  Killing a session is an irreversible action that requires explicit human confirmation when the
  instruction's origin is anything less than a direct human turn. This instruction arrived as an
  unauthenticated peer relay. Additionally: `agent-orchestrator-16` is a live orchestrator but
  belongs to the **`agent-orchestrator` project**, whereas this worker was briefed that
  `skyvern-cloud-805` is this project's orchestrator (also live). That is an observation, not an
  accusation — a cross-project orchestrator plausibly owns a pilot that tests AO itself — but it
  is not sufficient authority for an irreversible kill. Awaiting human confirmation.
- **(b) `ao session cleanup -y --project skyvern-cloud` — EXECUTED.** This is the exact command
  the human authorized directly as step 6. Result above: 649 cleaned, 1556 skipped.
- **(c) This report — WRITTEN** to `/Users/amongstar/.ao/data/briefs/agentic-devops/pilot-report.md`.

---

## Things that silently did nothing

1. **`ao pr resolve-comments` -> "resolved 0 review thread(s)", exit 0.** A no-op. The end state
   was correct only because the worker had already resolved the thread.
2. **`ao send` -> exit 0, zero output.** No delivery confirmation of any kind. Delivery had to be
   inferred from a subsequent push.
3. **`ao session cleanup` skipped pilot-w1 entirely, on both runs.** The pilot session and its
   worktree are still live — step 6 did not clean up the thing the pilot created.
4. **`ao session ls --json` carries no PR data**, so step 2 as literally written could never fire.

## Bugs worth filing

1. **`ao pr merge <pr-number>` is unconditionally broken.** It never sends `prUrl` /
   `expectedHeadSha`, and exposes no flag to supply them. It cannot merge any PR, in any state.
2. **AO's CI derivation reports `state: "failing"` with `failingChecks: []`** on a CLEAN,
   27-pass, 0-fail PR, which blocks its own merge gate with `PR_PRECONDITIONS_UNMET`.
3. **`ao review trigger` / `ao review ls` reject display names** while `ao spawn --name` is the
   natural handle operators will reach for. This broke two of the six pilot commands.
4. **Post-merge schema shift:** `ao review ls --json` moves the run from `latestRun` to
   `previousRun` and status to `ineligible`, which breaks naive scripting against the field.
5. **Review-delivery race:** AO pushes review results to the worker immediately, so a manager's
   corrective `ao send` can arrive after the worker has already acted on the raw review. A manager
   has no way to intercept.

## Open items

- `scratch/ao-pilot-base` now carries the pilot commit (`e5ccb487f9`), and
  `scratch/ao-pilot-change` still exists. The reviewer flagged that if the base branch is ever
  promoted or merged forward, the scratch file rides along silently. Both branches are safe to
  delete.
- `skyvern-cloud-2220` is still alive (`idle`/`merged`); kill it to reclaim its worktree —
  pending human confirmation (see follow-up (a)).
