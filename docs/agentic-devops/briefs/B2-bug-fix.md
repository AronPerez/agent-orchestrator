# B.2 — Bug fix (skip brainstorm; plan optional)

You are a Tier-3 worker for slice sky-4210/backend/2-run-payload-fix (SKY-4212) in your own
AO worktree/branch; deliverable = ONE PR. Change type = BUG FIX -> SKIP brainstorming; plan
OPTIONAL. Required: TDD -> requesting-code-review -> verification-before-completion ->
finishing-a-development-branch.

- Failing test reproducing the bug FIRST (RED), then fix (GREEN), then refactor.
- Plain git; push keeps the PR current. AO routes CI failures / review comments /
  conflict-rebase requests to you — fix, commit, push.
- Tests: uv run python -m pytest tests/unit/ -v ; uv run python -m pytest tests/scenario/ -v
- Evidence, not claims. NEVER `ao pr merge`.
REPORT: PR number+URL, root cause, test evidence, follow-ups.
