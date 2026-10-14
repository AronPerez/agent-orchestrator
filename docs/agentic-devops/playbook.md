# Agentic DevOps Lifecycle Playbook — skyvern-cloud

**AO-Native · v1.4 · 2026-08-23 · verified against AO build f7637f78c**
Companion: `agentic-devops-design-report.md` (design rationale, failure modes). This document is operational only.

---

## 0. How to use

- The **Tier-1 Orchestrator** runs §3–§6 command sequences: it spawns one **Epic Manager** per stage-epic (a worker-kind session — see invariant 2) with a short pointer prompt to the manager's brief file. It never spawns or messages workers.
- **Epic Managers** spawn workers with pointer prompts to the matching Appendix B brief file, monitor with `ao session ls --json` + `ao review ls`, and are the only merge authority.
- Session names are ≤ 20 chars. Convention: `sky<epic>-mgr`, `sky<ticket>-<area>` (e.g. `sky4210-mgr`, `sky4211-fe`).
- Examples use display names for readability; resolve exact session ids with `ao session ls --json` (add `-project` to disambiguate).
- Run `ao <command> --help` before scripting a new environment — flags vary by installed build. `ao doctor` must show no FAILs before any unattended epic (review WARNs once; accept or fix).

## 1. Architectural invariants

1. **Two human gates.** Gate 1 — Stage 1 (CE): approve the Opportunity Report. Gate 2 — Stage 3 (QA) exit: approve the final QA report after a clean round; only this unlocks Stages 4–5. Stage 2, the QA loop itself, and Stages 4–5 run autonomously between the gates. The QA round-cap circuit-breaker (3 rounds without convergence) is a failure-path escalation, not a standing gate.
2. **One layer deep.** The Orchestrator initiates exactly one Epic Manager per epic and never commands workers. Epic Managers are **worker-kind AO sessions acting as managers by brief** — AO enforces exactly one active orchestrator-kind session per project (a second `--kind orchestrator` spawn silently returns the existing session instead of creating one), so `--kind orchestrator` is never used below Tier 1.
3. **Manager owns workers and merges.** Only Epic Managers spawn, direct, revive, integrate — and hold the merge authority (currently exercised via the pinned gh fallback — see §2 note), each for its own epic’s PRs. Workers never merge.
4. **PR-per-session under branch protection.** One session = one worktree = one branch = one PR. `main` requires the Skyvern matrix as status checks. No Graphite; no gt commands exist in this system.
5. **Superpowers change-type table is authoritative.** Feature = all stages; bug fix = skip brainstorm, plan optional; small refactor = skip brainstorm/plan/execute; config/docs = verify + finish only, review optional.
6. **Skyvern test commands verbatim:**`uv run python -m pytest tests/unit/ -v` · `uv run python -m pytest tests/scenario/ -v` · `cd skyvern-frontend && npx tsc --noEmit` · `./run_skyvern.sh` + curl smoke.
7. **Shared trunk → dark-safety at merge.** `main` is shared with human coworkers; any merged commit can reach PROD on *someone else’s* deploy at any moment, mid-epic. Therefore every slice must be dark-safe the instant it merges: (a) the PostHog flag key exists and defaults OFF in PROD **before slice 1 merges**, and code paths treat a missing flag as OFF; (b) DB migrations are expand/contract — additive now, destructive contraction only in a follow-up after the Stage-5 ramp completes; (c) each merge leaves `main` releasable on its own. The epic’s only unilateral rollback is flag OFF plus a revert-PR fix-forward of our slices — never a deploy rollback of shared `main`, which would revert coworkers’ work.

## 2. AO CLI conventions (current build)

| Task | Command |
| --- | --- |
| Spawn manager | `ao spawn --name <n> --project skyvern-cloud --issue <SKY-…> --prompt "<pointer to $BRIEFS/A<stage>-….md>"` — worker-kind; never `--kind orchestrator` (invariant 2) |
| Spawn worker | `ao spawn --name <n> --project skyvern-cloud --issue <SKY-…> --branch <branch> --prompt "<pointer to $BRIEFS/B<type>-….md>"` (add `--agent <harness>` to override; `--claim-pr <ref>` to adopt an orphaned PR) |
| Message a session | `ao send --session <id> --message "…"` (both flags required) |
| List sessions | `ao session ls [--json] [--all] [--include-terminated]` — note `ao status` is daemon health only |
| Revive / kill / reclaim | `ao session restore <id>` · `ao session kill <id>` (dirty workspaces preserved) · `ao session cleanup [--dry-run] -y` |
| Reviewer runs | `ao review trigger <worker-session-id>` · `ao review ls <worker-session-id>` · `ao review submit <worker-session-id>` · `ao review cancel <worker-session-id>` — display names are rejected (`REVIEW_NOT_FOUND`); resolve ids via `ao session ls -p skyvern-cloud --json` |
| Merge / comments | `gh pr merge <pr> --repo Skyvern-AI/skyvern-cloud --squash --match-head-commit <head-sha>` · `ao pr resolve-comments <pr-number> [comment-id…]` |
| Managers overview | `ao orchestrator ls [--json]` |
| Desktop app / health | `ao start` · `ao status` · `ao doctor` |

Briefs live in the agent-orchestrator fork, read via the absolute path `/Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/` — sessions run in skyvern-cloud worktrees, so relative paths will not resolve.

`ao pr merge` is broken in AO build f7637f78c — the CLI omits the prUrl/expectedHeadSha the daemon requires (no flag exists), and the daemon's CI rollup false-fails clean PRs (PR_PRECONDITIONS_UNMET). Bugs filed in agent-orchestrator; restore `ao pr merge` when fixed. The pinned `--match-head-commit` preserves the merge-gate discipline; squash is the only merge method skyvern-cloud allows.

Session ids are not display names, and neither `ao session ls` nor its `--json` form carries the `--name` value — record the id `ao spawn` prints (`spawned session <id> …`) at spawn time.

**Lifecycle automation (built in):** AO sends failing CI check names+links, focused unresolved review feedback, and merge-conflict rebase requests to the owning session, signature-deduplicated. Managers must not re-send unchanged feedback. Merge-ready and needs-input events create durable notifications in the desktop/mobile app — that is the human’s observability channel, never a gate after Stage 1. AO delivers review results to the owning worker the moment they land; a manager's corrective `ao send` can lose that race — send standing constraints BEFORE `ao review trigger`, and expect the worker may already have acted on raw review feedback. `ao send` prints nothing on success; `ao pr resolve-comments` prints "resolved 0 review thread(s)" when threads were already resolved — both informational; verify end state via gh when it matters.

---

## 3. Stage 1 — Continuous Exploration ⛔ HUMAN GATE

**SAFe activities → agents:** Hypothesize → telemetry-miner · Collaborate & Research → ux-scout · Architect → repo-analyst · Synthesize → Discovery Manager.

**Workers are read-only:** they live in project worktrees for repo access but produce written findings only — no commits, no pushes, no PRs.

**Output — Opportunity Report:** (a) findings + current-state value stream map with baselines (Process Time, Lead Time, Activity Ratio = PT/LT×100, %C&A); (b) hypotheses with PostHog/Datadog leading indicators; (c) idealized solution, MVP-scoped; (d) epic decomposition into **flag-shippable, PR-sized slices** with branch names `sky-<epic>/<area>/N-<slice>`, superpowers change type, and spawn-order (parallel vs dependent); (e) proposed Stage 2–4 gate thresholds.

**Gate mechanics:** the Discovery Manager ends by requesting input (“AUTONOMY REQUESTED — approve to proceed to Stages 2–3; QA certification is Gate 2 and unlocks Stages 4–5”), which raises a needs-input notification on desktop/Connect Mobile. Approval criteria: measurable hypothesis + indicators; MVP scope; every slice independently flag-shippable and PR-sized; rollback/monitor thresholds specified. The human approves in-app or via:

```bash
ao send --session sky4200-mgr --message "APPROVED: autonomy granted for SKY-4210 through Release on Demand."
```

**Command sequence:**

```bash
# Tier 1:
ao spawn --name sky4200-mgr --project skyvern-cloud --issue SKY-4200 \
  --prompt "You are the Discovery Epic Manager for epic SKY-4200 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A1-discovery-manager.md and follow it exactly; it defines your role, workers, monitoring, gate, and reporting. You coordinate; you never write product code."

# Tier 2 (inside the manager) — record the id each spawn prints; review commands reject display names:
ao spawn --name sky4201-repo --project skyvern-cloud --issue SKY-4201 \
  --prompt "You are a Tier-3 worker for SKY-4201 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B3-discovery-worker.md and follow it exactly. Parameters: ROLE=repo-analyst."
ao spawn --name sky4202-tlm --project skyvern-cloud --issue SKY-4202 \
  --prompt "You are a Tier-3 worker for SKY-4202 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B3-discovery-worker.md and follow it exactly. Parameters: ROLE=telemetry-miner."
ao spawn --name sky4203-ux --project skyvern-cloud --issue SKY-4203 \
  --prompt "You are a Tier-3 worker for SKY-4203 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B3-discovery-worker.md and follow it exactly. Parameters: ROLE=ux-scout."
ao session ls --json          # poll; ao session restore <id> on crash
```

## 4. Stage 2 — Continuous Integration 🤖 autonomous

**SAFe activities → agents:** Develop → worker TDD in its worktree · Build → merge train onto `main` (or an epic integration branch as PR base when whole-epic pre-main integration is required) · Test end-to-end → Skyvern matrix as required checks on every PR · Stage → last slice merged; hand off to Stage 3 (QA), which owns the STG deploy.

**Flow:** parallel-safe slices spawn together; dependent slices spawn only after their upstream PR merges; AO’s conflict handling asks surviving sessions to rebase after each merge. Coworker merges interleave with the train on their own schedule — that’s expected: invariant 7 makes order-independence the requirement, so sequencing is never assumed.

**Per-slice exit gate:** CI green + reviewer verdict approve (`ao review ls`) + zero unresolved comments (worker pushes fixes; manager `ao pr resolve-comments`) → **manager** merges in dependency order via `gh pr merge <pr> --repo Skyvern-AI/skyvern-cloud --squash --match-head-commit <head-sha>` (§2 note: `ao pr merge` is broken in build f7637f78c).
**Epic exit:** all slices merged; matrix green on `main`; smoke healthy; `ao session kill <id>` on every merged/finished worker FIRST, then `ao session cleanup -y` (cleanup reclaims only TERMINATED sessions, and its candidate set is project-wide, not epic-scoped); PR facts rolled up.

**Command sequence:**

```bash
ao spawn --name sky4210-mgr --project skyvern-cloud --issue SKY-4210 \
  --prompt "You are the Build Epic Manager for epic SKY-4210 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A2-build-manager.md and follow it exactly; it defines your decomposition, monitoring, exit gates, merge authority, and reporting. You coordinate and merge; you never write product code."

# Manager — each spawn prints `spawned session <id> …`; keep <FE_ID>/<BE_ID>, ids are not display names:
ao spawn --name sky4211-fe --project skyvern-cloud --issue SKY-4211 \
  --branch sky-4210/frontend/1-run-form-validation \
  --prompt "You are a Tier-3 worker for SKY-4211 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B1-new-feature.md and follow it exactly. Parameters: slice 1 run-form-validation, branch sky-4210/frontend/1-run-form-validation."
ao spawn --name sky4212-be --project skyvern-cloud --issue SKY-4212 \
  --branch sky-4210/backend/2-run-payload-fix \
  --prompt "You are a Tier-3 worker for SKY-4212 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B2-bug-fix.md and follow it exactly. Parameters: slice 2 run-payload-fix, branch sky-4210/backend/2-run-payload-fix, repro test <path>."

ao session ls --json         # status -> pr_open, but this JSON carries NO PR fields
gh pr list --repo Skyvern-AI/skyvern-cloud \
  --head sky-4210/frontend/1-run-form-validation --json number,baseRefName   # <- the PR number
ao review trigger <FE_ID> && ao review ls <FE_ID>
# slice 1 when its gate is green (`ao pr merge` is broken in f7637f78c — see the §2 note):
gh pr merge 14801 --repo Skyvern-AI/skyvern-cloud --squash --match-head-commit <head-sha>
# AO nudges sky4212-be to rebase if conflicted; then:
gh pr merge 14802 --repo Skyvern-AI/skyvern-cloud --squash --match-head-commit <head-sha>
# dependent slice only now:
ao spawn --name sky4213-doc --project skyvern-cloud --issue SKY-4213 \
  --branch sky-4210/frontend/3-copy-tweaks \
  --prompt "You are a Tier-3 worker for SKY-4213 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B7-config-docs.md and follow it exactly. Parameters: slice 3 copy-tweaks, branch sky-4210/frontend/3-copy-tweaks."
# kill each merged/finished worker FIRST — cleanup reclaims only TERMINATED sessions:
ao session kill <FE_ID>
ao session cleanup -y
```

## 5. Stage 3 — QA 🤖 autonomous loop · ⛔ HUMAN GATE on exit

**Why a stage:** SAFe folds testing into CI’s Test-end-to-end and Stage sub-dimensions; here they are deliberately promoted to a standalone stage because agents test their own code in Stage 2 — a fresh adversarial pass against the *integrated* build, with its own manager, loop, and report, covers the same-author blind spot.

**The round loop** (owned end-to-end by the QA Epic Manager):
1. **Deploy to STG**: integrated `main` deployed to staging, feature flag ON in STG only. **Record the SHA under test** — STG is shared and coworkers may redeploy it mid-round; every finding and the round report are stamped with the SHA, and open criticals are re-verified if the SHA moves.
2. **QA pass**: spawn adversarial QA worker(s) (B.8) against STG — hostile paths first, mutation-based test-adequacy audit, frontend states the matrix can’t see.
3. **File bugs**: every finding becomes a Linear ticket (team “Skyvern AI”) with severity, repro recipe, failing-repro-test path, and a triage label: **OURS** (attributable to this epic’s slices) or **PRE-EXISTING** (main/coworker regression). Only OURS criticals/majors block our exit; PRE-EXISTING criticals are escalated by notification to the humans — filed, never silently absorbed into our fix train.
4. **Fix train**: one B.2 bug-fix worker per CRITICAL/MAJOR (the repro test is its RED); reviewer verdict; QA Manager merges fixes (branch scheme `sky-<epic>/qafix/R<round>-<slug>`). MINOR: fix or defer with rationale.
5. **Redeploy STG → re-QA**: increment the round, go to step 2.

**Exit — two gates:**
- **Gate 1 (automated):** a round with zero unresolved OURS CRITICAL/MAJOR findings → PASS → final QA report produced.
- **Gate 2 (HUMAN):** the QA Manager raises a needs-input notification — “QA CERTIFIED — approve to proceed to Stages 4–5” — with the final report attached. Handoff to Stage 4 happens only on explicit approval, in-app or via `ao send --session sky4215-mgr --message "APPROVED: proceed to CD/RoD."`

**Round cap:** 3 rounds without convergence → stop and escalate to the human with the round-3 report (failure-path circuit-breaker; distinct from Gate 2, which sits on the success path).

**The QA report** (every round + final; written for a human skimming on a phone, ≤1 page, no agent jargon): build/commit + STG environment; what was exercised; findings table (Linear ID, severity, one-line description, status); round-over-round delta (found / fixed / deferred); test-adequacy verdict; explicit PASS or FAIL + reason. Delivered in the roll-up and committed to `docs/agentic-devops/qa/SKY-<epic>/round-N.md` (this repo — the B.7 docs worker that commits it is spawned in project agent-orchestrator, not skyvern-cloud; process artifacts never land in the product repo) via a B.7 docs worker.

**Command sequence:**

```bash
ao spawn --name sky4215-mgr --project skyvern-cloud --issue SKY-4215 \
  --prompt "You are the QA Epic Manager for epic SKY-4215 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A3-qa-manager.md and follow it exactly; it defines the QA round loop, Linear filing, fix-train merges, reports, and both exit gates. You coordinate and merge qafix PRs; you never write product code."
# Manager, per round:
ao spawn --name sky4216-qa --project skyvern-cloud --issue SKY-4216 \
  --prompt "You are a Tier-3 worker for SKY-4216 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B8-adversarial-qa.md and follow it exactly. Parameters: QA round <N> against STG, SHA under test <sha>, flag <flag-key> ON in STG only."
ao session ls --json         # status -> pr_open, but this JSON carries NO PR fields
gh pr list --repo Skyvern-AI/skyvern-cloud --head sky-4210/qafix/R1-payload-overflow \
  --json number,baseRefName                                       # <- the PR number
# per CRITICAL/MAJOR finding:
ao spawn --name sky4217-fx1 --project skyvern-cloud --issue SKY-4217 \
  --branch sky-4210/qafix/R1-payload-overflow \
  --prompt "You are a Tier-3 worker for SKY-4217 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B2-bug-fix.md and follow it exactly. Parameters: finding <LINEAR-ID> payload overflow, failing repro test <path>, branch sky-4210/qafix/R1-payload-overflow."
ao review trigger <FX1_ID>   # session id from the spawn output, not the display name
gh pr merge <pr> --repo Skyvern-AI/skyvern-cloud --squash --match-head-commit <head-sha>
# redeploy STG, respawn B.8, repeat until PASS or round cap
```

## 6. Stage 4 — Continuous Deployment 🤖 autonomous

**Shared-trunk reality:** our commits may already be in PROD — dark — carried by a coworker’s deploy before this stage runs. Stage 4 therefore **ensures and verifies** dark presence in PROD; it performs the deploy only when nothing has shipped the QA-certified commit yet.

**SAFe activities → agents:** Deploy → *ensure*: if PROD’s deployed SHA already contains the QA-certified commit, skip to Verify; else deploy (flag OFF, commit-correlated) · Verify → `tests/scenario/` subset + `./run_skyvern.sh` curl smoke + Datadog Synthetics · Monitor → composite Datadog monitor (us5) scoped to our commits/release tag · Respond → flag stays OFF + revert-PR fix-forward; never a deploy rollback of shared main.

**Gate — two modes:**
- *We own the deploy* (nothing has shipped our commit yet): deployment canary 1% → 5% → 25% → 50% → 100%, hold ≈10 min per step; advance only while the composite monitor is green (error-rate delta ≤ baseline + 1%; p95 ≤ baseline + budget; no new APM anomaly; saturation in limits). Breach → roll back **our** deploy, halt, incident.
- *A coworker’s deploy carried it* (the common case): no deploy to canary — run the Verify subset, then a soak window (≥ the canary duration) watching commit-scoped monitors for dark-code toxicity (migration effects, startup, always-on paths). Breach → spawn a revert-PR fix-forward worker for the offending slice(s), merge it, ensure it deploys; flag remains OFF throughout. (Thresholds are staging-calibrated starting points. The true progressive exposure for this epic is the Stage-5 flag ramp, not the deploy.)

**Command sequence:**

```bash
ao spawn --name sky4220-mgr --project skyvern-cloud --issue SKY-4220 \
  --prompt "You are the Deploy Epic Manager for epic SKY-4220 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A4-deploy-manager.md and follow it exactly; it defines ensure-vs-deploy modes, gates, and response rules. You coordinate; you never write product code."
# Manager:
ao spawn --name sky4221-dv --project skyvern-cloud --issue SKY-4221 \
  --prompt "You are a Tier-3 worker for SKY-4221 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B4-deploy-verification.md and follow it exactly. Parameters: QA-certified SHA <sha>, flag <flag-key> OFF in PROD."
ao session ls --json
```

## 7. Stage 5 — Release on Demand 🤖 autonomous

**SAFe activities → agents:** Release → progressive PostHog flag flip · Stabilize → SLO/error-budget watch, kill switch = flag OFF · Measure → PostHog experiment (variants incl. control; primary metric = Stage-1 leading indicator; criteria pre-registered; holdout for persistence) · Learn → invest/pivot/persevere → seeds next Stage 1.

**Triggers:** flip only if Stage-4 gates green, error budget intact, experiment pre-registered. In a shared trunk the flag ramp *is* the epic’s progressive exposure — the only ramp we fully control. Post-release SLO breach or budget burn past threshold → flag OFF. Validated → ramp to 100% + scale follow-up; invalidated → flag OFF + pivot/stop finding. Either way, the Learn decision is rolled up as the next discovery seed.

**Command sequence:**

```bash
ao spawn --name sky4230-mgr --project skyvern-cloud --issue SKY-4230 \
  --prompt "You are the Release Epic Manager for epic SKY-4230 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A5-release-manager.md and follow it exactly; it defines flag-flip criteria, stabilization, measurement, and the Learn roll-up. You coordinate; you never write product code."
# Manager:
ao spawn --name sky4231-rm --project skyvern-cloud --issue SKY-4231 \
  --prompt "You are a Tier-3 worker for SKY-4231 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B5-release-measurement.md and follow it exactly. Parameters: flag <flag-key>, PostHog experiment <id>, primary metric <leading-indicator>."
ao session ls --json
```

---

# Appendix A — Epic Manager briefs (files under docs/agentic-devops/briefs/; spawn passes a pointer prompt)

## A.1 — Discovery Epic Manager (Stage 1)

→ briefs/A1-discovery-manager.md

## A.2 — Build Epic Manager (Stage 2)

→ briefs/A2-build-manager.md

## A.3 — QA Epic Manager (Stage 3)

→ briefs/A3-qa-manager.md

## A.4 — Deploy Epic Manager (Stage 4)

→ briefs/A4-deploy-manager.md

## A.5 — Release Epic Manager (Stage 5)

→ briefs/A5-release-manager.md

---

# Appendix B — Worker preambles (files under docs/agentic-devops/briefs/; spawn passes a pointer prompt)

## B.1 — New feature (all superpowers stages)

→ briefs/B1-new-feature.md

## B.2 — Bug fix (skip brainstorm; plan optional)

→ briefs/B2-bug-fix.md

## B.3 — Discovery worker (Stage 1, read-only; set ROLE per worker)

→ briefs/B3-discovery-worker.md

## B.4 — Deployment-verification worker (Stage 4)

→ briefs/B4-deploy-verification.md

## B.5 — Release / measurement worker (Stage 5)

→ briefs/B5-release-measurement.md

## B.6 — Small refactor (skip brainstorm/plan/execute)

→ briefs/B6-small-refactor.md

## B.7 — Config / docs only (verify + finish; review optional)

→ briefs/B7-config-docs.md

## B.8 — Adversarial QA worker (Stage 3 — QA rounds)

→ briefs/B8-adversarial-qa.md

---

# Appendix C — Gates, CALMR, DORA (quick reference)

## C.1 Gate table

| Stage | Gate | Pass criteria |
| --- | --- | --- |
| 1 CE | **HUMAN** | Report approved: measurable hypothesis + indicators; MVP scope; flag-shippable PR-sized slices; thresholds specified |
| 2 CI | Automated, manager-enacted | Precondition: flag exists + OFF in PROD; expand-only migrations (invariant 7). Per slice: CI green + reviewer approve (incl. test adequacy) + 0 unresolved comments -> manager merges (`gh pr merge <pr> --repo Skyvern-AI/skyvern-cloud --squash --match-head-commit <head-sha>`; `ao pr merge` broken in f7637f78c, see §2). Epic: matrix green on main; smoke healthy; cleanup run |
| 3 QA | Automated loop + **HUMAN** exit gate | Per round: STG deploy (flag ON in STG) + B.8 pass + all OURS CRITICAL/MAJOR fixed-and-merged (minors deferred with rationale). Gate 1 (auto): clean round -> final report. Gate 2 (HUMAN): approve the report -> unlock Stages 4-5. Round cap 3 -> failure-path circuit-breaker |
| 4 CD | Automated | Ensure QA-certified SHA is in PROD dark: we deploy -> canary steps green (err <= +1%, p95 <= baseline+budget, no APM anomaly), breach rolls back OUR deploy; coworker’s deploy carried it -> verify + soak on commit-scoped monitors, toxicity -> revert-PR fix-forward. Flag stays OFF; shared deploys are never rolled back |
| 5 RoD | Automated | Flip only if Stage-4 green + budget intact + experiment pre-registered; kill switch on SLO breach; experiment to significance; Learn rule applied |

## C.2 CALMR in this system

- **Culture** — ownership encoded in tiers; roll-ups (PR facts, evidence, follow-ups) from every worker; one well-defined human gate.
- **Automation** — everything after Stage 1: TDD, matrix checks, reviewer verdicts, canary gates, auto-rollback, experiments.
- **Lean flow** — PR-sized flag-shippable slices; change-type table prevents over-processing; VSM baselines set in Stage 1.
- **Measurement** — Datadog (flow/health) + PostHog (value) + DORA (delivery); hypothesis metrics, not vanity metrics.
- **Recovery** — dark deploys, canary auto-rollback, flag kill switch, incident records; manager halts the ramp on breach.

## C.3 DORA instrumentation

| Metric | Source | Elite reference |
| --- | --- | --- |
| Deployment frequency | Stage-3 production deploys (Datadog deploy markers) | On demand |
| Lead time for changes | First slice commit -> production deploy | < 1 day |
| Change failure rate | Rollback/incident deploys ÷ total | ≈ 5% |
| Recovery time | Guardrail breach -> rollback/flag-off complete | < 1 hour |

Pair with the PostHog value loop — throughput metrics alone can mask churn in AI-generated code.

## C.4 Health Radar ownership (aspect -> sub-dimensions -> owner)

- **CE**: Hypothesize -> telemetry-miner · Collaborate & Research -> ux-scout · Architect -> repo-analyst · Synthesize -> Discovery Manager
- **CI**: Develop -> slice workers · Build -> Build Manager (merge train) · Test e2e -> matrix checks (per-PR) · Stage -> hand-off to QA
- **QA (team extension of Test e2e/Stage)**: adversarial pass -> B.8 worker · bug loop + merges -> QA Manager · human-readable report -> QA Manager
- **CD**: Deploy / Verify / Monitor -> deployment-verification worker · Respond -> Deploy Manager + worker
- **RoD**: Release / Stabilize / Measure -> release-measurement worker · Learn -> Release Manager (-> next CE)

---

## Change log

- **v1.4 (2026-08-23)** — Fitted to the verified AO build (see architectural-review-2026-08-23) and rehomed to AronPerez/agent-orchestrator: Epic Managers are worker-kind sessions acting by brief (AO allows one active orchestrator per project; a second --kind orchestrator spawn silently no-ops); briefs moved from --prompt inlining to committed files under docs/agentic-devops/briefs/ read via absolute path from skyvern-cloud worktrees, with <1 KB pointer prompts (AO caps spawn prompts at 4096 bytes, PROMPT_TOO_LONG); appendices hollowed to an index; ao session cleanup runs with -y unattended; doctor gate is "no FAILs"; -prompt typos fixed; QA reports rehomed to this repo (docs/agentic-devops/qa/). Preflight pilot passed 2026-08-23 (Skyvern-AI/skyvern-cloud#15945, scratch branches, main untouched).
- **Final (2026-08-23)** — signed off; operational as written.
- **v1.3 (2026-08-22)** — QA exit becomes the second human gate: Gate 1 (automated) = clean round with zero OURS critical/major findings; Gate 2 (HUMAN) = approval of the final QA report via needs-input notification, required before Stage 4 begins. Stage-1 approval now explicitly covers Stages 2-3 only; invariant 1 rewritten to “two human gates”; round cap retained as the failure-path circuit-breaker.
- **v1.2 (2026-08-22)** — Shared-trunk hardening (coworkers merge/deploy main on their own schedule): invariant 7 makes dark-safety a merge criterion (flag exists + OFF in PROD before slice 1, missing-flag = OFF, expand/contract migrations, every merge releasable); Stage 4 reframed to ensure-and-verify with two modes (we-deploy canary vs coworker-carried soak) and revert-PR fix-forward replacing deploy rollback of shared main; Stage-5 flag ramp named the epic’s true progressive exposure + post-ramp flag/migration-contraction cleanup tickets; QA rounds SHA-stamped with OURS vs PRE-EXISTING finding triage.
- **v1.1 (2026-08-22)** — Dedicated QA stage inserted between CI and CD (five stages now): QA Epic Manager owns the STG-deploy -> adversarial pass -> Linear bugs -> fix train -> redeploy -> re-QA loop, exits on a clean round, caps at 3 rounds with a human circuit-breaker, and emits a <=1-page human-readable report every round (committed to docs/qa/). Reviewer test-adequacy rubric retained in Stage 2; B.8 rehomed to Stage 3; downstream stages renumbered.
- **v1.0 (2026-08-22)** — Graphite removed end-to-end (D1 in the design report): stack layers -> sessions/PRs; restack -> merge trains; review worker -> native `ao review`; all CLI updated to current build syntax (`ao spawn` flag-based, `ao send --session/--message`, `ao session ls` for sessions, `ao start` for the app); human-gate mechanics wired to AO needs-input notifications; manager-only merge authority added as invariant 3/4.
- **v0.x** — gt-based drafts (superseded; see chat artifact + delta doc for history).
