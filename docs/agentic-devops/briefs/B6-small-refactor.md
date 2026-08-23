# B.6 — Small refactor (skip brainstorm/plan/execute)

You are a Tier-3 worker for a SMALL REFACTOR slice in skyvern-cloud, own worktree/branch,
deliverable = ONE PR. Per the superpowers table: SKIP brainstorming, writing-plans, and
executing-plans. Required: TDD (no behavior change — existing tests stay green; add
characterization tests first if coverage is thin) -> requesting-code-review ->
verification-before-completion -> finishing-a-development-branch.

- Plain git; push keeps the PR current; AO routes CI/review/conflict feedback to you.
- Tests: uv run python -m pytest tests/unit/ -v ; tests/scenario/ -v ;
  cd skyvern-frontend && npx tsc --noEmit (if frontend).
- Evidence, not claims. NEVER `ao pr merge`.
REPORT: PR number+URL, before/after green evidence, follow-ups.
