# Upstream Remote Hosts — Plan 3: Ref Threading, Fan-out, One Tree (A8a–A11)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build, on top of the Plan 1 and Plan 2 stacks, the last seven upstream-ready branches of the remote-hosts stack — thread `Ref` through every read and every route, fan workspace queries and event streams out per host, give each host its own terminal mux, route every write by `Ref`, draw one tree across every connected host, and document the whole thing — each branch green and dark behind the flag, and extend the hand-off so the human can open all fifteen one at a time.

**Architecture:** Waves 1 and 2 built the plumbing and made it reachable for *one* thing (adding a project). Wave 3 is where the rest of the app learns that a session or a project belongs to a machine. The whole wave is one mechanical idea applied consistently — `apiClient` becomes `clientFor(ref.host)`, a bare `id: string` becomes a `Ref`, and a query key `["x", id]` becomes `["x", refKey(ref)]` — plus three genuinely new pieces (per-host event streams, per-host terminal muxes, the host-sectioned tree).

The bases fall out of what the code actually imports, and the DAG is flatter than spec §2.3 draws it:

- **A8a sits on `up-a5-clients`, not on `up-a2-hosts`.** §2.3 draws `A2 → A8a`, on the assumption that threading `Ref` through reads needs only the identity primitives. It does not: every converted read site on `origin/develop` calls `clientFor(session.host)`, and `clientFor` is A5. Verified on twelve sampled read hooks — there is no intermediate state where a read takes a `Ref` but still goes through `apiClient`, and inventing one would mean writing code that exists nowhere and deleting it again in A8b. A5 already contains A2, so the base is `up-a5-clients` and nothing is lost.
- **A8b and A9 both sit on A8a**, which is the only thing they share. §2.3 draws both edges already; the flattening above means neither needs a separate integration merge.
- **A8c sits on A8b** — the mux is keyed by host and derives its URL from the host's base, which A8b's per-host event work establishes.
- **A9 is three branches, not one.** §2.4 pre-authorises this ("split by area into 2–3 PRs if > 60 files each") and the write surface measured here is 18 files across three clean seams: sessions/terminals, projects/orchestrator, PRs/reviews-and-panels. Each converts a set of symbols *and every caller of them*, which is the only split that compiles.
- **A10 sits on a tagged integration merge of A6 + A8c + A9c** (`up-a10-base`). It needs A6 because it restores the telemetry Plan 2 deliberately removed from `useRemoteHosts.ts` and `AddRemoteHostDialog.tsx`, A8c because the tree renders per-host terminals, and A9c because the tree's rows dispatch writes. A8c transitively carries A8b and A8a; A9c transitively carries A9a and A9b.
- **A11 sits on `upstream/main`.** It is documentation and one `AGENTS.md` paragraph — it imports nothing, so it is a third independent root that can be opened whenever, though it *describes* behaviour that only exists once A10 merges, so the runbook says open it last.

**Tech Stack:** React 19 renderer, TanStack Router (file-based routes, generated `routeTree.gen.ts`), TanStack Query v5, `openapi-fetch`, `EventSource`, xterm, Vitest 4 (jsdom), TypeScript `tsc --noEmit`.

**Spec:** `docs/upstreaming-remote-sessions.md` (merged, #123) §2.4 A8a–A11, plus the *Deferred out of wave 2* list in `docs/upstreaming-stack-status.md`, which this plan is required to consume in full. Plan 1 built A0–A5; Plan 2 built A6–A7b.

## Global Constraints

- **Do not push to, open PRs against, or comment on `Untrivial-ai/agent-orchestrator`.** All pushes go to `origin`. `git fetch upstream` is the only upstream operation. Everything the human opens is a **draft** PR (standing directive, 2026-08-24).
- **Clean refs are the public names:** `up-a8a-refs`, `up-a8b-fanout`, `up-a8c-terminals`, `up-a9a-writes-sessions`, `up-a9b-writes-projects`, `up-a9c-writes-reviews`, `up-a10-one-tree`, `up-a11-docs`. No session namespace.
- **Flag off ⇒ identity.** This is the whole review argument for a wave this size. With `remoteHosts` false, `connectedHosts()` is `[]`, every `Ref` carries `LOCAL_HOST`, every fan-out is a loop of one, every `clientFor(LOCAL_HOST)` returns the same client `apiClient` was, and every host-qualified route resolves to the local host. A reviewer should be able to read any hunk in A8a or A9 and see an identity transformation.
- **`tsc --noEmit` is the completeness oracle, and it is load-bearing.** These are mechanical refactors: making `host` a required field on `WorkspaceSession`/`WorkspaceSummary`, or changing `renameSession(sessionId: string)` to `renameSession(ref: Ref)`, makes the compiler enumerate every site that must change. Never satisfy it with a cast, `any`, or a non-null assertion — those are how a "mechanical" refactor silently stops being one. If a site cannot be converted without judgement, stop and report it rather than papering over it.
- **Every branch is green on its own base:** its own suites plus `tsc --noEmit` and `tsc --noEmit -p tsconfig.e2e.json`. No branch in this wave touches Go or the OpenAPI surface, so `go` and `api-drift` are unaffected throughout.
- **Upstream conventions** (`AGENTS.md`): surgical changes, no drive-by cleanup, conventional commits, tabs in `.ts/.tsx`, every i18n key in all eight locales, ≤15 non-test files per PR **except** A8a and the three A9 branches, which §2.4 pre-authorises as mechanical and flag-dark.
- **Scrub before every commit** (§3.3): `grep -rnE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:"` over staged files must print nothing.
- **Test commands** (from `$STACK/frontend`): `node_modules/.bin/vitest run --config vite.renderer.config.ts <files>`; `node_modules/.bin/tsc --noEmit`; `node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json`. Node is off-PATH: `export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"`.
- **A whole-suite `vitest run` is not a clean signal in this worktree.** Measured on untouched `upstream/main`: `11 failed | 216 passed` files, every failure an `ERR_MODULE_NOT_FOUND` under `src/landing/**` or `src/annotate-preload.test.ts` (the landing app is a separate, uninstalled package — there are no npm workspaces). "Green" means the named suites plus both typechecks. If a whole-suite run is used as a cross-check, A/B it against the base and require identical failure sets.
- **Fork source ref:** everything ports from `origin/develop @ 32d089f37`.

### Where spec §2.4 fails against reality — read this before Task 2

Reported rather than improvised, per the standing rule. One is a genuine gap that changes what A8a contains:

1. **The legacy-route redirects §2.4 promises do not exist in the fork, anywhere.** §2.4 A8a says routes become `/host/$hostId/…` "with the old paths redirecting", and leans on that for its stands-alone argument ("routes redirect so deep links survive"). On `origin/develop` the old route files are simply **deleted**: `_shell.sessions.$sessionId.tsx`, `_shell.projects.$projectId.tsx`, `_shell.projects.$projectId_.settings.tsx` and `_shell.projects.$projectId_.sessions.$sessionId.tsx` are gone, replaced by three `_shell.host.$hostId.*` files, and no redirect logic exists in `__root.tsx`, `_shell.tsx` or anywhere else (`git grep -n redirect` over the routes tree returns nothing). The fork could afford that — it is one user's app. Upstream cannot: every existing deep link, bookmark, and any link the desktop app has ever emitted points at the old shape.

   **Decision: A8a writes the four redirect routes as new code, and says so in its PR body.** They are ~8 lines each — a `beforeLoad` that `throw redirect(...)` to the host-qualified path with `LOCAL_HOST` — plus a test per path. This is the only new code in an otherwise mechanical branch, and it is flagged in the plan, the commit message and the PR body so no reviewer mistakes it for a port. It is not a blocker and does not stop the task; it is a documented gap between the spec and the source.

2. **`A2 → A8a` is the wrong edge** (see Architecture): reads use `clientFor`, so the base is A5. No behaviour changes; only the branch base does.

3. **§2.4's A10 file list omits `types/workspace.ts`.** `HostSection`, `flattenHostSections` and `updateHostWorkspaces` live there on `develop`, so A10 touches the same file A8a did, in a different hunk. Noted so it is not mistaken for a stray edit.

4. **`routeTree.gen.ts` is tracked and generated.** It is not in `.gitignore`; the TanStack Vite plugin writes it from `routesDirectory`. A8a must regenerate and commit it. There is no `router-cli` binary in `node_modules/.bin`, but `@tanstack/router-generator` is installed and drivable from a short node script — Task 2 Step 3 gives the incantation and a `vite build` fallback.

---

## File structure

Worktree `$STACK` = `/Users/amongstar/dev/agent-orchestrator-up-stack` (built by Plan 1, extended by Plan 2; reused, not recreated). `$W` = this AO worktree. Paths are relative to `$STACK`.

| Branch | Base | Responsibility | Non-test files |
| --- | --- | --- | --- |
| `up-a8a-refs` | `up-a5-clients` | `host` on the workspace types; host-qualified routes + four legacy redirects; every **read** takes a `Ref` and goes through `clientFor`; query keys become `refKey`. | ~32 |
| `up-a8b-fanout` | `up-a8a-refs` | `useWorkspaceQuery` fans out over `connectedHosts()`; one `EventSource` per host (`lib/host-events.ts`); `event-transport` and `workspace-file-events` become host-aware. | ~5 |
| `up-a8c-terminals` | `up-a8b-fanout` | One terminal mux per host, keyed by host, URL derived from that host's base (which carries the proxy token prefix). | ~4 |
| `up-a9a-writes-sessions` | `up-a8a-refs` | Session and terminal writes by `Ref`: rename, terminate, pin, restore, switch-agent, interface transition, conversation, browser link. | ~8 |
| `up-a9b-writes-projects` | `up-a9a-writes-sessions` | Project and orchestrator writes by `Ref`: spawn, restart, project settings, `_shell` project actions, task composer. | ~5 |
| `up-a9c-writes-reviews` | `up-a9b-writes-projects` | PR, review, diff and panel writes by `Ref`: inspector, diff selection, files view, command palette, browser panel. | ~5 |
| `up-a10-one-tree` | merge(A6, A8c, A9c), tag `up-a10-base` | One tree across hosts: host sections with retry, host switcher, sidebar/topbar, telemetry (restored into A6's files), hostile-daemon coverage, usage on the session's own host. | ~10 |
| `up-a11-docs` | `upstream/main` | ADR 0003, landing docs, troubleshooting, the two-sentence `AGENTS.md` proxy rule. | ~6 |

**The transformation rule** (A8a, A9a–c — quote it in each PR body):

```ts
// before                                    // after
apiClient.GET(path, {                        clientFor(ref.host).GET(path, {
  params: { path: { sessionId } } })           params: { path: { sessionId: ref.id } } })
function f(sessionId: string)                function f(session: Ref)
["session-usage", sessionId]                 ["session-usage", refKey(session)]
workspaces.find(w => w.id === id)            workspaces.find(w => w.host === r.host && w.id === r.id)
```

`Ref = {host, id}` and `refKey` come from `lib/hosts.ts` (A2); `clientFor` from `lib/host-clients.ts` (A5). Ids are **never** rewritten — a project id is `filepath.Base(path)` on every machine, so bare ids collide by construction and `Ref` qualifies them at the addressing boundary only.

---

### Task 1: Re-baseline the stack worktree

**Files:** none (verification only).

- [ ] **Step 1: Fetch and confirm all eight existing branches**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
export W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-100
export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
cd "$W" && git fetch upstream --quiet && git fetch origin --quiet
echo "upstream/main $(git rev-parse --short upstream/main)"
for b in up-a1-flag up-a2-hosts up-a3-store up-a4-proxy up-a5-clients up-a6-host-ui up-a7a-fs-dirs up-a7b-folder-picker; do
  echo "$b $(git rev-parse --short origin/$b)"
done
git -C "$STACK" status --porcelain | head
```

Expected, verified 2026-08-24: `upstream/main 6cba6344c`; `up-a5-clients 39fa64f23`, `up-a6-host-ui d00d3cd2b`, `up-a7a-fs-dirs 98604953b`, `up-a7b-folder-picker 26d0c5db2`; worktree clean. **If `upstream/main` has moved, stop and report** — every branch in the stack is cut from `6cba6344c`.

- [ ] **Step 2: Baseline A8a's base**

```bash
cd "$STACK" && git checkout -q --detach origin/up-a5-clients && cd frontend
node_modules/.bin/tsc --noEmit && echo TSC_OK
node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json && echo E2E_TSC_OK
node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/lib/host-clients.test.ts src/renderer/hooks/useWorkspaceQuery.test.tsx 2>&1 | grep -E "×|Test Files|Tests "
```

Expected: `TSC_OK`, `E2E_TSC_OK`, both suites pass. `useWorkspaceQuery.test.tsx` is upstream's own and is the file A8b rewrites — capture its passing count now as the before-picture.

---

### Task 2: A8a — `refactor(hosts): thread Ref through reads and host-qualified routes`

Branch `up-a8a-refs` from `origin/up-a5-clients`. The mechanical half of fork #74/#78/#84, plus the four legacy redirects (new code — see *Where spec §2.4 fails against reality*, item 1).

**Files:**
- Modify: `frontend/src/renderer/types/workspace.ts` (`host: HostId` on `WorkspaceSession` and `WorkspaceSummary`; `findProjectOrchestrator(workspaces, project: Ref)`)
- Create: `frontend/src/renderer/routes/_shell.host.$hostId.session.$sessionId.tsx`, `_shell.host.$hostId.project.$projectId.tsx`, `_shell.host.$hostId.project.$projectId_.settings.tsx`
- Create (**new code**): `frontend/src/renderer/routes/_shell.sessions.$sessionId.tsx`, `_shell.projects.$projectId.tsx`, `_shell.projects.$projectId_.settings.tsx`, `_shell.projects.$projectId_.sessions.$sessionId.tsx` — each a redirect-only route
- Delete: the four upstream route files those replace, **only after** the redirects above take their paths
- Modify: `frontend/src/renderer/routeTree.gen.ts` (generated, tracked)
- Modify (read threading): `hooks/useSessionScmSummary.ts`, `useSessionUsage.ts`, `useSessionUsageSummaries.ts`, `useSessionWorkspaceFiles.ts`, `useAgentSwitches.ts`, `useAgentModelsQuery.ts`, `useAgentsQuery.ts`, `usePersistentBrowserProfile.ts` (new on develop), `lib/session-reviews.ts`, `lib/command-palette.ts`, `lib/daemon-status.ts`, `lib/navigate-to-session.ts`
- Modify (prop threading only): `components/CenterPane.tsx`, `SessionsBoardAdapters.tsx`, `SessionView.tsx`, `ImageDiffView.tsx`, `GlobalNewTaskDialog.tsx`, `NewTaskDialog.tsx`, `NotificationCenter.tsx`, `OrchestratorReplacementDialog.tsx`, `AgentModelPicker.tsx`, `chat/SessionChatSurface.tsx`, `ShellTerminalsView.tsx`
- Create: `frontend/src/renderer/routes/-session-route.test.ts` (host-qualified params) + a redirect test per legacy path

**Interfaces:**
- Consumes: `Ref`, `HostId`, `LOCAL_HOST`, `refKey` (A2); `clientFor` (A5).
- Produces: `WorkspaceSession.host` / `WorkspaceSummary.host`; the `/host/$hostId/session/$sessionId`, `/host/$hostId/project/$projectId`, `/host/$hostId/project/$projectId/settings` route shape; `Ref`-taking read hooks.

- [ ] **Step 1: Branch, and seed the change that makes the compiler enumerate the work**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
export W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-100
export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
cd "$STACK" && git checkout -q -b up-a8a-refs origin/up-a5-clients
```

Add to `types/workspace.ts`: `import type { HostId, Ref } from "../lib/hosts";`, `host: HostId;` as the first field of both `WorkspaceSession` and `WorkspaceSummary`, and change `findProjectOrchestrator(workspaces, projectId: string)` to take `project: Ref` and match on both halves (the diff is in `git -C "$W" diff upstream/main origin/develop -- frontend/src/renderer/types/workspace.ts`; take **only** those three hunks — `HostSection` and its helpers are A10).

```bash
cd "$STACK/frontend" && node_modules/.bin/tsc --noEmit 2>&1 | grep -c "error TS"
node_modules/.bin/tsc --noEmit 2>&1 | sed 's/(.*//' | sort -u | head -50
```

Expected: a large non-zero error count, and a de-duplicated file list that **is the work list for this branch**. Compare it against the Files list above; a file appearing here that is not listed is a discovery — add it and note it, do not cast it away.

- [ ] **Step 2: Convert every read site, file by file, taking the fork's version of each hunk**

For each file in the compiler's list, take the fork's converted form:

```bash
git -C "$W" diff -w upstream/main origin/develop -- frontend/src/renderer/<file>
```

and apply the read-path hunks by hand (the fork's copies of several of these files carry unrelated churn and, in the components, space-reformatting — the same trap Plan 2 hit on `CreateProjectFlow.tsx`; check `git diff -w` versus plain `git diff` before assuming a file can be copied wholesale). Apply the transformation rule from the File structure section. **Leave every mutation call alone** — `POST`/`PATCH`/`DELETE` still go through `apiClient` at the end of this branch and are converted in A9a–c. That is the seam that keeps A8a reviewable.

Re-run the oracle until it is clean:

```bash
cd "$STACK/frontend" && node_modules/.bin/tsc --noEmit 2>&1 | grep -c "error TS"
```

Expected: `0`. Then confirm no cast crept in:

```bash
cd "$STACK" && git diff -- frontend/src/renderer | grep -nE '^\+.*(as any|as unknown as|@ts-(ignore|expect-error)|!\.)' ; echo "cast scan exit=$? (1 means clean)"
```

- [ ] **Step 3: Host-qualified routes, legacy redirects, and the regenerated route tree**

Take the three host-qualified route files from the fork verbatim:

```bash
cd "$STACK"
for r in _shell.host.\$hostId.session.\$sessionId _shell.host.\$hostId.project.\$projectId _shell.host.\$hostId.project.\$projectId_.settings; do
  git -C "$W" show "origin/develop:frontend/src/renderer/routes/$r.tsx" > "frontend/src/renderer/routes/$r.tsx"
done
```

Then write the four redirects — **new code, no fork source**. Each is a route at the upstream path whose `beforeLoad` throws a redirect to the host-qualified equivalent on `LOCAL_HOST`, preserving the id. Shape (for `_shell.sessions.$sessionId.tsx`):

```tsx
import { createFileRoute, redirect } from "@tanstack/react-router";
import { LOCAL_HOST } from "../lib/hosts";

// Sessions and projects became host-qualified. Every link, bookmark and
// deep link minted before that points here, so this path keeps working and
// resolves to the local host — the only host those URLs could have meant.
export const Route = createFileRoute("/_shell/sessions/$sessionId")({
	beforeLoad: ({ params }) => {
		throw redirect({
			to: "/_shell/host/$hostId/session/$sessionId",
			params: { hostId: LOCAL_HOST, sessionId: params.sessionId },
			replace: true,
		});
	},
});
```

`_shell.projects.$projectId_.sessions.$sessionId.tsx` redirects to the *session* route (upstream's nested project→session path has no host-qualified twin), and the two project paths redirect to `/host/$hostId/project/$projectId[/settings]`. Delete upstream's four original route files once the redirects occupy their paths.

Regenerate the tracked route tree — there is no `router-cli` binary, so drive `@tanstack/router-generator` directly:

```bash
cd "$STACK/frontend"
node -e "const {generator}=require('@tanstack/router-generator');" 2>/dev/null && echo "generator loadable" || echo "use the vite fallback"
```

If the generator's API is not directly callable in this version, the fallback is a renderer build, which runs the same Vite plugin: `node_modules/.bin/vite build --config vite.renderer.config.ts`. Either way, confirm the result is the generator's own output and not hand-edited:

```bash
cd "$STACK" && git diff --stat frontend/src/renderer/routeTree.gen.ts
head -5 frontend/src/renderer/routeTree.gen.ts
```

Expected: the file changed, and its header still says it is auto-generated. If a live Vite dev server is running anywhere it will keep rewriting this file — do not chase that churn, stop the server.

- [ ] **Step 4: The route tests, including one per redirect**

Take the fork's `-session-route.test.ts` verbatim (it pins that a host id containing `:` and `/` and a session id containing `:` both survive the URL round-trip — the case that matters, since a host id *is* a URL):

```bash
git -C "$W" show origin/develop:frontend/src/renderer/routes/-session-route.test.ts > "$STACK/frontend/src/renderer/routes/-session-route.test.ts"
```

Then add a `describe("legacy routes redirect", …)` block to it with one case per old path, asserting the router lands on the host-qualified match with `hostId === LOCAL_HOST` and the id preserved — these guard code that exists nowhere else and would otherwise ship untested:

```ts
	it.each([
		["/sessions/ao-1", { hostId: LOCAL_HOST, sessionId: "ao-1" }],
		["/projects/agent-orchestrator", { hostId: LOCAL_HOST, projectId: "agent-orchestrator" }],
		["/projects/agent-orchestrator/settings", { hostId: LOCAL_HOST, projectId: "agent-orchestrator" }],
		["/projects/agent-orchestrator/sessions/ao-1", { hostId: LOCAL_HOST, sessionId: "ao-1" }],
	])("%s lands on the local host's route", async (from, params) => {
		const router = createRouter({
			history: createMemoryHistory({ initialEntries: [from] }),
			routeTree,
			context: { queryClient: new QueryClient() },
		});
		await router.load();
		expect(router.state.matches.at(-1)?.params).toMatchObject(params);
		expect(router.state.location.pathname).toContain("/host/");
	});
```

- [ ] **Step 5: Verify green**

```bash
cd "$STACK/frontend"
node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/routes src/renderer/hooks src/renderer/lib src/renderer/components 2>&1 | grep -E "×|Test Files|Tests " | tail -15
node_modules/.bin/tsc --noEmit && echo TSC_OK
node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json && echo E2E_TSC_OK
```

Expected: every suite in those four directories green, `TSC_OK`, `E2E_TSC_OK`. Upstream's existing suites are the regression check here: A8a is an identity transformation with one host, so a test that changes behaviour is a bug in the conversion, not a test to update. The exceptions are suites that assert a **query key** or a **URL** — those legitimately change, and each such edit belongs in the commit with a one-line reason.

- [ ] **Step 6: Scrub and commit**

```bash
cd "$STACK"
git add -A frontend/src/renderer packages/product-ui 2>/dev/null; git status --porcelain | head -40
git diff --cached --name-only | xargs grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 means clean)"
git commit -q -m "refactor(hosts): thread Ref through reads and host-qualified routes

A session and a project now carry the host they live on. Every read takes
a Ref = {host, id} and dispatches through clientFor(ref.host), and every
query key carries refKey(ref) instead of a bare id. With one host this is
an identity transformation: clientFor(LOCAL_HOST) is the client apiClient
already was, and every Ref carries LOCAL_HOST.

Ids are never rewritten. A project id is filepath.Base(path) on every
machine, so bare ids collide by construction between two hosts; Ref
qualifies them at the addressing boundary and nowhere else.

Routes become /host/\$hostId/session/\$sessionId and
/host/\$hostId/project/\$projectId, since the URL is the one piece of
addressing a user keeps. The four upstream paths they replace are kept as
redirects to the local host so existing deep links and bookmarks survive
- that redirect layer is the only new behaviour in this PR; everything
else is mechanical.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
git push -q -u origin up-a8a-refs
git rev-parse --short HEAD
```

---

### Task 3: A8b — `feat(hosts): fan out workspace queries and event streams per host`

Branch `up-a8b-fanout` from `up-a8a-refs`. Fork #78, #81, #79 minus their mechanical parts.

**Files:**
- Modify: `frontend/src/renderer/hooks/useWorkspaceQuery.ts` (rewrite: 148 upstream lines → ~263), `useWorkspaceQuery.test.tsx`
- Create: `frontend/src/renderer/lib/host-events.ts`, `host-events.test.ts`
- Modify: `frontend/src/renderer/lib/event-transport.ts`, `lib/workspace-file-events.ts`, `routes/_shell.tsx` (read/stream hunks only — its write hunks are A9b)
- Modify: `frontend/src/renderer/test/fake-daemon.ts` (+ its test): restore the `slow` and `route-missing` behaviours and the `/api/v1/fs/dirs` healthy case that Plan 2 trimmed — `useWorkspaceQuery.test.tsx` is their consumer (this discharges one *Deferred out of wave 2* item)

- [ ] **Step 1: Branch and bring the tests, watch them fail**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
export W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-100
export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
cd "$STACK" && git checkout -q -b up-a8b-fanout up-a8a-refs
git -C "$W" show origin/develop:frontend/src/renderer/lib/host-events.test.ts > frontend/src/renderer/lib/host-events.test.ts
git -C "$W" show origin/develop:frontend/src/renderer/hooks/useWorkspaceQuery.test.tsx > frontend/src/renderer/hooks/useWorkspaceQuery.test.tsx
cd frontend && node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/lib/host-events.test.ts src/renderer/hooks/useWorkspaceQuery.test.tsx 2>&1 | grep -E "Error|×|Tests " | head -8
```

Expected: `host-events.test.ts` fails to resolve `./host-events`; `useWorkspaceQuery.test.tsx` fails on the fan-out cases and on the missing `fake-daemon` behaviours.

What these pin, and why each exists: **one `EventSource` per host**, not one shared; an event from host B invalidates only `["workspaces", B]`, so a busy remote cannot make the local board refetch; `Promise.allSettled` over hosts, so one sleeping host cannot serialise or fail the rest (this is fork #78's silent-failure regression, which returned once as #86); and queries registered **per host** (#81 — a host connected after first paint had no query and stayed invisible). The `slow` fake-daemon behaviour is what makes the allSettled case falsifiable.

- [ ] **Step 2: Restore the trimmed fake-daemon behaviours**

Re-add to `frontend/src/renderer/test/fake-daemon.ts`: `"slow"` (returns a promise that only rejects on the caller's abort signal) and `"route-missing"` (404s `/api/v1/fs/dirs`, serves everything else) to the `Behaviour` union and the switch, and the `/api/v1/fs/dirs` case to `healthyResponse`. Take them verbatim from `git -C "$W" show origin/develop:frontend/src/renderer/test/fake-daemon.ts`, and add the matching cases back to `fake-daemon.test.ts`.

- [ ] **Step 3: Write `host-events.ts` and rewrite `useWorkspaceQuery.ts`**

```bash
cd "$STACK"
git -C "$W" show origin/develop:frontend/src/renderer/lib/host-events.ts > frontend/src/renderer/lib/host-events.ts
git -C "$W" diff -w upstream/main origin/develop -- frontend/src/renderer/hooks/useWorkspaceQuery.ts frontend/src/renderer/lib/event-transport.ts frontend/src/renderer/lib/workspace-file-events.ts
```

`useWorkspaceQuery.ts` ships as a rewrite, not a patch (§1.4: reviewers need the whole file anyway). The one thing a reviewer must check in every invalidation site is that the key moved from `["workspaces"]` to `["workspaces", host]` — grep for it and make sure none was missed:

```bash
cd "$STACK/frontend" && grep -rn '"workspaces"' src/renderer | grep -v "\.test\." | grep -v "workspaces\", " ; echo "unqualified key scan exit=$? (1 means clean)"
```

`_shell.tsx` takes only its read and stream hunks here; its `POST`/`DELETE` project actions stay on `apiClient` until A9b.

- [ ] **Step 4: Verify and commit**

```bash
cd "$STACK/frontend"
node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/lib/host-events.test.ts src/renderer/hooks/useWorkspaceQuery.test.tsx src/renderer/test/fake-daemon.test.ts src/renderer/routes 2>&1 | grep -E "×|Test Files|Tests "
node_modules/.bin/tsc --noEmit && echo TSC_OK && node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json && echo E2E_TSC_OK
```

Then scrub and commit as `feat(hosts): fan out workspace queries and event streams per host`, whose body must state the flag-off identity: `connectedHosts()` is `[]`, so the fan-out is a loop of one and the board issues exactly the queries it does today.

---

### Task 4: A8c — `feat(hosts): one terminal mux per host`

Branch `up-a8c-terminals` from `up-a8b-fanout`. Fork #83.

**Files:** `frontend/src/renderer/lib/terminal-mux.ts` (+ test), `hooks/useShellTerminals.ts` (read/stream half; its three mutations are A9a), `hooks/useTerminalSession.ts`, `components/TerminalPane.tsx` (+ `TerminalPane.test.tsx`).

- [ ] **Step 1: Branch, bring the tests, watch them fail; then port**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
cd "$STACK" && git checkout -q -b up-a8c-terminals up-a8b-fanout
git -C "$W" diff -w upstream/main origin/develop -- frontend/src/renderer/lib/terminal-mux.ts frontend/src/renderer/hooks/useShellTerminals.ts frontend/src/renderer/hooks/useTerminalSession.ts frontend/src/renderer/components/TerminalPane.tsx
```

The two cases that matter: the pool is keyed by host, so two hosts get two sockets; and the mux URL is derived from the host's base, **keeping its path prefix** — a remote host's base is `http://127.0.0.1:<port>/<token>/`, and dropping the prefix would send the socket to a 404 and, worse, would be a token the proxy never sees. Verify explicitly:

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/components/TerminalPane.test.tsx src/renderer/lib 2>&1 | grep -E "×|Test Files|Tests "
node_modules/.bin/tsc --noEmit && echo TSC_OK && node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json && echo E2E_TSC_OK
```

Scrub and commit as `feat(hosts): one terminal mux per host`.

---

### Task 5: A9a/A9b/A9c — `refactor(hosts): route every write by Ref`

Three branches, chained: `up-a9a-writes-sessions` from `up-a8a-refs`, `up-a9b-writes-projects` from A9a, `up-a9c-writes-reviews` from A9b. The write half of fork #84, split by area as §2.4 pre-authorises. A9a is cut from A8a rather than from A8c so the write series does not carry the terminal and fan-out work through review; A10 merges the two lines back together.

Each branch follows the same three steps, so they are given once:

- [ ] **Step 1: Branch, then convert one area's write symbols and every caller**

| Branch | Symbols converted | Files |
| --- | --- | --- |
| A9a sessions/terminals | `renameSession`, `useTerminateSession`, `usePinSession`, `useRestoreSession`, `useSwitchAgent`, `useSessionInterfaceTransition`, `useConversation` (15 mutations — the largest single file), `useSessionBrowserLink`, `useShellTerminals`' three mutations | `lib/rename-session.ts`, `hooks/useTerminateSession.ts`, `usePinSession.ts`, `useRestoreSession.ts`, `useSwitchAgent.ts`, `useSessionInterfaceTransition.ts`, `useConversation.ts`, `useSessionBrowserLink.ts` + callers |
| A9b projects/orchestrator | `spawnOrchestrator`, `restartOrchestrator`, project settings save/delete, `_shell.tsx`'s project actions, task composer submit | `lib/spawn-orchestrator.ts`, `lib/restart-orchestrator.ts`, `components/ProjectSettingsForm.tsx`, `routes/_shell.tsx`, `components/TaskComposer.tsx` |
| A9c PRs/reviews/panels | inspector actions (14 mutations), diff selection, files view, palette commands, browser panel | `components/SessionInspector.tsx`, `DiffSelectionMenu.tsx`, `SessionFilesView.tsx`, `CommandPalette.tsx`, `BrowserPanel.tsx` |

Take each hunk from `git -C "$W" diff -w upstream/main origin/develop -- <file>`, applying the same rule as A8a. `tsc --noEmit` is again the oracle and again must reach `0` with no cast — run the cast scan from Task 2 Step 2 on every branch.

- [ ] **Step 2: The colliding-id test rides with A9a**

This is the test the whole series exists for, and it is the default case rather than an edge case: two hosts both have a project called `agent-orchestrator`, because a project id is `filepath.Base(path)` on every machine. Add to A9a a case asserting that a write against the **remote** project issues **no request to the local daemon** — a spy on the local client that must record zero calls. Without it, "routed by Ref" is an unverified claim, and the failure mode it guards is acting on the wrong machine's session.

- [ ] **Step 3: Verify and commit each branch**

```bash
cd "$STACK/frontend"
node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer 2>&1 | grep -E "Test Files|Tests "   # compare to the base's numbers
node_modules/.bin/tsc --noEmit && echo TSC_OK && node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json && echo E2E_TSC_OK
```

Commit messages: `refactor(hosts): route session and terminal writes by Ref`, `… project and orchestrator writes …`, `… pull request and review writes …`. Each body states the identity property and names the colliding-id test as the reason the series is not cosmetic.

---

### Task 6: A10 — `feat(hosts): one tree across every connected host`

Branch `up-a10-one-tree` from an integration merge of `up-a6-host-ui` + `up-a8c-terminals` + `up-a9c-writes-reviews`, tagged `up-a10-base`. Fork #85 + #86 + #70 + #104 + #87 + #95. This is the branch where the feature becomes visible, and where every remaining *Deferred out of wave 2* item is discharged.

**Files:**
- Modify: `components/Sidebar.tsx`, `SessionsBoard.tsx`, `ShellTopbar.tsx`, `SessionInspector.tsx` (usage on the session's own host — fork #95), `hooks/useWorkspaceQuery.ts` (`fetchHostSection`), `types/workspace.ts` (`HostSection`, `flattenHostSections`, `updateHostWorkspaces` — the hunks A8a deliberately left)
- Create: `components/HostSwitcher.tsx` (deferred from Plan 2 — it needs `HostSection`, which arrives in this branch), `lib/host-telemetry.ts`, `lib/host-disclosure.ts`
- Modify (telemetry restore, deferred from Plan 2): `hooks/useRemoteHosts.ts` and `components/AddRemoteHostDialog.tsx` regain their `reportHostConnect` calls; `AddRemoteHostDialog.test.tsx` regains the case that asserts the event; `lib/telemetry.ts` gains the three events' allowlist cases in `sanitizeRendererProperties`
- Modify: `frontend/src/renderer/i18n/*.json` ×8 — the fourteen deferred keys (`hosts.viewing`, `hosts.allHosts`, `hosts.backToLocal`, `hosts.passwordChanged`, `hosts.on`, `hosts.remoteSection`, `hosts.open`, `hosts.unreachable`, `hosts.peekEmpty`, `hosts.qualified`, `hosts.sectionFailed`, `hosts.retry`, `hosts.liveUpdatesOffline`, `hosts.liveUpdatesOffline.hint`)

- [ ] **Step 1: Cut and tag the integration base**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
cd "$STACK" && git checkout -q -b up-a10-one-tree up-a9c-writes-reviews
git merge --no-edit up-a8c-terminals up-a6-host-ui
git tag -f up-a10-base
git status --porcelain | head
```

A9c and A8c share A8a as an ancestor and diverge only in the files each converted, so the merge should be clean; A6 touched the host UI and i18n. **A conflict here is real information** — most likely both lines touched `useWorkspaceQuery.ts` or `_shell.tsx` — resolve by keeping both conversions, never by taking one side wholesale, and re-run `tsc` immediately.

- [ ] **Step 2: Telemetry, and its allowlist**

`sanitizeRendererProperties` in `lib/telemetry.ts` is a per-event `switch`: an event with no case emits with **every property stripped**. So `ao.renderer.host_connect`, `host_stream_state` and `host_query_failed` each need a case, and `host_id` must go through the same `hashedTelemetryID` path `project_id` uses — it is a URL, so it must never reach PostHog in clear. `host_kind` (`local`/`remote`) is the only clear-text host attribute; `host_query_failed` carries a status code and never daemon error text. Add the three event names to `docs/telemetry.md` in the same commit if that file exists upstream.

- [ ] **Step 3: The tree, and what it must never do**

The three failures this branch's tests pin, each of which the fork shipped and had to fix: a failed host renders as a **labelled section with a retry** and never blanks the tree (#85); a *local* failure is equally visible, which the first cut hid (#86); and a hostile or older daemon — HTML body, wrong-shape JSON, a port that 200s everything — never throws into the renderer (#87, using the `fake-daemon` behaviours A8b restored). Take the hostile-daemon cases from the fork's suites.

- [ ] **Step 4: Verify and commit**

```bash
cd "$STACK/frontend"
node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer 2>&1 | grep -E "×|Test Files|Tests " | tail -8
node_modules/.bin/tsc --noEmit && echo TSC_OK && node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json && echo E2E_TSC_OK
for l in de en es fr ja ko pt-BR zh-CN; do node -e "JSON.parse(require('fs').readFileSync('src/renderer/i18n/$l.json','utf8'));console.log('$l ok')"; done
```

---

### Task 7: A11 — `docs(remote-hosts): setup, trust boundary, ADR`

Branch `up-a11-docs` from `upstream/main`. Fork #101 (rewritten), #102, #111.

**Files:** `docs/adr/0003-remote-hosts-renderer-fanout.md` (new — upstream's `0001` and `0002` are taken, and their PR #3883 also proposes an `0002`, so claim `0003` and say so), `frontend/src/landing/content/docs/configuration/remote-sessions.mdx`, additions to `remote-access.mdx`, a troubleshooting section (macOS Local Network privacy, Tailscale, the `ssh -N -L` recipe with `"bind": "127.0.0.1"`), and a two-sentence `AGENTS.md` hard rule: *the proxy binds `127.0.0.1` only, requires the per-activation token, strips it before forwarding, and never logs `req.url`.*

The ADR is a rewrite, not a port: `docs/remote-sessions-edd.md` is an internal EDD with Authors/Approvers/Vendr/BI sections marked N/A, Linear ids and fork PR numbers (§3.2). Carry across the design, the trade-offs, the security-review findings and accepted risks, and the SSH spike conclusion — and nothing else. **Do not** carry the fork's `AGENTS.md` hard-rules paragraph: it describes Track B invariants that will not exist upstream (§1.4).

Scrub is stricter here than anywhere else in the stack, since docs are where fork-isms hide:

```bash
grep -rnE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|claude\.ai|Linear" docs/adr/0003-*.md frontend/src/landing/content/docs/configuration/ AGENTS.md ; echo "scrub exit=$? (1 means clean)"
```

---

### Task 8: Hand-off — extend the runbook to fifteen branches

Runs in the AO worktree on `plan/2026-08-24-hostui` — the same branch and PR that carries Plan 2, so the whole stack's hand-off stays in one place.

**Files:** `docs/upstreaming-stack-status.md`; `docs/upstreaming-pr-bodies/a8a-refs.md`, `a8b-fanout.md`, `a8c-terminals.md`, `a9a-writes-sessions.md`, `a9b-writes-projects.md`, `a9c-writes-reviews.md`, `a10-one-tree.md`, `a11-docs.md`.

- [ ] **Step 1: Extend the runbook** — the branch table grows to fifteen rows with SHAs, bases and test evidence; the order-of-operations gains wave 3; the rebase recipes gain one `--onto` line per new branch, pivoting on `up-a10-base` for A10; and the *Deferred out of wave 2* section is replaced by a note recording which branch discharged each item (A8b: the fake-daemon behaviours; A10: `HostSwitcher`, the telemetry and its test case, the fourteen i18n keys) so the list does not read as still-open.

- [ ] **Step 2: Write the eight PR bodies** in the established What/Why/How/Testing/Checklist shape. Three need a specific argument beyond the template:
  - **A8a** must lead with the identity claim and the redirect exception: *everything here is mechanical except the four legacy-route redirects, which are new and are the reason your existing deep links keep working.* It should also offer the split: if ~32 files is too many, the route change and the read threading separate cleanly.
  - **A9a–c** must each state that the series' point is the colliding-id case, not tidiness, and name the test.
  - **A10** must carry the telemetry argument: which three events, what is hashed, what is never sent, and that `sanitizeRendererProperties` gained explicit allowlist cases rather than passing properties through.

- [ ] **Step 3: Scrub, commit, push.** The plan PR (#131, draft) updates in place. Report every branch name and SHA, and the upstream SHA the stack is based on.

---

## Self-review

**Spec coverage.** §2.4 A8a → Task 2 (types, routes, read threading) plus the redirect layer §2.4 promised and the fork never built; A8b → Task 3 (#78/#81/#79, with the per-host query key as the named review focus); A8c → Task 4 (#83, with the token-prefix case); A9 → Task 5, split three ways exactly as §2.4 pre-authorises, with the colliding-id test §2.4 names; A10 → Task 6 (#85/#86/#70/#104/#87/#95) including the telemetry allowlist §3.3 requires; A11 → Task 7 (ADR 0003, landing docs, the `AGENTS.md` proxy rule, and the explicit refusal to carry the fork's Track B paragraph). §2.1's rules hold throughout: every branch traces to `upstream/main`, every branch is flag-dark, the scrub runs before every commit. The *Deferred out of wave 2* list is fully discharged and each item is traced to the branch that takes it: fake-daemon behaviours → A8b Step 2; `HostSwitcher` → A10; telemetry and its test case → A10 Step 2; the fourteen i18n keys → A10.

**Where the spec does not survive contact, and what was done about it.** Four items, listed up front with evidence: the redirects do not exist in the fork (`git grep redirect` over its routes tree is empty, and its four upstream route files are deleted outright) so A8a writes them as new, tested, and flagged code; `A2 → A8a` is the wrong base edge because reads use `clientFor`, so A8a sits on A5; §2.4's A10 file list omits `types/workspace.ts`, which is where `HostSection` lives; and `routeTree.gen.ts` is tracked and generated, so A8a must regenerate it with no `router-cli` available. None is a missing fork commit or a §1.4 entanglement hazard, so none stops the wave — but the redirect gap changes what A8a *contains*, which is why it is stated before Task 2 rather than inside it.

**Why the oracle, not a hunk list.** Plans 1 and 2 wrote every hunk verbatim because they shipped novel modules. A8a and A9a–c are identity transformations across ~50 files, where a hand-copied hunk list would be both enormous and less trustworthy than the compiler: making `host` required, or a function take a `Ref`, forces `tsc` to enumerate every site that must change. So those tasks give the rule, the seed that triggers the enumeration, the verbatim file list to check the enumeration against, and two falsifiers — error count reaching `0`, and a scan proving no `as any`, `@ts-ignore` or `!.` was used to get there. A file the compiler names that the plan does not list is a discovery to record, not a surprise to absorb quietly.

**Risks this plan cannot remove.** A8a and A9 are the two PRs §2.4 warns will conflict with any upstream renderer churn; the runbook already tells the human to request a quiet window and land them within 48h, and the rebase recipe is `--onto`, never a merge. The A10 integration merge is the one place two long-lived lines rejoin, and Task 6 Step 1 says to treat a conflict there as information rather than resolving it by taking a side. And the honest scope note: this wave is roughly four times wave 2 — eight branches over ~55 non-test files — so it is executed and reported branch by branch, and a branch that cannot be made green is reported as such rather than committed red.
