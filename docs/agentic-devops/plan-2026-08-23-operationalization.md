# Agentic DevOps Playbook Operationalization — Implementation Plan

> **RETARGETED 2026-08-23 (explicit user decision):** all artifacts land in
> **AronPerez/agent-orchestrator** (`docs/agentic-devops/`), NOT in skyvern-cloud.
> The lifecycle still RUNS AGAINST skyvern-cloud at runtime (SKY epics, Skyvern tests,
> product PRs), but its machinery — playbook, briefs, pointer prompts, pilot, QA reports —
> lives entirely in this fork. Nothing process-related is ever committed to skyvern-cloud;
> only real product slices land there when the lifecycle runs.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement
> this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. This is a docs/config change
> (playbook invariant 5: verify + finish; no TDD cycles) — every task still ends in
> verification evidence.

**Goal:** Land the amended (v1.4) Agentic DevOps Lifecycle Playbook, its 13 extracted brief
files, ready-to-paste pointer prompts, and the preflight-pilot record in the
AronPerez/agent-orchestrator fork as ONE PR, so the lifecycle can actually run on the
current AO build.

**Architecture:** Everything lands under `docs/agentic-devops/` in this repo (the fork's
`docs/` tree is the established home for design/ops docs — adr/, architecture.md, cli/).
Two substrate repairs drive every edit: (1) AO permits exactly ONE active orchestrator-kind
session per project — a second `--kind orchestrator` spawn silently returns the existing
session — so Epic Managers become worker-kind sessions acting as managers by brief;
(2) `ao spawn --prompt` hard-caps at 4096 bytes (`PROMPT_TOO_LONG`) — so briefs are
committed files and every spawn passes a <1024-byte pointer prompt. Because manager/worker
sessions run in **skyvern-cloud worktrees** while the briefs live in **this repo**, every
runtime pointer uses the absolute canonical checkout path
`/Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/…` (single-host
assumption; revisit if the lifecycle ever runs on a remote AO host).

**Tech Stack:** Markdown; plain git + gh; `ao` CLI (verified build `f7637f78c`).

**Repo / branch:** origin `AronPerez/agent-orchestrator`; work branch
`ao-devops/agentic-devops-playbook-v14`; PR base **`develop`** (the fork's work branch and
GitHub default — `main` mirrors upstream, never PR into it).

**Spec (read BOTH before starting; they sit next to this plan):**
- `/Users/amongstar/.ao/data/briefs/agentic-devops/playbook-2026-08-23-signed.md` — signed
  source text; appendices A.1–A.5 / B.1–B.8 are the raw material for `briefs/`.
- `/Users/amongstar/.ao/data/briefs/agentic-devops/architectural-review-2026-08-23.md` —
  the review mandating each amendment, with file:line evidence for both substrate rules.
  (Its "repo placement" section described skyvern-cloud and is SUPERSEDED by the retarget
  note above.)

## Preflight pilot — COMPLETE (record it, don't run it)

The pilot ran 2026-08-23 against scratch branches in skyvern-cloud. VERDICT: the
worker-kind-manager mechanism PASSES, and the run corrected four commands. Proven: a
worker-kind manager ran `ao spawn` (created pilot-w1); the worker opened PR
Skyvern-AI/skyvern-cloud#15945 with base `scratch/ao-pilot-base`; `ao review trigger` →
verdict `approved` (but ONLY with session ids — display names are rejected); the merge
landed on the scratch base with `main` independently verified untouched. Corrections the
pilot forced: **`ao pr merge` is unconditionally broken in build f7637f78c** (the CLI
omits the prUrl/expectedHeadSha the daemon route requires and exposes no flag for them;
the daemon's CI rollup also false-fails clean PRs — `state: failing` with zero failing
checks on a CLEAN 27-pass PR), so the merge used
`gh pr merge --squash --match-head-commit <head-sha>`; `ao session ls --json` carries no
PR fields (PR detection needs `gh pr list --head`); `ao session cleanup` reclaims only
TERMINATED sessions (kill merged workers first). All folded into Task 1 as edits P1–P6
and into Task 2 as T2g. Scratch branches are deleted; the merged PR record remains (PRs
are undeletable). The manager's full transcript is at
`/Users/amongstar/.ao/data/briefs/agentic-devops/pilot-report.md` — commit a copy as
`docs/agentic-devops/pilot-report.md` in Task 4 and quote its step-verdict table in the
PR body.

## Global Constraints

- Never use `--kind orchestrator` anywhere in the landed docs except inside the two
  "why not" explanations (E4, E5). Verification greps below enforce this.
- Every pointer prompt measures `< 1024` bytes via `wc -c` (hard API limit is 4096).
- Session names in examples stay ≤ 20 characters.
- Preserve the signed playbook's process content verbatim except the enumerated edits —
  you are repairing substrate assumptions and rehoming artifacts, not redesigning the
  lifecycle.
- All new files under `docs/agentic-devops/` in THIS repo. Nothing is committed to
  skyvern-cloud by this plan or by the lifecycle's process machinery.
- Plain git, no Graphite. One branch, one PR: `ao-devops/agentic-devops-playbook-v14`
  → base `develop` on origin (AronPerez/agent-orchestrator).
- `<ANGLE-BRACKET>` slots inside pointer-prompt TEMPLATES are spawn-time parameters by
  design, not TODOs. Everything else in this plan is literal.

## File Structure

```
docs/agentic-devops/                          (this repo, AronPerez/agent-orchestrator)
  playbook.md                     # v1.4 (Task 1)
  briefs/
    A1-discovery-manager.md  A2-build-manager.md  A3-qa-manager.md
    A4-deploy-manager.md     A5-release-manager.md
    B1-new-feature.md  B2-bug-fix.md  B3-discovery-worker.md
    B4-deploy-verification.md  B5-release-measurement.md
    B6-small-refactor.md  B7-config-docs.md  B8-adversarial-qa.md    # (Task 2)
  pointer-prompts.md              # ready-to-paste spawn commands (Task 2)
  pilot.md                        # reusable preflight checklist (Task 3)
  pilot-report.md                 # pilot transcript, if available (Task 4)
  plan-2026-08-23-operationalization.md       # copy of this plan (Task 4)
```

Runtime path constant used inside briefs and pointer prompts (absolute, because sessions
run in skyvern-cloud worktrees): `BRIEFS=/Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs`

---

### Task 1: Playbook v1.4

**Files:**
- Create: `docs/agentic-devops/playbook.md`

**Interfaces:**
- Consumes: the signed source playbook (Spec file 1), copied verbatim then edited.
- Produces: `playbook.md` whose §3–§7 spawn commands reference the exact brief paths
  Task 2 creates, and whose appendices are an index of those files.

- [ ] **Step 1:** Copy `playbook-2026-08-23-signed.md` into `docs/agentic-devops/playbook.md`
      verbatim, dropping the "SOURCE COPY" blockquote near the top.
- [ ] **Step 2:** Apply edits E1–E13 below. Each is verbatim old → new; apply with exact
      string replacement, not paraphrase.

**E1 — §0 bullet 1.** old: `it spawns one **Epic Manager** per stage-epic and injects the matching Appendix A brief via ` `` `-prompt` `` `. It never spawns or messages workers.`
new: `it spawns one **Epic Manager** per stage-epic (a worker-kind session — see invariant 2) with a short pointer prompt to the manager's brief file. It never spawns or messages workers.`

**E2 — §0 bullet 2.** old: `**Epic Managers** spawn workers with the matching Appendix B preamble via ` `` `-prompt` `` `, monitor`
new: `**Epic Managers** spawn workers with pointer prompts to the matching Appendix B brief file, monitor`

**E3 — §0 last bullet.** old: `` `ao doctor` `` ` must be green before any unattended epic.`
new: `` `ao doctor` `` ` must show no FAILs before any unattended epic (review WARNs once; accept or fix).`

**E4 — invariant 2.** old: `2. **One layer deep.** The Orchestrator initiates exactly one Epic Manager per epic and never commands workers.`
new: `2. **One layer deep.** The Orchestrator initiates exactly one Epic Manager per epic and never commands workers. Epic Managers are **worker-kind AO sessions acting as managers by brief** — AO enforces exactly one active orchestrator-kind session per project (a second ` `` `--kind orchestrator` `` ` spawn silently returns the existing session instead of creating one), so ` `` `--kind orchestrator` `` ` is never used below Tier 1.`

**E5 — §2 table, first two rows.**
Spawn-manager row new value: `` `ao spawn --name <n> --project skyvern-cloud --issue <SKY-…> --prompt "<pointer to $BRIEFS/A<stage>-….md>"` `` ` — worker-kind; never ` `` `--kind orchestrator` `` ` (invariant 2)`
Spawn-worker row: replace `"<Appendix B preamble>"` with `"<pointer to $BRIEFS/B<type>-….md>"`.
Immediately below the table add one line: `Briefs live in the agent-orchestrator fork, read via the absolute path ` `` `/Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/` `` ` — sessions run in skyvern-cloud worktrees, so relative paths will not resolve.`

**E6 — §2 table, revive/kill/reclaim row.** old: `` `ao session cleanup [--dry-run]` ``
new: `` `ao session cleanup [--dry-run] -y` ``

**E7 — §2 lifecycle paragraph.** old: `**Lifecycle automation (built in, not configurable):**`
new: `**Lifecycle automation (built in):**`

**E8 — the five Tier-1 manager spawns (§3, §4, §5, §6, §7).** Replace each two-line
`ao spawn … --kind orchestrator \ --issue … --prompt "<Appendix A.x>"` block with the
matching block below (verbatim; also recorded in `pointer-prompts.md`):

```bash
ao spawn --name sky4200-mgr --project skyvern-cloud --issue SKY-4200 \
  --prompt "You are the Discovery Epic Manager for epic SKY-4200 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A1-discovery-manager.md and follow it exactly; it defines your role, workers, monitoring, gate, and reporting. You coordinate; you never write product code."
```
```bash
ao spawn --name sky4210-mgr --project skyvern-cloud --issue SKY-4210 \
  --prompt "You are the Build Epic Manager for epic SKY-4210 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A2-build-manager.md and follow it exactly; it defines your decomposition, monitoring, exit gates, merge authority, and reporting. You coordinate and merge; you never write product code."
```
```bash
ao spawn --name sky4215-mgr --project skyvern-cloud --issue SKY-4215 \
  --prompt "You are the QA Epic Manager for epic SKY-4215 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A3-qa-manager.md and follow it exactly; it defines the QA round loop, Linear filing, fix-train merges, reports, and both exit gates. You coordinate and merge qafix PRs; you never write product code."
```
```bash
ao spawn --name sky4220-mgr --project skyvern-cloud --issue SKY-4220 \
  --prompt "You are the Deploy Epic Manager for epic SKY-4220 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A4-deploy-manager.md and follow it exactly; it defines ensure-vs-deploy modes, gates, and response rules. You coordinate; you never write product code."
```
```bash
ao spawn --name sky4230-mgr --project skyvern-cloud --issue SKY-4230 \
  --prompt "You are the Release Epic Manager for epic SKY-4230 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A5-release-manager.md and follow it exactly; it defines flag-flip criteria, stabilization, measurement, and the Learn roll-up. You coordinate; you never write product code."
```

**E9 — worker spawn examples in §3/§4/§5 sequences.** Replace each `--prompt "<B.x …>"`
with the worker pointer template filled in (template, verbatim in `pointer-prompts.md`):
`You are a Tier-3 worker for <ISSUE> in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/<FILE> and follow it exactly. Parameters: <ONE LINE: slice+branch / B3 ROLE / B2 finding+repro-test path>.`
Occurrences to rewrite: §3 `sky4201-repo` (B3, `ROLE=repo-analyst`), `sky4202-tlm` (B3,
`ROLE=telemetry-miner`), `sky4203-ux` (B3, `ROLE=ux-scout`); §4 `sky4211-fe` (B1),
`sky4212-be` (B2), `sky4213-doc` (B7); §5 `sky4216-qa` (B8), `sky4217-fx1` (B2, finding
params); §6 `sky4221-dv` (B4); §7 `sky4231-rm` (B5).

**E10 — §4 sequence.** old line: `ao session cleanup` → new: `ao session cleanup -y`

**E11 — appendix hollowing.** Appendix A header → `# Appendix A — Epic Manager briefs (files under docs/agentic-devops/briefs/; spawn passes a pointer prompt)`;
Appendix B header → same pattern. Replace each `## A.x — …` / `## B.x — …` section BODY
(the fenced block) with one index line, e.g. `→ briefs/A2-build-manager.md`. The content
lives ONLY in `briefs/` (single source of truth; no drift).

**E12 — title + change log.** Title line 3 → `**AO-Native · v1.4 · 2026-08-23 · verified against AO build f7637f78c**`. Prepend to the change log:
`- **v1.4 (2026-08-23)** — Fitted to the verified AO build (see architectural-review-2026-08-23) and rehomed to AronPerez/agent-orchestrator: Epic Managers are worker-kind sessions acting by brief (AO allows one active orchestrator per project; a second --kind orchestrator spawn silently no-ops); briefs moved from --prompt inlining to committed files under docs/agentic-devops/briefs/ read via absolute path from skyvern-cloud worktrees, with <1 KB pointer prompts (AO caps spawn prompts at 4096 bytes, PROMPT_TOO_LONG); appendices hollowed to an index; ao session cleanup runs with -y unattended; doctor gate is "no FAILs"; -prompt typos fixed; QA reports rehomed to this repo (docs/agentic-devops/qa/). Preflight pilot passed 2026-08-23 (Skyvern-AI/skyvern-cloud#15945, scratch branches, main untouched).`

**E13 — QA report home.** §5: `docs/qa/SKY-<epic>/round-N.md` → `docs/agentic-devops/qa/SKY-<epic>/round-N.md` (this repo — the B.7 docs worker that commits it is spawned in project agent-orchestrator, not skyvern-cloud; process artifacts never land in the product repo).

**P1 — merge command (pilot: `ao pr merge` unconditionally broken in f7637f78c).**
Everywhere the playbook invokes `ao pr merge` (§2 merge row, §4 `ao pr merge 14801` /
`14802`, §5 `… && ao pr merge <pr>`, C.1 table), replace with
`gh pr merge <pr> --repo Skyvern-AI/skyvern-cloud --squash --match-head-commit <head-sha>`
and add ONE note under the §2 table: `` `ao pr merge` is broken in AO build f7637f78c —
the CLI omits the prUrl/expectedHeadSha the daemon requires (no flag exists), and the
daemon's CI rollup false-fails clean PRs (PR_PRECONDITIONS_UNMET). Bugs filed in
agent-orchestrator; restore `ao pr merge` when fixed. The pinned `--match-head-commit`
preserves the merge-gate discipline; squash is the only merge method skyvern-cloud
allows.`` Invariant 3's `call ` `` `ao pr merge` `` phrasing → `hold the merge authority
(currently exercised via the pinned gh fallback — see §2 note)`.

**P2 — review commands take SESSION IDS.** §2 reviewer row: `<worker>` →
`<worker-session-id>` plus note `(display names are rejected — resolve ids via
` `` `ao session ls -p skyvern-cloud --json` `` `)`. §4's
`ao review trigger sky4211-fe && ao review ls sky4211-fe` → resolve the session id
first, then trigger/ls with the id. Same in §5's `ao review trigger sky4217-fx1`.

**P3 — PR detection.** Where §4/§5 poll for a worker's PR, add: session `status`
transitions to `pr_open`, but `ao session ls --json` carries NO PR fields — get the
number with `gh pr list --repo Skyvern-AI/skyvern-cloud --head <branch> --json number,baseRefName`.

**P4 — cleanup semantics.** §4 epic exit: `ao session kill <id>` each merged/finished
worker FIRST, then `ao session cleanup -y` — cleanup reclaims only TERMINATED sessions
and its candidate set is project-wide, not epic-scoped.

**P5 — review-delivery race (§2 lifecycle paragraph, one added sentence).** `AO delivers
review results to the owning worker the moment they land; a manager's corrective
` `` `ao send` `` ` can lose that race — send standing constraints BEFORE
` `` `ao review trigger` `` `, and expect the worker may already have acted on raw
review feedback.`

**P6 — silent-success semantics (§2, one added sentence).** `` `ao send` `` ` prints
nothing on success; ` `` `ao pr resolve-comments` `` ` prints "resolved 0 review
thread(s)" when threads were already resolved — both informational; verify end state via
gh when it matters.`

- [ ] **Step 3: Verify.** From the repo root, all of:
      `grep -c -- '--kind orchestrator' docs/agentic-devops/playbook.md` → exactly `2` (E4 + E5 explanations);
      `grep -n 'inject via' docs/agentic-devops/playbook.md` → no matches;
      `grep -n '"<Appendix\|"<B\.' docs/agentic-devops/playbook.md` → no matches;
      `grep -c 'cleanup -y' docs/agentic-devops/playbook.md` → `≥ 2` (§2 table + §4);
      `grep -c 'cloud_docs/agentic-devops' docs/agentic-devops/playbook.md` → `0`;
      `grep -c 'match-head-commit' docs/agentic-devops/playbook.md` → `≥ 3` (P1 sites);
      `grep -n 'ao pr merge' docs/agentic-devops/playbook.md` → occurrences ONLY inside the P1 broken-note.
- [ ] **Step 4: Commit** `docs(agentic-devops): playbook v1.4 — fit to AO substrate (worker-kind managers, brief files)`

---

### Task 2: Brief files + pointer prompts

**Files:**
- Create: `docs/agentic-devops/briefs/` — 13 files per File Structure
- Create: `docs/agentic-devops/pointer-prompts.md`

**Interfaces:**
- Consumes: appendix bodies from the SIGNED SOURCE spec file (not from playbook.md, whose
  appendices are hollowed by E11).
- Produces: the 13 brief filenames exactly as referenced by Task 1's E8/E9 and Task 3.

- [ ] **Step 1:** For each of A.1–A.5 and B.1–B.8, copy the appendix's fenced content from
      the source spec into the matching `briefs/` file (fence removed; plain markdown),
      then apply T2a–T2f:

**T2a — A-brief ROLE openers (verbatim replacements).**
- A1 old: `ROLE: You are the Discovery Epic Manager for epic SKY-4200 (skyvern-cloud), an AO
orchestrator-kind session, Tier 2, durable.` → new: `ROLE: You are the Discovery Epic Manager for epic SKY-4200 (skyvern-cloud) — a worker-kind
AO session acting as Epic Manager, Tier 2, durable. You are NOT the project's AO
orchestrator (AO allows exactly one; that is Tier 1). Your manager authority comes from
this brief; you run ao commands (spawn/send/session/review) from your session shell.`
- A2 old: `ROLE: You are the Build Epic Manager for epic SKY-4210 (skyvern-cloud), an AO orchestrator-
kind session, Tier 2, durable.` → new: `ROLE: You are the Build Epic Manager for epic SKY-4210 (skyvern-cloud) — a worker-kind AO
session acting as Epic Manager, Tier 2, durable. You are NOT the project's AO orchestrator
(AO allows exactly one; that is Tier 1). Your manager authority comes from this brief; you
run ao commands (spawn/send/session/review/pr) from your session shell.` (keep the rest of
the sentence list, including "you are the ONLY merge authority", unchanged)
- A3 old: `ROLE: You are the QA Epic Manager for epic SKY-4215 (skyvern-cloud), an AO orchestrator-
kind session, Tier 2, durable.` → new: same pattern as A2 with QA wording.
- A4 old: `ROLE: You are the Deploy Epic Manager for epic SKY-4220 (skyvern-cloud), Tier 2, durable.`
→ new: `ROLE: You are the Deploy Epic Manager for epic SKY-4220 (skyvern-cloud) — a worker-kind AO
session acting as Epic Manager, Tier 2, durable. You are NOT the project's AO orchestrator;
your authority comes from this brief.` (rest unchanged)
- A5 old: `ROLE: You are the Release Epic Manager for epic SKY-4230 (skyvern-cloud), Tier 2, durable.`
→ new: same pattern as A4 with Release wording.

**T2b — inner spawn lines inside A briefs.** Rewrite every `--prompt "<B.x …>"` to the E9
pointer template with the absolute briefs path (A1: three B3 spawns with ROLE params;
A3: the step-2 B8 spawn and the step-4 fix-worker line; A4: the B4 spawn; A5: the B5
spawn). In A2, replace the sentence
`Pass preambles via --prompt at spawn: B.1 features, B.2 bug fixes, B.6 refactors, B.7 config/docs.`
with `Start workers with pointer prompts to the briefs files (see pointer-prompts.md in the same directory): B1 features, B2 bug fixes, B6 refactors, B7 config/docs.`

**T2c — A2/A3 cleanup.** In A2's EPIC EXIT: `` `ao session cleanup` run.`` → `` `ao session cleanup -y` run.``

**T2d — A3 QA-report path.** `docs/qa/SKY-4210/round-N.md` → `docs/agentic-devops/qa/SKY-4210/round-N.md (agent-orchestrator repo — spawn the B.7 docs worker in project agent-orchestrator)`.

**T2e — MONITORING addendum.** Append to each A-brief's MONITORING paragraph:
`The session list is project-wide — filter to your epic's sessions by name prefix.`

**T2f — B briefs.** Verbatim from source, no edits, EXCEPT: B.1's slice-plan path
(`docs/superpowers/plans/…` inside skyvern-cloud) stays — slice plans are part of product
PRs and belong with product code.

**T2g — apply the pilot corrections (P1–P5) inside the A briefs.** A2/A3 (and A4's
revert-PR merge line): every `ao pr merge` → the P1 `gh pr merge … --squash
--match-head-commit` fallback with a one-line pointer to the §2 note; every review
trigger/ls → session-id form per P2; PR-detection guidance per P3; A2's EPIC EXIT per P4
(kill merged workers, then `ao session cleanup -y`); append the P5 race sentence to the
A2/A3 MONITORING paragraphs (alongside T2e's addendum).

- [ ] **Step 2:** Write `pointer-prompts.md`: the five E8 manager spawn blocks, the E9
      worker template, and one filled worker example per B brief (use the §3–§5 sequence
      occurrences), each followed by its measured byte count.
- [ ] **Step 3: Verify.**
      `ls docs/agentic-devops/briefs | wc -l` → `13`, none empty;
      `grep -rl 'orchestrator-kind session' docs/agentic-devops/briefs/` → no matches;
      `grep -rn -- '--kind orchestrator' docs/agentic-devops/briefs/` → no matches;
      `grep -rn 'ao pr merge' docs/agentic-devops/briefs/` → only P1-note pointers, no bare invocations;
      `grep -rc 'cloud_docs' docs/agentic-devops/briefs/ | grep -v ':0'` → only B1's
      `cloud_docs`-free… (B briefs contain no cloud_docs references; expect no output);
      every prompt string in `pointer-prompts.md`: `wc -c` `< 1024` (record the numbers).
- [ ] **Step 4: Commit** `docs(agentic-devops): extract manager/worker briefs + pointer prompts`

---

### Task 3: Preflight pilot checklist (reusable)

**Files:**
- Create: `docs/agentic-devops/pilot.md`

- [ ] **Step 1:** Create `pilot.md`: the reusable end-to-end checklist for re-verifying
      manager mechanics after any AO upgrade (`ao version` changes). Content = the
      2026-08-23 pilot procedure, generalized:
      - Part A (operator): `ao doctor` no FAILs; scratch base branch created server-side
        at the target project's main via `gh api`; spawn a worker-kind pilot manager with
        an inline prompt (< 4096 bytes) that spawns one scratch worker, drives PR →
        `ao review trigger` → `ao review ls` → `ao pr resolve-comments` → `ao pr merge`
        into the scratch base, then `ao session cleanup -y`.
      - Part B (expected evidence): the six checkboxes from the 2026-08-23 run — spawn
        from worker-kind works; PR base = scratch branch; verdict lands; comments
        resolvable; merge lands on scratch base with main untouched (verify via file
        404-on-main); cleanup reclaims.
      - Teardown: delete both scratch branches; kill the pilot manager.
      - Note: target any low-stakes repo the AO project can reach; the 2026-08-23 run
        used skyvern-cloud (PR #15945) before the artifacts were rehomed here.
      - PASS/ABORT criteria: PASS = every Part B box with transcript evidence; ABORT =
        any silent no-op or permission failure → stop and report verbatim, do not
        improvise.
- [ ] **Step 2: Verify:** contains no `--kind orchestrator`; renders cleanly.
- [ ] **Step 3: Commit** `docs(agentic-devops): reusable preflight pilot checklist`

---

### Task 4: Pilot record, plan copy, PR

- [ ] **Step 1:** If `/Users/amongstar/.ao/data/briefs/agentic-devops/pilot-report.md`
      exists, copy it to `docs/agentic-devops/pilot-report.md`. Copy this plan file to
      `docs/agentic-devops/plan-2026-08-23-operationalization.md`. Commit
      `docs(agentic-devops): pilot record + operationalization plan`
- [ ] **Step 2:** Push `ao-devops/agentic-devops-playbook-v14` to origin
      (AronPerez/agent-orchestrator); open the PR with `gh pr create --base develop` —
      title `docs: operationalize agentic-devops playbook (v1.4) for AO`.
      Body: one-paragraph summary; the two substrate rules (quote the review); Task 1–3
      verification outputs; pilot verdict + link to Skyvern-AI/skyvern-cloud#15945 (or
      "transcript pending").
- [ ] **Step 3:** Let CI run; fix anything it flags (docs-only — expect green).
- [ ] **Step 4: Report** to the orchestrator: PR number + URL, verification outputs, and
      any deviations from this plan (there should be none without sign-off).

---

## Self-review (performed at authoring time)

- Spec coverage: review rec 1 → Tasks 1–2; rec 2 (small fixes) → E3/E6/E7/E10/E12/E13;
  rec 3 (pilot) → executed 2026-08-23 + Task 3 record; rec 4 → this plan + Task 4.
- Retarget coverage: no landed file or runtime pointer references skyvern-cloud paths;
  the only skyvern-cloud writes the lifecycle ever makes are product slices/qafix PRs by
  design (and B.1's in-repo slice plan, which is part of the product PR).
- Placeholder scan: `<…>` tokens are declared spawn-time parameters or command arguments
  inside quoted operational text.
- Consistency: brief filenames identical across E5/E8/E9/T2/Task 3/File Structure; the
  absolute briefs path is identical everywhere it appears.

## After this plan merges (orchestrator/human, not the executor)

- Kickoff: `ao send` the skyvern-cloud Tier-1 orchestrator a pointer to
  `/Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/playbook.md` to begin a
  real Stage-1 epic.
- Before Stage 3+ autonomy: verify the external prerequisites in the review (STG deploy
  mechanism, PostHog/Datadog/Linear access inside sessions).
- Future edit (deliberately out of scope): genericize the hardcoded SKY-42xx example ids
  into `<epic>` parameters once the first real epic has exercised the flow.
