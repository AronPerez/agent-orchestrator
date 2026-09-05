# AGENTS.md

Operational guidance for coding agents working in this repository. Keep changes small, match the current rewrite architecture, and prefer the documented daemon/API boundaries over behavior from the old TypeScript implementation.

## Repo layout

- `backend/` — Go rewrite of Agent Orchestrator: Cobra `ao` CLI, loopback HTTP daemon, services, SQLite storage, lifecycle/reaper, runtime/workspace/agent/tracker adapters, terminal mux, and tests.
- `frontend/` — Electron + React supervisor wired to the daemon via the generated typed client. Treat it as a thin supervisor/UI surface; do not move daemon logic into it.
- `docs/` — current architecture/status notes. Start here before changing lifecycle, CLI, agents, storage, or daemon behavior.
- `test/` — external smoke/e2e assets, including the CLI fresh-install container check.
- `.github/workflows/` — CI definitions. Mirror these commands locally when possible.

## Commands

From the repo root unless noted:

```bash
npm run lint                         # backend go test ./... + golangci-lint v2.12.2
npm run frontend:typecheck           # frontend TypeScript check
npm run sqlc                         # regenerate backend/internal/storage/sqlite/gen from queries/schema
npm run api                          # regenerate OpenAPI spec + frontend TS types (see API contract changes below)
npx @redwoodjs/agent-ci run --all    # local workflow validation; requires Docker socket
```

Backend-specific checks:

```bash
cd backend
go build ./...
go test ./...
go test -race ./...
go vet ./...
go run ./cmd/ao start
```

Frontend-specific checks:

```bash
cd frontend
npm run typecheck
npm run build
```

When showing or demoing frontend changes, run `ao preview [url]` from inside the session so the change renders in the desktop browser panel (the inspector rail's Browser tab); do not just describe it.

## Where to look first

- `README.md` — current run/config/test quickstart.
- `docs/README.md` — docs index.
- `docs/architecture.md` — backend mental model, package layout, lifecycle/session/service boundaries, and load-bearing rules.
- `docs/STATUS.md` — what is shipped on `main` today and what is still in flight.
- `docs/cli/README.md` — intended CLI shape: thin Cobra client over daemon HTTP, never direct storage/runtime access.
- `CLAUDE.md` — compatibility pointer for Claude Code; it directs agents back to `AGENTS.md`.

For code entry points:

- CLI commands: `backend/internal/cli/*.go`; follow nearby command/test patterns before adding a new style.
- HTTP controllers and DTOs: `backend/internal/httpd/controllers/`.
- Service read/write boundaries: `backend/internal/service/`.
- Domain vocabulary: `backend/internal/domain/`.
- Port contracts: `backend/internal/ports/`.
- SQLite queries/migrations/store: `backend/internal/storage/sqlite/`.
- Generated sqlc code: `backend/internal/storage/sqlite/gen/`.

## Distribution

- The **desktop app** (GitHub Releases) is the canonical, auto-updating install path. Point users there first.
- **npm still works but is no longer recommended.** `0.10.0` is the final version published to npm; the `@aoagents/ao` package is frozen and will not receive further updates. It remains a legacy on-ramp for users who already have `ao` on their PATH, where `ao start` fetches and opens the desktop build. Do not add features, docs, or flows that treat npm as the intended way to install AO.
- **Exactly one publisher.** Only the designated release conductor runs a real publish, on any channel. Divergent artifacts from multiple publishers made the 28-29 Jul macOS incident unreadable. Use the fork dev loop for test builds. Full rule and rationale: `frontend/docs/desktop-release.md`, "Hard rule: exactly one publisher".
- **Verify macOS artifacts with `frontend/scripts/verify-mac-artifact.sh`, never by hand.** It extracts with `ditto -x -k` and runs `codesign --verify --deep --strict`, `spctl -a -vv -t exec`, `xcrun stapler validate`. Plain `unzip` breaks the seal and yields a convincing false failure; `spctl` without `-vv` prints nothing at all on success.
- **macOS ships both a `.zip` and a `.dmg`.** The dmg is first install only. The zip and `latest-mac.yml` must keep publishing forever: electron-updater cannot install an update from a dmg. macOS differential updates are permanently disabled (full download only); see issues #3151 and #3267.

## Coding conventions

- Keep every change surgical and directly tied to the task. Avoid drive-by cleanup, broad renames, formatting churn, speculative abstractions, and architectural refactors unless the task explicitly asks for them.
- Follow existing Go package boundaries. CLI code should call daemon HTTP routes through shared CLI client helpers; it should not open SQLite, spawn runtimes, or call adapters directly.
- Keep Cobra commands in the relevant command file and table-test them in the style of `backend/internal/cli/*_test.go`.
- Mirror existing response/request DTOs in the CLI instead of importing HTTP controller packages into CLI code, unless the package already establishes that dependency.
- Return usage errors as `usageError` so CLI misuse exits 2; runtime/daemon failures should exit 1.
- Preserve API error envelopes and request IDs when surfacing daemon errors.
- Use `context.Context` as the first argument for functions that do I/O or blocking work.
- Do not add abstractions for one-off use cases. Add helpers only when they remove duplication across real call sites.
- Tests should cover the user-visible behavior and boundary being changed: happy path, validation/missing args, daemon error envelopes, and any destructive confirmation path.

## Hard rules and boundaries

- The daemon's **primary (loopback) listener** stays bound to `127.0.0.1` and unauthenticated. Do not change its bind host or add auth to it.
- The daemon MAY run a **second, opt-in LAN listener** (the "Connect Mobile" feature) that binds `0.0.0.0` **only while explicitly enabled**, behind the bearer-password `authMiddleware`, serving the app API **and the embedded web UI** but never the loopback-gated control routes (`/shutdown`, telemetry, mobile control). **Exactly one registered route is exempt from `authMiddleware`: `GET /api/v1/identity`**, which returns only an opaque host id and the mobile contract version so a phone can confirm which machine answered before presenting a credential — see `docs/adr/0003-unauthenticated-identity-probe.md`. The exemption is an exact path, `GET` only, and checked ahead of the lockout; any further unauthenticated route needs its own ADR. The embedded web UI bundle is the other unauthenticated surface because it renders the password prompt; its fail-closed boundary is described below. The listener is plaintext and home-network-only by deliberate decision — see `docs/adr/0001-lan-listener-for-mobile.md` and `CONTEXT.md`. Do not add any other network-facing bind. Its bind host may be _narrowed_ by the `bind` mode in `~/.ao/mobile/config.json` (`all` — the default — / `tailscale` / an IP); never widened past `0.0.0.0`.
- The loopback listener answers only to Hosts naming its own address: loopback literals, `*.localhost`, or an allowlisted origin's host (`hostGuard` in `backend/internal/httpd/cors.go`). This is what stops DNS rebinding, and no origin check substitutes for it — after a rebind the request is GENUINELY same-origin and a same-origin GET carries no `Origin` header at all. The LAN listener is exempt: its authority-bearing surfaces are credential-gated, while the identity probe and static UI shell expose no authority. Enforcing bound-address equality there would also break the legitimate ways a phone addresses the bridge — a Tailscale MagicDNS name, or any hostname that resolves to it.
- **The LAN listener is exempt from `hostGuard` BECAUSE every authority-bearing route or surface on it is credential-gated. Adding another unauthenticated data-returning or state-changing route or surface breaks the exemption.** A rebinding page reaching the LAN socket carries no Bearer and no `ao_conn` cookie (the browser scopes that cookie to the host the daemon actually served), so it can reach only the opaque identity probe and the static password-prompt bundle. `TestEveryLANRouteIsCredentialGated` fails the build if another registered route opens; `unauthenticatedLANRoutes` contains only the exact identity probe, and anything else reading or changing state may not be added. **"Surface", not just "route", is deliberate:** the embedded web UI bundle (`backend/internal/httpd/webui`, routed by `webUIBypass` in `lan_listener.go`) is served by the router's `NotFound` fallback, so `chi.Walk` cannot yield it and `TestEveryLANRouteIsCredentialGated` cannot see it — the two tests meet at that seam. `TestWebUIBypassFailsClosedForRegisteredRoutes`, `TestWebUIBypassDisabledForUnknownHandler` and `TestWebUIBypassDoesNotExposeWorkspacePreviews` cover the other side: the bypass serves the static bundle directly and treats a request as UI only when the router has **no** handler for it, so a handler that cannot report its routes disables the bypass entirely rather than skipping the check, and no router middleware ever runs on the unauthenticated path. **Know what that guarantee does and does not cover.** It covers routes the chi router _matches_, so a newly registered route is credential-gated the day it is written; for those `webui.daemonPrefixes` is defense-in-depth. It does not cover endpoints served by **middleware** rather than a registered route — `Mux.Match` cannot see them (today: `POST /api/v1/auth/login`, served inside `authMiddleware`). Such an endpoint is never _exposed_ by the bypass, since the bypass no longer routes into the router at all; but on a UI-classified path it would be _shadowed_ — answered with the app shell instead of running. So for middleware-served endpoints `daemonPrefixes` is load-bearing for reachability, and adding one outside those prefixes on a GET/HEAD path means listing it there.
- The loopback listener is unauthenticated, so **reaching it is the authorization**. Anything with a side effect there — `/mux` and every non-GET method — must pass a strict origin check (explicit allowlist, exact same-origin, or `Sec-Fetch-Site`), never blanket loopback-origin trust: any local dev server's page can present a loopback origin. See `requiresStrictOrigin` in `backend/internal/httpd/cors.go`. Where the `ao_conn` **cookie** authenticates, that check is mandatory regardless of listener, because cookies ride cross-site; an `Authorization: Bearer` / `ao.bearer.*` credential does not, and stays exempt.
- **`AO_ALLOWED_ORIGINS` is load-bearing only for a _separately hosted_ UI** — a Vite dev server, or a build served from another host. A UI the daemon serves itself is same-origin with the API and is carried by host-equality, so it needs no entry and no configuration; tests pin that with an empty allowlist (`TestDaemonServedUINeedsNoAllowlistEntry` and its LAN twin). Do not add an entry to "make the web UI work" — if a daemon-served page needs one, something is broken, not misconfigured.
- **`POST /api/v1/auth/login` leaves no trace in the access log.** It is answered by `authMiddleware`, which wraps the router and therefore sits outside `requestLogger`. A login that fails looks exactly like a request that never arrived. When debugging one, instrument the handler or read the client — do not conclude from daemon-log silence that nothing reached the daemon.
- The **remote-host proxy** in Electron main binds `127.0.0.1` only, requires a per-activation 128-bit token carried in the URL path, and strips that token before forwarding upstream with the host's `Authorization: Bearer` credential — so no remote daemon or its logs ever see it. Never log `req.url` on that path, never hand a connection password to the renderer, and never bind the proxy to anything but loopback — see `docs/adr/0005-remote-hosts-renderer-fanout.md`.
- The CLI is a thin client. Do not port old in-process TypeScript CLI behavior that bypasses daemon HTTP routes.
- Do not store derived/display session status. Status is derived from durable facts (`activity_state`, `is_terminated`, PR/check/comment facts) at service read time.
- Do not treat failed/unknown runtime probes as proof a session is dead.
- Do not force-delete dirty registered worktrees.
- Do not modify already-merged SQLite migrations. Add a new migration instead.
- Do not hand-edit `backend/internal/storage/sqlite/gen/*`; change `backend/internal/storage/sqlite/queries/*` or migrations and run `npm run sqlc`.
- SQLite change events come from DB triggers into `change_log`; do not add parallel manual CDC emission from store methods unless the architecture changes explicitly.
- Keep generated OpenAPI/API DTO drift in mind: controller response shapes live in `backend/internal/httpd/controllers/dto.go` and tests may assert CLI/HTTP wire compatibility.
- Do not add network calls to tests unless the package already has an integration/e2e pattern for them. Prefer `httptest`, fakes, and injected dependencies.
- Do not commit local run state, daemon data, temporary worktrees, build outputs, or credentials.
- All app state lives under `~/.ao` only. The daemon's data dir, `running.json`, worktrees, and the Electron supervisor's `userData` (Chromium cache, cookies, local/session storage, crash dumps) must resolve under `~/.ao` (overridable via `AO_DATA_DIR`/`AO_RUN_FILE`). Never write to or read from `~/Library/Application Support` or any other OS default app-data location. `main.ts` pins Electron's `userData` to `~/.ao/electron`; do not remove that override or rely on Electron's default path.

## API contract changes

The daemon API is code-first. The OpenAPI spec and frontend TypeScript types are generated artifacts — edit the source, then regenerate.

**Source files to edit:**

- `backend/internal/httpd/controllers/dto.go` — request/response shapes.
- `backend/internal/httpd/apispec/specgen/build.go` — operation registry; add a `schemaNames` entry for any new named type.

**Regenerate after editing:**

```bash
npm run api          # runs api:spec then api:ts in sequence
```

This is equivalent to running:

```bash
npm run api:spec     # cd backend && go generate ./internal/httpd/apispec/...
npm run api:ts       # npx openapi-typescript@7.4.4 backend/internal/httpd/apispec/openapi.yaml -o frontend/src/api/schema.ts
```

**Verify:**

```bash
cd backend && go test ./internal/httpd/...    # spec drift + route/spec parity tests (does not cover schema.ts — that is checked by the api-drift CI job)
```

Commit `openapi.yaml` and `frontend/src/api/schema.ts` together with the Go changes. CI will regenerate both files and fail if the committed versions are out of date. The CLI hand-mirrored DTOs remain a deliberate manual boundary and are not generated.

## PR hygiene

- Branch from `main` unless explicitly continuing an existing PR.
- Keep one issue per PR. If asked for separate work, create a separate branch and PR.
- Use conventional commit messages (`feat:`, `fix:`, `docs:`, `test:`, `chore:`).
- Explain intentional omissions in the PR body, especially when the TypeScript original had more behavior than the Go rewrite domain currently supports.
- Run the narrowest relevant tests first, then the repo/CI commands that match the touched area.
