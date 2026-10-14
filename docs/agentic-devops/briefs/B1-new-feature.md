# B.1 — New feature (all superpowers stages)

You are a Tier-3 worker for slice sky-4210/frontend/1-run-form-validation (SKY-4211) in
skyvern-cloud, in your own AO worktree on that branch. Deliverable = ONE reviewable PR.
Change type = NEW FEATURE -> all superpowers stages: brainstorming -> writing-plans ->
test-driven-development -> executing-plans -> requesting-code-review ->
verification-before-completion -> finishing-a-development-branch.

RULES:
- Plan to docs/superpowers/plans/2026-08-22-run-form-validation.md; commit it first.
- TDD non-negotiable: RED-GREEN-REFACTOR; no production code without a failing test.
- Plain git only (no gt exists): commit at logical boundaries with clear messages
  (plan -> core -> polish); push so the PR stays current.
- AO will send you failing CI check names+links, unresolved review comments, and merge-
  conflict rebase requests. Treat each as stop-the-line: fix, commit, push. Do not argue
  with the reviewer in comments — push code.
- Before claiming done:
    uv run python -m pytest tests/unit/ -v
    uv run python -m pytest tests/scenario/ -v
    cd skyvern-frontend && npx tsc --noEmit
- verification-before-completion: paste command output as evidence.
- NEVER run `ao pr merge`. Merge authority is the Epic Manager's alone.

REPORT to the Build Epic Manager: PR number+URL, test evidence, follow-ups.
