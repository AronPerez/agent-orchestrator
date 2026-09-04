# Pointer prompts — ready to paste

AO caps `ao spawn --prompt` at **4096 bytes** (`PROMPT_TOO_LONG`, `backend/internal/httpd/controllers/sessions.go:39`), so briefs are committed files and every spawn passes a
short pointer to one. Every prompt below is measured with `wc -c` and is well under the 1024-byte
working budget this playbook holds itself to.

Sessions run in **skyvern-cloud** worktrees while the briefs live in the **agent-orchestrator** fork, so
every pointer uses the absolute path `/Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/`.

Managers are **worker-kind** sessions — never `--kind orchestrator` (AO allows one active orchestrator per
project and a second spawn silently returns the existing session). Record the session id each spawn prints
(`spawned session <id> …`): review and session commands reject display names.

`<ANGLE-BRACKET>` slots are spawn-time parameters, not TODOs.

---

## Tier-1 → Epic Manager spawns (Appendix A)

### Discovery Epic Manager — sky4200-mgr (SKY-4200)

```bash
ao spawn --name sky4200-mgr --project skyvern-cloud --issue SKY-4200 \
  --prompt "You are the Discovery Epic Manager for epic SKY-4200 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A1-discovery-manager.md and follow it exactly; it defines your role, workers, monitoring, gate, and reporting. You coordinate; you never write product code."
```
`wc -c` = **351 bytes**

### Build Epic Manager — sky4210-mgr (SKY-4210)

```bash
ao spawn --name sky4210-mgr --project skyvern-cloud --issue SKY-4210 \
  --prompt "You are the Build Epic Manager for epic SKY-4210 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A2-build-manager.md and follow it exactly; it defines your decomposition, monitoring, exit gates, merge authority, and reporting. You coordinate and merge; you never write product code."
```
`wc -c` = **376 bytes**

### QA Epic Manager — sky4215-mgr (SKY-4215)

```bash
ao spawn --name sky4215-mgr --project skyvern-cloud --issue SKY-4215 \
  --prompt "You are the QA Epic Manager for epic SKY-4215 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A3-qa-manager.md and follow it exactly; it defines the QA round loop, Linear filing, fix-train merges, reports, and both exit gates. You coordinate and merge qafix PRs; you never write product code."
```
`wc -c` = **386 bytes**

### Deploy Epic Manager — sky4220-mgr (SKY-4220)

```bash
ao spawn --name sky4220-mgr --project skyvern-cloud --issue SKY-4220 \
  --prompt "You are the Deploy Epic Manager for epic SKY-4220 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A4-deploy-manager.md and follow it exactly; it defines ensure-vs-deploy modes, gates, and response rules. You coordinate; you never write product code."
```
`wc -c` = **343 bytes**

### Release Epic Manager — sky4230-mgr (SKY-4230)

```bash
ao spawn --name sky4230-mgr --project skyvern-cloud --issue SKY-4230 \
  --prompt "You are the Release Epic Manager for epic SKY-4230 in skyvern-cloud — a worker-kind AO session acting as Epic Manager. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/A5-release-manager.md and follow it exactly; it defines flag-flip criteria, stabilization, measurement, and the Learn roll-up. You coordinate; you never write product code."
```
`wc -c` = **365 bytes**

---

## Epic Manager → worker spawns (Appendix B)

Template — fill `<ISSUE>`, `<FILE>`, and the one-line parameters:

```
You are a Tier-3 worker for <ISSUE> in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/<FILE> and follow it exactly. Parameters: <ONE LINE: slice+branch / B3 ROLE / B2 finding+repro-test path>.
```
`wc -c` = **232 bytes** (unfilled template)

### B1 — sky4211-fe (SKY-4211) — playbook §4

```bash
ao spawn --name sky4211-fe --project skyvern-cloud --issue SKY-4211 \
  --branch sky-4210/frontend/1-run-form-validation \
  --prompt "You are a Tier-3 worker for SKY-4211 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B1-new-feature.md and follow it exactly. Parameters: slice 1 run-form-validation, branch sky-4210/frontend/1-run-form-validation."
```
`wc -c` = **256 bytes**

### B2 — sky4212-be (SKY-4212) — playbook §4

```bash
ao spawn --name sky4212-be --project skyvern-cloud --issue SKY-4212 \
  --branch sky-4210/backend/2-run-payload-fix \
  --prompt "You are a Tier-3 worker for SKY-4212 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B2-bug-fix.md and follow it exactly. Parameters: slice 2 run-payload-fix, branch sky-4210/backend/2-run-payload-fix, repro test <path>."
```
`wc -c` = **262 bytes**

### B3 — sky4201-repo (SKY-4201) — playbook §3

```bash
ao spawn --name sky4201-repo --project skyvern-cloud --issue SKY-4201 \
  --prompt "You are a Tier-3 worker for SKY-4201 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B3-discovery-worker.md and follow it exactly. Parameters: ROLE=repo-analyst."
```
`wc -c` = **203 bytes**

### B4 — sky4221-dv (SKY-4221) — playbook §6

```bash
ao spawn --name sky4221-dv --project skyvern-cloud --issue SKY-4221 \
  --prompt "You are a Tier-3 worker for SKY-4221 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B4-deploy-verification.md and follow it exactly. Parameters: QA-certified SHA <sha>, flag <flag-key> OFF in PROD."
```
`wc -c` = **240 bytes**

### B5 — sky4231-rm (SKY-4231) — playbook §7

```bash
ao spawn --name sky4231-rm --project skyvern-cloud --issue SKY-4231 \
  --prompt "You are a Tier-3 worker for SKY-4231 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B5-release-measurement.md and follow it exactly. Parameters: flag <flag-key>, PostHog experiment <id>, primary metric <leading-indicator>."
```
`wc -c` = **265 bytes**

### B6 — sky4214-rfc (SKY-4214) — playbook (no §3–§5 example; branch scheme applied)

```bash
ao spawn --name sky4214-rfc --project skyvern-cloud --issue SKY-4214 \
  --branch sky-4210/backend/4-extract-run-validator \
  --prompt "You are a Tier-3 worker for SKY-4214 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B6-small-refactor.md and follow it exactly. Parameters: small refactor extract-run-validator, branch sky-4210/backend/4-extract-run-validator."
```
`wc -c` = **269 bytes**

### B7 — sky4213-doc (SKY-4213) — playbook §4

```bash
ao spawn --name sky4213-doc --project skyvern-cloud --issue SKY-4213 \
  --branch sky-4210/frontend/3-copy-tweaks \
  --prompt "You are a Tier-3 worker for SKY-4213 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B7-config-docs.md and follow it exactly. Parameters: slice 3 copy-tweaks, branch sky-4210/frontend/3-copy-tweaks."
```
`wc -c` = **240 bytes**

### B8 — sky4216-qa (SKY-4216) — playbook §5

```bash
ao spawn --name sky4216-qa --project skyvern-cloud --issue SKY-4216 \
  --prompt "You are a Tier-3 worker for SKY-4216 in skyvern-cloud. Open /Users/amongstar/dev/agent-orchestrator/docs/agentic-devops/briefs/B8-adversarial-qa.md and follow it exactly. Parameters: QA round <N> against STG, SHA under test <sha>, flag <flag-key> ON in STG only."
```
`wc -c` = **262 bytes**

---

## Byte-count table

| Prompt | bytes | limit |
| --- | --- | --- |
| A1 Discovery manager | 351 | < 1024 |
| A2 Build manager | 376 | < 1024 |
| A3 QA manager | 386 | < 1024 |
| A4 Deploy manager | 343 | < 1024 |
| A5 Release manager | 365 | < 1024 |
| B1 worker (sky4211-fe) | 256 | < 1024 |
| B2 worker (sky4212-be) | 262 | < 1024 |
| B3 worker (sky4201-repo) | 203 | < 1024 |
| B4 worker (sky4221-dv) | 240 | < 1024 |
| B5 worker (sky4231-rm) | 265 | < 1024 |
| B6 worker (sky4214-rfc) | 269 | < 1024 |
| B7 worker (sky4213-doc) | 240 | < 1024 |
| B8 worker (sky4216-qa) | 262 | < 1024 |
| worker template (unfilled) | 232 | < 1024 |
