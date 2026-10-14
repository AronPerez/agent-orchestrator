# PR management: real merge/close + triage redesign

**Date:** 2026-07-09
**Decision (user):** one deliverable — real in-app **Merge & Close** backed by the daemon, **plus** the PRs-screen redesign (triage + density + dead-session section). Ships only when merge/close genuinely work. Merge/close are irreversible → always behind an explicit confirm; never bulk.

## Why (frame)
The mobile PRs screen (`packages/mobile/app/(tabs)/prs.tsx`) is a flat grid of 100+ cards with only project + open/merged/all filters and Session/Open actions. Hard to find what needs action; PRs from dead sessions bury live work; no way to act without leaving the app. **And** the daemon's PR actions are stubs: `ActionService.Merge` (`backend/internal/service/pr/action_service.go:37`) returns fake success without calling GitHub; there is no Close. Wiring buttons to the stub would report merges that never happened — so the backend must be made real first/with the UI.

## Part A — Backend: real Merge & Close

**Existing to reuse:**
- GitHub `Client` (token-authed REST+GraphQL) — `backend/internal/adapters/scm/github/` (`NewClient(ClientOptions{...})`, `TokenSource`). Currently observe-only.
- Route + envelopes: `controllers/prs.go` (`POST /prs/{id}/merge`, sentinel-error mapping via `writePRError`), `MergePRResponse`.
- Sentinels already defined: `ErrPRNotFound` (404), `ErrPRNotMergeable` (409), `ErrPRPreconditions` (422).
- PR facts (owner/repo/number/mergeability/state) are stored via the observation layer (session PR observations). The service resolves `{id}` (PR number) → owner/repo from there.

**Build:**
1. **SCM action port** (`backend/internal/ports`): `SCMPRActions` with `MergePR(ctx, owner, repo, number int, method string) error` and `ClosePR(ctx, owner, repo, number int) error`.
2. **GitHub impl** on the existing client/provider: `MergePR` → REST `PUT /repos/{owner}/{repo}/pulls/{n}/merge` with `{"merge_method":"squash"}`; `ClosePR` → REST `PATCH /repos/{owner}/{repo}/pulls/{n}` with `{"state":"closed"}`. Reuse the client's token auth + base URL. Map 405/409 (not mergeable) → `ErrPRNotMergeable`, 404 → `ErrPRNotFound`, 422 → `ErrPRPreconditions`.
3. **Wire `ActionService`**: give it the PR-fact lookup + the `SCMPRActions` port (replace the empty struct). `Merge` resolves number→owner/repo, calls `MergePR`, returns `MergeResult{PRNumber, Method:"squash"}`. Add `Close(ctx, prID) (CloseResult, error)` → `ClosePR`. Keep the stub behavior only as the injected-nil fallback (route returns 501 when Svc/provider absent, matching the existing `NotImplemented` pattern).
4. **Route + response**: add `POST /prs/{id}/close` in `controllers/prs.go`; add `ClosePRResponse{OK, PRNumber}`; map errors via `writePRError`.
5. **openapi**: add `/api/v1/prs/{id}/close` (mirror the merge path/operationId/responses) in `apispec/openapi.yaml`; regenerate anything derived.
6. **Tests**: mirror `controllers/prs_test.go` merge cases for close (200/404/409/501) + service-level tests with a fake `SCMPRActions`. Do NOT hit real GitHub in tests.

**Guardrails:** these perform irreversible GitHub operations. No retries that could double-merge. The daemon action must be idempotent-safe (a second merge of an already-merged PR → clean 409, not a crash).

## Part B — Frontend: redesign + gated actions (`packages/mobile`)

Reuse `lib/theme.ts`, `lib/ui.tsx`, `lib/responsive.tsx` (from the just-shipped responsive work), and the north-star board pattern (`frontend/src/renderer/components/SessionsBoard.tsx`).

**Triage IA** — replace the flat list with status-grouped sections in action order, each collapsible with a count:
- **Needs you** ← CI failing / changes requested
- **Ready to merge** ← approved AND CI passing (mergeable)
- **In review** ← open, review pending
- **Merged** (collapsed by default)
Add a **search** box (repo / #/ title) and a **sort** control (updated, CI, review).

**Density toggle** — card grid (current `CardGrid`) ↔ compact **table** rows (repo · # · title · CI · review · +/- · age), one row each. Persist the choice.

**Dead-session section** — PRs whose owning session is terminal (`api.ts` `TERMINAL_STATUSES`: killed/terminated/done/cleanup/errored/merged) are pulled OUT of the main groups into a collapsed **"Dead sessions"** section at the bottom, so live work isn't buried.

**Gated actions per PR:**
- **Merge** — only shown when mergeable; opens a confirm dialog ("Squash-merge #N?") → `api.mergePR()`. On success, optimistic move to Merged + refresh. On 409/422, surface the daemon message.
- **Close** — confirm dialog → new `api.closePR()` (add it, mirroring `mergePR` → `POST ${API}/prs/${n}/close`). On success, move out of active groups + refresh.
- **Open** (GitHub) and **Session** stay. No bulk destructive actions.
- Both destructive actions disabled while in flight; never auto-fire.

**API contract (both parts agree on this):**
- `POST /api/v1/prs/{id}/merge` → `{ok, prNumber, method}` (exists)
- `POST /api/v1/prs/{id}/close` → `{ok, prNumber}` (new)

## Gap ledger
- **Backend merge/close is net-new real logic** (was stubbed) — the bulk of the risk. SCM action port + GitHub REST impl + close route + tests.
- Frontend: no new visual primitives; a `Table`/dense-row layout is new composition from existing tokens. Density-persistence uses existing config/store.

## Verification
1. **Backend mechanical**: `go test ./...` + golangci-lint (repo `npm run lint`). New tests green; nothing hits real GitHub.
2. **Contract**: openapi regenerated; `frontend`/mobile types match.
3. **Frontend mechanical**: `cd packages/mobile && npx tsc --noEmit`; prettier clean (pre-commit hook now enforces it).
4. **Integration (the real proof)**: against a throwaway test PR on a scratch repo, confirm the in-app Merge actually squash-merges on GitHub and Close actually closes — because the whole point was that the stub faked it. Drive via `ao preview` / browser.
5. **a11y**: confirm dialogs focus-trap correctly; every action reachable by keyboard.

## Out of scope
- Bulk actions; reopen; merge methods other than squash; comment/approve from the app (resolve-comments already has a separate stubbed route — not part of this).
