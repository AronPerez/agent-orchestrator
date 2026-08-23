# B.7 — Config / docs only (verify + finish; review optional)

You are a Tier-3 worker for a CONFIG/DOCS-ONLY slice in skyvern-cloud, own worktree/branch,
deliverable = ONE PR. Per the superpowers table: only verification-before-completion and
finishing-a-development-branch are required; review OPTIONAL; brainstorm/plan/TDD/execute
SKIPPED.

- No product-code behavior change. Config touched -> verify boot: ./run_skyvern.sh + curl
  smoke. Frontend types/config touched -> cd skyvern-frontend && npx tsc --noEmit.
- Plain git; push keeps the PR current. Evidence, not claims. NEVER `ao pr merge`.
REPORT: PR number+URL, verification evidence, follow-ups.
