# Upstream Remote Hosts — Plan 2: Host UI + Remote Folder Browsing (A6–A7b)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build, on top of the Plan 1 stack, the next three upstream-ready branches of the remote-hosts stack (host management UI → daemon `GET /api/v1/fs/dirs` → remote folder picker), each one green and dark behind the flag, and extend the hand-off the human needs to open them upstream one at a time.

**Architecture:** Wave 2 turns Plan 1's plumbing into a user-reachable feature. It has exactly one integration pivot and one independent root, and the shape falls out of what each branch's imports actually resolve against:

- **A6 sits on `up-a5-clients`.** The host UI imports the flag (`useUiStore().remoteHosts`, A1), the preload `remotes` surface (A3), the proxy's `connect`/`disconnect` IPC (A4) and `connectHost`/`disconnectHost` (A5). A5 is already the integration merge of A1+A2+A4 (tag `up-a5-base`), so A6 is a plain linear child of it — no second octopus merge is needed and none is invented. Upstream sees A6 only after A1–A5 have merged, at which point its base is `main`.
- **A7a sits on `upstream/main`.** It is Go plus two generated files and imports nothing from the desktop stack. Spec §2.3 draws it as a root that can go any time after A0, and reality agrees: `controllers/fs.go` needs only `apierr` and `envelope`, both of which exist on `upstream/main` unchanged.
- **A7b sits on a tagged integration merge of A6 + A7a (`up-a7b-base`).** `RemoteFolderPicker.tsx` reads `components["schemas"]["ListDirsResponse"]` out of the regenerated `frontend/src/api/schema.ts`, which only A7a produces, and it mounts inside the remote-path field that only A6 produces. It cannot be cut from either parent alone. This is the same pivot A5 used, for the same reason, and the hand-off's rebase recipe pivots on the tag the same way.

Every branch is a port of code that already ships on our `develop`, re-cut against its base in the order §2.3 dictates, with the flag's off-state semantics preserved and the §3.3 scrub applied before every commit. Everything lives in the Plan 1 stack worktree and is pushed to our fork. Nothing is opened against upstream: the final task extends the hand-off with the exact `gh pr create` commands for the human.

**Tech Stack:** React 19 renderer, Radix (`Popover`, `Dialog`, `Select`), `react-i18next`, zustand (`ui-store`), Vitest 4 (jsdom), TypeScript `tsc --noEmit`; Go 1.26 (`chi`, `httptest`), `openapi-typescript` + `go generate` for the `api-drift` gate.

**Spec:** `docs/upstreaming-remote-sessions.md` (merged, #123). This plan implements §2.4 A6, A7a and A7b, and the §3.3 scrub list. Plan 1 (`docs/superpowers/plans/2026-08-23-upstream-remote-hosts-foundation.md`) built A0–A5; Plan 3 (A8a–A11) and Plan C (CLI) follow separately.

## Global Constraints

- **Do not push to, open PRs against, or comment on `Untrivial-ai/agent-orchestrator`.** All pushes go to `origin` (`AronPerez/agent-orchestrator`). The human opens upstream PRs from Task 5's hand-off. `git fetch upstream` is the only upstream operation this plan performs.
- **Clean refs are the public names.** Branches are `up-a6-host-ui`, `up-a7a-fs-dirs`, `up-a7b-folder-picker` — no session namespace, matching `up-a1-flag` … `up-a5-clients`. Never open an upstream PR from a namespaced ref.
- **Flag off ⇒ zero remote network, still** (spec §2.2): with `remoteHosts` false, `useRemoteHosts` lists nothing and probes nothing, and no host row is rendered. Plan 1's off-state invariant is not weakened by adding UI on top of it; Task 2 adds the falsifying test for the UI half.
- **Flag off ⇒ byte-identical behaviour.** With the flag off, `CreateProjectFlow` renders exactly upstream's tree: `hostRow` is `null`, `ProjectSourcePickerView` gets `hostRow={undefined}`, `remoteHost` is `undefined`, and every folder path is the native picker's.
- **Every branch is green on its own base:** its own suites, plus `tsc --noEmit` and `tsc --noEmit -p tsconfig.e2e.json`. A7a additionally: `go build ./...`, `go vet ./...`, `go test ./internal/httpd/...`, and `npm run api` producing **no** diff (the `api-drift` gate).
- **Upstream conventions** (`AGENTS.md`): surgical changes, no drive-by cleanup, conventional commits, **tabs in `.ts/.tsx`**, every `hosts.*`/`fsBrowse.*` i18n key present in all eight locales (`de en es fr ja ko pt-BR zh-CN`), ≤15 non-test files per PR.
- **Scrub before every commit** (spec §3.3): `grep -rnE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:"` over the branch's staged files must print nothing. **This one is not a formality on this plan:** `origin/develop`'s `CreateProjectFlow.tsx` carries a live `// ponytail:` comment on the `onReconnect` prop. Task 2 drops it by construction; the scrub is the backstop.
- **Test commands** (from `$STACK/frontend`): unit `node_modules/.bin/vitest run --config vite.renderer.config.ts <files>`; typecheck `node_modules/.bin/tsc --noEmit`; e2e typecheck `node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json`. Node is off-PATH: `export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"`. Go is on PATH (`go1.26.5 darwin/arm64`).
- **Fork source ref:** everything ports from `origin/develop @ 32d089f37`. Every file named below was verified to exist there on 2026-08-24.
- **Known env traps:** there are no npm workspaces — install per package (`npm ci --prefix packages/product-ui`); the frontend landing suites fail on clean `develop` and are not this stack's problem (the stack worktree runs named files only, never the whole suite); `routeTree.gen.ts` churn is noise and is never staged.

### Deviations from spec §2.4, decided here

Four, each because the spec's file list does not survive contact with what the code imports:

1. **`HostSwitcher.tsx` moves out of A6, into Plan 3 (A10).** §2.4 lists it under A6, but it imports `HostSection` from `types/workspace.ts`, a type A8a introduces and which does not exist on `upstream/main` or on the A6 base. It cannot compile in A6. Its two keys (`hosts.viewing`, `hosts.allHosts`) move with it. Verified: `git show upstream/main:frontend/src/renderer/types/workspace.ts | grep -c HostSection` → `0`.
2. **`reportHostConnect` is dropped from the ported A6 files.** `useRemoteHosts.ts` and `AddRemoteHostDialog.tsx` on `develop` call it; `lib/host-telemetry.ts` is §2.4's A10 (fork #104), and pulling it into A6 would drag the renderer telemetry allowlist review into a UI PR. Neither call is load-bearing — no A6 test references `host-telemetry` (verified: 0 hits across all six A6/A7b test files) — so the two call sites and their `startedAt` timings are removed. A10 restores them. Verified: `frontend/src/renderer/lib/telemetry.ts` on `upstream/main` has no `host_id` in its allowlist, so shipping the calls in A6 would silently drop the property anyway.
3. **Registering a project on a remote host moves from A7b into A6.** §2.4 gives A6 "one mount point in `CreateProjectFlow.tsx`" and A7b the host choice's effect. Split that way, A6 ships a host dropdown where picking *workbox* then *Open local repository* opens the **local** native folder dialog and registers the project on the **local** daemon — a control that silently acts on the wrong machine. So A6 carries the picker *and* what picking does (`openFolderStep`'s remote branch, the absolute-path field, `createRemoteProject`, `lib/daemon-error.ts`), and A7b is exactly what its title says: browsing that host's folders instead of typing the path. A6 lands at 14 non-test files, inside the ≤15 rule; §2.4 already flagged A6 as the stack's largest UI PR at ~700 lines and offered to split add/edit from remove if reviewers ask.
4. **`lib/response-validation.ts` lands in A7b, not A6.** Plan 1 deferred it out of A5 for having no caller; its only caller anywhere is `RemoteFolderPicker.parseListing`, so it ships with it.

Two more, found at RED during execution rather than during reading, and recorded here so a re-run does not rediscover them:

5. **`frontend/src/renderer/test/fake-daemon.ts` ships with A6.** `AddRemoteHostDialog.test.tsx` imports it — two `it.each` cases prove a 200-HTML or wrong-shape body from an older daemon surfaces as "not an AO daemon" instead of throwing. §2.4 puts the hostile-daemon *harness* (fork #87) in A10, but the fixture is a 60-line `fetch` stub with no production import, and A6 is where the first test needs it. It ships **trimmed to the behaviours wave 2 exercises** (`healthy`, `html-catchall`, `wrong-shape`, `unauthorized`, `unreachable`), dropping `route-missing` and the `/api/v1/fs/dirs` case — both name a route A6 has not introduced — and `slow`, which has no consumer before Plan 3. Its own `fake-daemon.test.ts` ships with it, trimmed the same way, so no behaviour is shipped untested.
6. **One test case leaves `AddRemoteHostDialog.test.tsx` with the telemetry.** `it("reports which way an add failed, and never the address or the password")` asserts `captureRendererEvent` was called with `ao.renderer.host_connect`; with `reportHostConnect` removed (deviation 2) it has no subject. The case, the `vi.mock("../lib/telemetry", …)` and the `captureRendererEventMock` handle move to A10 together. The UI half of what it covered stays pinned by "distinguishes a wrong password from an unreachable host" and "says the host is unreachable when it does not answer". Evidence for deviation 2, gathered here: upstream's `sanitizeRendererProperties` (`lib/telemetry.ts:448`) is a per-event `switch` with an explicit allowlist, so `ao.renderer.host_connect` shipped in A6 would emit an event with **every** property stripped — adding its case is exactly the telemetry review §2.4 assigns to A10.

One spec path correction, not a deviation: §2.4 A7a names `specgen/build.go`; the file is `backend/internal/httpd/apispec/specgen/build.go`.

---

## File structure

Worktree `$STACK` = `/Users/amongstar/dev/agent-orchestrator-up-stack` (built by Plan 1 Task 1; reused, not recreated). Paths below are relative to `$STACK`.

| Branch | Base | Creates | Modifies | Responsibility |
| --- | --- | --- | --- | --- |
| `up-a6-host-ui` | `up-a5-clients` | `frontend/src/renderer/hooks/useRemoteHosts.ts`, `components/HostSelect.tsx`, `components/AddRemoteHostDialog.tsx`, `lib/daemon-error.ts` (+ 4 test files) | `components/CreateProjectFlow.tsx`, `packages/product-ui/src/ProjectViews.tsx`, `i18n/*.json` ×8 | List/probe saved hosts; pick one; add, edit and remove them; register a project on the picked host by absolute path. All behind the flag. |
| `up-a7a-fs-dirs` | `upstream/main` | `backend/internal/httpd/controllers/fs.go`, `fs_test.go` | `controllers/dto.go`, `httpd/api.go`, `apispec/specgen/build.go`, `apispec/openapi.yaml` (generated), `frontend/src/api/schema.ts` (generated), `httpd/lan_listener_test.go` | `GET /api/v1/fs/dirs`: directories only, dotfiles skipped, 500-entry cap, `.git` detection. Additive; credential-gated on the LAN listener like every other data route. |
| `up-a7b-folder-picker` | merge(A6, A7a), tag `up-a7b-base` | `frontend/src/renderer/components/RemoteFolderPicker.tsx`, `lib/response-validation.ts` (+ 2 test files) | `components/CreateProjectFlow.tsx`, `i18n/*.json` ×8 | Browse the selected host's directories over `fs/dirs` and drop the chosen path into the field A6 added. Typed paths keep working. |
| (develop) | `plan/2026-08-24-hostui` | `docs/superpowers/plans/2026-08-24-upstream-host-ui-fs.md`, `docs/upstreaming-pr-bodies/a6-host-ui.md`, `a7a-fs-dirs.md`, `a7b-folder-picker.md` | `docs/upstreaming-stack-status.md` | This plan, the three new PR bodies, and the runbook extended to eight branches. |

Module boundaries, fixed here so tasks agree on names:

- `hooks/useRemoteHosts.ts` — `LOCAL_HOST_ID = "local"`, `type RemoteHealth` (re-exported from `main/remote-request`), `type HostStatus = "local" | "checking" | RemoteHealth`, `probeFailed(status)`, `type RemoteHostView = {label, url}`, `type Host = {id, label, url: string | null, status}`, `remotesBridge()`, `useRemoteHosts(): {hosts, refresh}`.
- `components/HostSelect.tsx` — `HostSelect({hosts, value, onChange, onAddHost, onReconnect?, onEditHost?, onRemoveHost?})`.
- `components/AddRemoteHostDialog.tsx` — `AddRemoteHostDialog({open, onOpenChange, host?, onSaved})`; internal `normalizeHostUrl`, `hasUserinfo`.
- `lib/daemon-error.ts` — `daemonErrorMessage(body: unknown): string | null`.
- `lib/response-validation.ts` — `parseResponseArray<T>(body, key, isItem): T[] | null`.
- `components/RemoteFolderPicker.tsx` — `RemoteFolderPicker({hostLabel, hostUrl, open, onOpenChange, onSelect})`.
- `controllers/fs.go` — `FSController{Home func() (string, error)}`, `Register(chi.Router)`, `maxDirEntries = 500`.
- `controllers/dto.go` — `ListDirsQuery{Path}`, `FSEntry{Name, Path, GitRepo}`, `ListDirsResponse{Path, Parent, Entries, Truncated}`.

`LOCAL_HOST_ID` in `useRemoteHosts.ts` and `LOCAL_HOST` in `lib/hosts.ts` (A2) are the same string `"local"` and stay two constants: the hook's is the picker's row id, A2's is the `Ref` host id. A9 collapses them when every write is `Ref`-routed; doing it here would touch files no PR in this wave owns.

---

### Task 1: Refresh the stack worktree and re-baseline

The Plan 1 worktree already exists with a symlinked `frontend/node_modules` and an installed `packages/product-ui`. Do not recreate it. Its checked-out branch (`ao/agent-orchestrator-96/up-a5-clients`) has **no remote twin** — the namespaced twins were deleted and the clean refs are canonical — so every checkout below names `origin/up-a*` explicitly and never trusts a tracking ref.

**Files:** none (verification only).

- [ ] **Step 1: Fetch, confirm the ground truth, confirm the worktree**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
export W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-100
export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
cd "$W" && git fetch upstream --quiet && git fetch origin --quiet
echo "upstream/main $(git rev-parse --short upstream/main)"
for b in up-a1-flag up-a2-hosts up-a3-store up-a4-proxy up-a5-clients; do echo "$b $(git rev-parse --short origin/$b)"; done
git -C "$STACK" status --porcelain | head
git -C "$STACK" tag -l up-a5-base
ls -l "$STACK/frontend/node_modules" | head -1
```

Expected, verified 2026-08-24: `upstream/main 6cba6344c`; `up-a1-flag 616dd08af`, `up-a2-hosts 9c0010aad`, `up-a3-store 4c4e7e9d1`, `up-a4-proxy 825dfde92`, `up-a5-clients 39fa64f23`; `git status` clean; `up-a5-base` present; `node_modules` is a symlink into `/Users/amongstar/dev/agent-orchestrator/frontend/node_modules`. **If `upstream/main` has moved past `6cba6344c`, stop and report** — the Plan 1 branches are cut from `6cba6344c` and A7a must be cut from the same commit as they were, or the hand-off's rebase recipe describes a topology that no longer exists.

- [ ] **Step 2: Baseline the A6 base — prove A5 is still green before building on it**

```bash
cd "$STACK" && git checkout -q --detach origin/up-a5-clients
cd frontend
node_modules/.bin/vitest run --config vite.renderer.config.ts \
  src/renderer/lib/host-clients.test.ts src/renderer/lib/active-host.test.ts src/renderer/i18n/instance.test.ts 2>&1 | grep -E "×|Test Files|Tests "
node_modules/.bin/tsc --noEmit && echo TSC_OK
node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json && echo E2E_TSC_OK
```

Expected: `Test Files  3 passed (3)`, `TSC_OK`, `E2E_TSC_OK`. A failure here is the environment or a Plan 1 regression, not this plan — fix it before Task 2.

**A whole-suite run is not a clean baseline and must not be used as one.** Measured 2026-08-24 on untouched `upstream/main`: `vitest run` over all 227 files reports `11 failed | 216 passed`, `5 tests failed`, every failure in `src/landing/**` or `src/annotate-preload.test.ts` and every one an `ERR_MODULE_NOT_FOUND` — the landing app is a separate package and this worktree never installed it (the no-npm-workspaces trap). "Green" in this plan therefore means the named suites plus both typechecks. If a whole-suite run is wanted as a cross-check, A/B it: run the same file list on `upstream/main` and require the failure sets to be identical, which they were for A7b.

- [ ] **Step 3: Baseline the A7a base — prove upstream's Go tree is green**

```bash
cd "$STACK" && git checkout -q --detach upstream/main
cd backend && go build ./... && go vet ./internal/httpd/... && go test ./internal/httpd/... 2>&1 | tail -12
```

Expected: `ok` (or `no test files`) for every `internal/httpd/...` package, no `FAIL`.

---

### Task 2: A6 — `feat(hosts): add, edit and remove remote hosts`

Branch `up-a6-host-ui` from `origin/up-a5-clients`. Ports fork #65 + #72 + #106 (a11y) + the host-choice half of #68, minus telemetry (deviation 2) and minus `HostSwitcher` (deviation 1).

**Files:**
- Create: `frontend/src/renderer/hooks/useRemoteHosts.ts`, `useRemoteHosts.test.tsx`
- Create: `frontend/src/renderer/components/HostSelect.tsx`, `HostSelect.test.tsx`
- Create: `frontend/src/renderer/components/AddRemoteHostDialog.tsx`, `AddRemoteHostDialog.test.tsx`
- Create: `frontend/src/renderer/lib/daemon-error.ts`
- Create: `frontend/src/renderer/components/CreateProjectFlowHosts.test.tsx`
- Modify: `frontend/src/renderer/components/CreateProjectFlow.tsx` (upstream anchors: imports 1–23, state block ending line 88, `hasModePicker`/`isBusy` 90–91, `selectSource` 93–107, `ImportSourcePicker` mount 266, `CreateProjectFolderDialog` mount 317, `ImportSourcePicker` 452–500, `CreateProjectFolderDialog` 525–681)
- Modify: `packages/product-ui/src/ProjectViews.tsx` (3 lines: prop type line 46, destructure line 60, render line 76)
- Modify: `frontend/src/renderer/i18n/{de,en,es,fr,ja,ko,pt-BR,zh-CN}.json` (37 keys each)

**Interfaces:**
- Consumes: `useUiStore().remoteHosts` (A1), `aoBridge.remotes.{list,add,update,remove,probe,request}` (A3), `connectHost`/`disconnectHost` from `lib/host-clients` (A5), `type RemoteHealth` from `../../main/remote-request` (A3).
- Produces: the `useRemoteHosts.ts`, `HostSelect.tsx`, `AddRemoteHostDialog.tsx` and `daemon-error.ts` signatures in the File structure section, and a `hostRow?: ReactNode` slot on `ProjectSourcePickerView`.

- [ ] **Step 1: Branch, bring the four test files over, watch them fail**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
export W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-100
export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
cd "$STACK" && git checkout -q -b up-a6-host-ui origin/up-a5-clients
for f in src/renderer/hooks/useRemoteHosts.test.tsx \
         src/renderer/components/HostSelect.test.tsx \
         src/renderer/components/AddRemoteHostDialog.test.tsx \
         src/renderer/components/CreateProjectFlowHosts.test.tsx; do
  git -C "$W" show origin/develop:frontend/$f > "$STACK/frontend/$f"
done
cd frontend && node_modules/.bin/vitest run --config vite.renderer.config.ts \
  src/renderer/hooks/useRemoteHosts.test.tsx src/renderer/components/HostSelect.test.tsx \
  src/renderer/components/AddRemoteHostDialog.test.tsx src/renderer/components/CreateProjectFlowHosts.test.tsx 2>&1 | grep -E "Error|Tests |Test Files" | head -6
```

Expected: three suites fail to resolve (`Failed to resolve import "../hooks/useRemoteHosts"` / `"./HostSelect"` / `"./AddRemoteHostDialog"`); `CreateProjectFlowHosts.test.tsx` collects but its five cases fail on the missing `host:` button. Nothing passes that should not.

`AddRemoteHostDialog.test.tsx` also fails to resolve `../test/fake-daemon` — that is deviation 5, and Step 2b writes the trimmed fixture. Note too that both `HostSelect.test.tsx` and `AddRemoteHostDialog.test.tsx` stay red after their modules exist and only go green once Step 5 lands the i18n keys: they match on rendered labels, and `t()` returns the bare key until the catalogue has it. Do not go hunting for a component bug in between.

The four suites are 5 + 12 + 21 + 6 = 44 cases. What they pin, in one line each: the flag off means `remotes.list`/`remotes.probe` are never called and no host row renders; every saved host shows as `checking` before its probe answers, so a slow host never looks like a missing one; an unreachable host stays focusable but is not selectable and says why in text, not colour; the add dialog refuses a URL carrying userinfo and refuses an unparseable address with a *different* sentence than an unreachable host; a blank password on an edit keeps the saved one; a probe is announced through `role="status"` and its failure through `role="alert"` that clears on retype; removing a host names the machine in the confirmation, and both edit and remove drop the renderer's client for the old base.

- [ ] **Step 2: Port the three renderer modules and `daemon-error.ts`, minus telemetry**

```bash
cd "$STACK"
for f in src/renderer/hooks/useRemoteHosts.ts \
         src/renderer/components/HostSelect.tsx \
         src/renderer/components/AddRemoteHostDialog.tsx \
         src/renderer/lib/daemon-error.ts; do
  git -C "$W" show origin/develop:frontend/$f > "$STACK/frontend/$f"
done
```

Then make exactly three edits (deviation 2). In `frontend/src/renderer/hooks/useRemoteHosts.ts`, delete the import line

```ts
import { reportHostConnect } from "../lib/host-telemetry";
```

and reduce the probe body inside `refresh` from

```ts
			saved.map(async (host) => {
				const startedAt = Date.now();
				const status = await remotesBridge().probe(host.url);
				reportHostConnect(host.url, "probe", status, Date.now() - startedAt);
				setRemotes((current) => current.map((row) => (row.id === host.url ? { ...row, status } : row)));
			}),
```

to

```ts
			saved.map(async (host) => {
				const status = await remotesBridge().probe(host.url);
				setRemotes((current) => current.map((row) => (row.id === host.url ? { ...row, status } : row)));
			}),
```

In `frontend/src/renderer/components/AddRemoteHostDialog.tsx`, delete the same import line and reduce the save body inside `submit` from

```ts
			const startedAt = Date.now();
			const health = editing
				? await remotesBridge().update(editing.url, {
```

…through the call…

```ts
			// Which failure mode dominates here is the whole question behind
			// "is adding a host working?" — a wrong password and an unreachable
			// machine are the same dead dialog to a user and different bugs to us.
			reportHostConnect(normalized, editing ? "edit" : "add", health, Date.now() - startedAt);
			if (health === "online") {
```

to the same statement without the timing and the report:

```ts
			const health = editing
				? await remotesBridge().update(editing.url, {
						label: label.trim(),
						url: normalized,
						// Omitted, not "": an empty string would wipe a working password.
						...(password === "" ? {} : { password }),
					})
				: await remotesBridge().add({ label: label.trim(), url: normalized, password });
			if (health === "online") {
```

Verify no telemetry import survives:

```bash
grep -rn "host-telemetry\|reportHostConnect" "$STACK/frontend/src/renderer" ; echo "telemetry refs exit=$? (1 means clean)"
```

- [ ] **Step 2b: Write the trimmed fake-daemon fixture, and drop the telemetry-only test case**

Per deviations 5 and 6. Write `frontend/src/renderer/test/fake-daemon.ts` exporting `type Behaviour = "healthy" | "html-catchall" | "wrong-shape" | "unauthorized" | "unreachable"` and `fakeDaemon(behaviour): typeof fetch` — `healthy` answers `/healthz`, `/readyz`, `/api/v1/projects`, `/api/v1/sessions` and 404s the rest; `html-catchall` answers every path `200 text/html`; `wrong-shape` answers `200 {"ok":true}`; `unauthorized` answers the daemon's real `401 {error,code:"BAD_PASSWORD",message,requestId}` envelope; `unreachable` throws `TypeError("fetch failed")`. Write `fake-daemon.test.ts` with one case per behaviour.

Then in `AddRemoteHostDialog.test.tsx`, delete `captureRendererEventMock` from the `vi.hoisted` block, the `vi.mock("../lib/telemetry", …)` line, the `captureRendererEventMock.mockClear();` in `beforeEach`, and the whole `it("reports which way an add failed, and never the address or the password", …)` case. Confirm:

```bash
grep -c "captureRendererEvent\|telemetry" "$STACK/frontend/src/renderer/components/AddRemoteHostDialog.test.tsx"
```

Expected: `0`.

- [ ] **Step 3: Add the `hostRow` slot to `product-ui` (three lines, tabs)**

`packages/product-ui/src/ProjectViews.tsx` on `develop` is space-reformatted; the real change is three lines. Apply them by hand against upstream's tab-indented file:

```tsx
// in ProjectSourcePickerViewProps, after `folderIcon?: ReactNode;` (line 46):
	hostRow?: ReactNode;
// in the ProjectSourcePickerView destructure, after `folderIcon,` (line 60):
	hostRow,
// in the render, between the title/description block's closing </div> and the
// `grid grid-cols-1 gap-4` div (line 76):
			{hostRow}
```

```bash
cd "$STACK" && git diff --stat packages/product-ui/src/ProjectViews.tsx
```

Expected: `1 file changed, 3 insertions(+)`. Anything larger means the reformatted `develop` version leaked in — reset the file and redo by hand.

- [ ] **Step 4: Wire `CreateProjectFlow.tsx` by hand (tabs, not `git apply`)**

`origin/develop`'s copy of this file is space-indented end to end (1,006/602 raw against upstream, 459/55 with `-w`), so **no patch tool can port it** — every hunk below is written out against upstream's tab-indented file. It also carries a `// ponytail:` comment that must not ship; the `onReconnect` prop below is the scrubbed form.

Imports (upstream lines 1–23) — add five, in the existing order:

```tsx
import type { ImportFolderScan } from "../../preload";
import { LOCAL_HOST_ID, remotesBridge, useRemoteHosts, type Host, type RemoteHostView } from "../hooks/useRemoteHosts";
import { aoBridge } from "../lib/bridge";
import { daemonErrorMessage } from "../lib/daemon-error";
import { connectHost, disconnectHost } from "../lib/host-clients";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import type { ProjectKind } from "../types/workspace";
import { AddRemoteHostDialog } from "./AddRemoteHostDialog";
import { ConfirmDialog } from "./ConfirmDialog";
import { CreateProjectAgentSheet, type CreateProjectAgentSelection } from "./CreateProjectAgentSheet";
import { HostSelect } from "./HostSelect";
```

and add `useId` to the existing `react` import (line 16).

State, after `pendingDropPath` (upstream line 88):

```tsx
	const { hosts, refresh: refreshHosts } = useRemoteHosts();
	const [hostId, setHostId] = useState<string>(LOCAL_HOST_ID);
	const [addHostOpen, setAddHostOpen] = useState(false);
	const [editingHost, setEditingHost] = useState<RemoteHostView | null>(null);
	const [removingHost, setRemovingHost] = useState<RemoteHostView | null>(null);
	const [removingHostBusy, setRemovingHostBusy] = useState(false);
	const [remotePath, setRemotePath] = useState("");
```

After `isBusy` (upstream line 91), the selected host and the folder-step fork:

```tsx
	// The selected host when it is not "This Mac". Undefined while a just-added
	// host is still being listed, so the flow is never pointed at nothing.
	const remoteHost = hosts.find((host): host is Host & { url: string } => host.id === hostId && host.url !== null);

	const openFolderStep = (kind: ProjectKind, presetPath?: string) => {
		if (remoteHost) {
			// chooseDirectory opens a native picker on *this* machine, so a remote
			// path is typed rather than picked. A dropped path also names a folder
			// on this machine, so it is dropped rather than guessed at over there.
			setError(null);
			setValidationScan(null);
			setRemotePath("");
			setSelectedKind(kind);
			setModePickerOpen(false);
			setFolderPickerOpen(true);
			return;
		}
		// Keep the selector mounted behind the native picker. Closing it first
		// exposes a blank compositor frame on Windows before Explorer takes focus.
		void chooseDirectory(kind, presetPath);
	};
```

In `selectSource` (upstream lines 104–106), replace the two-line comment and the `void chooseDirectory(...)` call with:

```tsx
		openFolderStep(source === "workspace" ? "workspace" : "single_repo", presetPath ?? undefined);
```

(the comment moved into `openFolderStep`, where the native picker now actually opens).

After `selectSource`, the three host operations:

```tsx
	// Registering a project on a remote daemon is REST-only, which is why this
	// slice works at all: the session stream and terminal cannot carry a Bearer
	// token. Nothing local changes, so there is no local list to refresh after.
	const createRemoteProject = async () => {
		if (!remoteHost) return;
		setError(null);
		setIsCreating(true);
		try {
			const response = await remotesBridge().request(remoteHost.url, {
				method: "POST",
				path: "/api/v1/projects",
				body: { path: remotePath.trim(), asWorkspace: selectedKind === "workspace" },
			});
			if (response.status >= 200 && response.status < 300) {
				setFolderPickerOpen(false);
				setRemotePath("");
				return;
			}
			// The daemon owns the verdict on its own filesystem — judging the path
			// here would judge the wrong machine's OS.
			setError(daemonErrorMessage(response.body) ?? t("createProject.couldNotAdd"));
		} catch (err) {
			setError(err instanceof Error ? err.message : t("createProject.couldNotAdd"));
		} finally {
			setIsCreating(false);
		}
	};

	// A saved host that was renamed, re-pointed or given a rotated password. The
	// url is the identity everything else keys off. Main drops the old proxy on
	// every edit, including password-only changes, so replace the renderer's
	// cached base with the fresh proxy before any project can write through it.
	const hostSaved = async (previousUrl: string, savedUrl: string) => {
		await refreshHosts();
		setHostId((current) => (current === previousUrl ? savedUrl : current));
		await disconnectHost(previousUrl);
		await connectHost(savedUrl);
	};

	const removeHost = async (url: string) => {
		setRemovingHostBusy(true);
		try {
			await remotesBridge().remove(url);
			await refreshHosts();
			setHostId((current) => (current === url ? LOCAL_HOST_ID : current));
			setRemovingHost(null);
			// Main already dropped the proxy; clear the renderer's matching client.
			await disconnectHost(url);
		} finally {
			setRemovingHostBusy(false);
		}
	};

	const remoteHostsEnabled = useUiStore((state) => state.remoteHosts);
	// null with the flag off, so ProjectSourcePickerView receives no slot at all
	// and renders exactly the tree it does today.
	const hostRow =
		hasModePicker && remoteHostsEnabled ? (
			<HostSelect
				hosts={hosts}
				value={hostId}
				onChange={setHostId}
				onAddHost={() => setAddHostOpen(true)}
				// Re-probes every host, not just this one; the list is a handful.
				onReconnect={() => void refreshHosts()}
				onEditHost={setEditingHost}
				onRemoveHost={setRemovingHost}
			/>
		) : null;
```

Both `ImportSourcePicker` mounts get the slot — upstream line 266 becomes `<ImportSourcePicker disabled={isBusy} hostRow={hostRow} onSelect={selectSource} />`, and `CreateProjectSourceDialog` (line 317's sibling, declared at 428) gains a `hostRow?: ReactNode` prop it forwards to its own `ImportSourcePicker` (line 444). `ImportSourcePicker` itself (line 452) gains `hostRow?: ReactNode` and passes it to `ProjectSourcePickerView` wrapped in its label:

```tsx
				hostRow={
					hostRow ? (
						<div className="relative z-[2] flex flex-col items-start gap-2 self-stretch">
							<span className="text-[13px] font-medium text-[var(--color-text-import-muted)]">{t("hosts.label")}</span>
							{hostRow}
						</div>
					) : undefined
				}
```

Before the `<CreateProjectFolderDialog` mount (upstream line 317), the three host dialogs:

```tsx
					<AddRemoteHostDialog
						open={addHostOpen}
						onOpenChange={setAddHostOpen}
						onSaved={(url) => {
							void refreshHosts();
							setHostId(url);
						}}
					/>
					{/* Its own mount rather than a mode on the add dialog: `host` is what
					    switches the form, and a single dialog would have to keep holding
					    the edited host through the close animation to avoid flashing "Add". */}
					<AddRemoteHostDialog
						host={editingHost}
						open={editingHost !== null}
						onOpenChange={(open) => !open && setEditingHost(null)}
						onSaved={(url) => {
							const previousUrl = editingHost?.url;
							setEditingHost(null);
							if (previousUrl) void hostSaved(previousUrl, url);
						}}
					/>
					<ConfirmDialog
						open={removingHost !== null}
						onOpenChange={(open) => !open && !removingHostBusy && setRemovingHost(null)}
						title={t("hosts.remove.title")}
						description={
							<>
								<p className="text-sm font-medium text-foreground">
									{t("hosts.remove.lead", { host: removingHost?.label ?? "" })}
								</p>
								<p className="mt-1 text-xs text-muted-foreground">{t("hosts.remove.body")}</p>
							</>
						}
						confirmLabel={t("hosts.remove.confirm")}
						destructive
						busy={removingHostBusy}
						onConfirm={() => {
							if (removingHost?.url) void removeHost(removingHost.url);
						}}
					/>
```

and the `<CreateProjectFolderDialog` mount itself gains four props:

```tsx
						remoteHost={remoteHost ?? null}
						remotePath={remotePath}
						onRemotePathChange={setRemotePath}
						onSubmitRemote={() => void createRemoteProject()}
```

`CreateProjectFolderDialog` (upstream 525–681) gains the four props in its signature and type (`remoteHost: { label: string; url: string } | null`, `remotePath: string`, `onRemotePathChange: (path: string) => void`, `onSubmitRemote: () => void`), a `const remotePathId = useId();` beside `const { t } = useTranslation();`, a middle branch in the `{hasScan ? … : …}` ternary (upstream line 641's `) : (` becomes `) : remoteHost ? (` … `) : (`):

```tsx
						) : remoteHost ? (
							<div className="flex flex-col gap-2">
								<label
									className="text-[13px] font-semibold text-[var(--color-text-import-title)]"
									htmlFor={remotePathId}
								>
									{t("hosts.remotePath", { host: remoteHost.label })}
								</label>
								<input
									id={remotePathId}
									autoComplete="off"
									spellCheck={false}
									className="settings-field-control h-(--size-settings-action-height) min-w-0 flex-1 font-mono"
									disabled={disabled}
									value={remotePath}
									onChange={(event) => onRemotePathChange(event.target.value)}
								/>
								<p className="text-[12px] text-[var(--color-text-import-muted)]">{t("hosts.remotePathHint")}</p>
							</div>
						) : (
```

the error block's live region (upstream line 658's `<div`):

```tsx
							<div
								// The remote path has no scan card to hang the failure on, so the
								// daemon's rejection has to announce itself.
								role={remoteHost ? "alert" : undefined}
								className={cn(
```

and the submit button beside Cancel in the footer (after upstream line 674's `</Button>`):

```tsx
							{remoteHost && (
								<Button
									type="button"
									variant="footer-primary"
									disabled={disabled || remotePath.trim() === ""}
									onClick={onSubmitRemote}
								>
									{t("hosts.addProjectOn", { host: remoteHost.label })}
								</Button>
							)}
```

The A7b Browse button belongs in the field's row and is **not** added here; Task 4 wraps the `<input>` in a flex row and adds it.

- [ ] **Step 5: Add the 37 i18n keys to all eight locales**

Every key below must exist in all eight files or `tsc` fails on `MessageKey` and `instance.test.ts` fails on parity. Take them verbatim from `develop`, which already has all eight translated — this filters `develop`'s 62 `hosts.*`/`fsBrowse.*` keys down to A6's 37, dropping A7b's `fsBrowse.*` (Task 4) and Plan 3's fourteen (`hosts.viewing`, `hosts.allHosts`, `hosts.backToLocal`, `hosts.passwordChanged`, `hosts.on`, `hosts.remoteSection`, `hosts.open`, `hosts.unreachable`, `hosts.peekEmpty`, `hosts.qualified`, `hosts.sectionFailed`, `hosts.retry`, `hosts.liveUpdatesOffline`, `hosts.liveUpdatesOffline.hint`):

```bash
cd "$STACK"
cat > /tmp/a6-keys.txt <<'EOF'
hosts.local
hosts.addRemote
hosts.addRemote.hint
hosts.connect
hosts.connectTo
hosts.label
hosts.switcher
hosts.status.checking
hosts.status.offline
hosts.status.online
hosts.status.notADaemon
hosts.status.unauthorized
hosts.add.address
hosts.add.errorCredentialInUrl
hosts.add.errorInvalidAddress
hosts.add.errorOffline
hosts.add.errorNotADaemon
hosts.add.errorUnauthorized
hosts.add.name
hosts.add.password
hosts.add.passwordHint
hosts.add.submit
hosts.add.title
hosts.add.willConnectTo
hosts.edit
hosts.remove
hosts.edit.title
hosts.edit.hint
hosts.edit.passwordHint
hosts.edit.submit
hosts.remove.title
hosts.remove.lead
hosts.remove.body
hosts.remove.confirm
hosts.remotePath
hosts.remotePathHint
hosts.addProjectOn
EOF
for l in de en es fr ja ko pt-BR zh-CN; do
  git -C "$W" show origin/develop:frontend/src/renderer/i18n/$l.json \
    | grep -F -f <(sed 's/^/\t"/;s/$/":/' /tmp/a6-keys.txt) > /tmp/a6-$l.txt
  echo "$l $(wc -l < /tmp/a6-$l.txt)"
done
```

Expected: `37` for every locale. Splice each locale's block into `$STACK/frontend/src/renderer/i18n/$l.json` immediately before the closing `}`, keeping the file's existing tab indentation and adding a trailing comma to the line that was previously last. Then:

```bash
cd "$STACK/frontend"
for l in de en es fr ja ko pt-BR zh-CN; do node -e "JSON.parse(require('fs').readFileSync('src/renderer/i18n/$l.json','utf8'));console.log('$l ok')"; done
```

Expected: eight `ok` lines. A `SyntaxError` here is a missing or doubled comma at the splice point.

- [ ] **Step 6: Verify green**

```bash
cd "$STACK/frontend"
node_modules/.bin/vitest run --config vite.renderer.config.ts \
  src/renderer/hooks/useRemoteHosts.test.tsx src/renderer/components/HostSelect.test.tsx \
  src/renderer/components/AddRemoteHostDialog.test.tsx src/renderer/components/CreateProjectFlowHosts.test.tsx \
  src/renderer/components/CreateProjectFlow.test.tsx src/renderer/i18n/instance.test.ts 2>&1 | grep -E "×|Test Files|Tests "
node_modules/.bin/tsc --noEmit && echo TSC_OK
node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json && echo E2E_TSC_OK
npm --prefix "$STACK/packages/product-ui" test 2>&1 | grep -E "×|Test Files|Tests "
```

Expected: `Test Files  6 passed (6)`, `TSC_OK`, `E2E_TSC_OK`, and product-ui's own suite green (`ProjectViews.test.tsx` must still pass — the new prop is optional, so it does not touch existing callers). `CreateProjectFlow.test.tsx` is upstream's own suite and is the flag-off regression check: it renders with `remoteHosts` at its default `false` and must be untouched.

- [ ] **Step 7: Scrub and commit**

```bash
cd "$STACK"
git add frontend/src/renderer/hooks/useRemoteHosts.ts frontend/src/renderer/hooks/useRemoteHosts.test.tsx \
  frontend/src/renderer/components/HostSelect.tsx frontend/src/renderer/components/HostSelect.test.tsx \
  frontend/src/renderer/components/AddRemoteHostDialog.tsx frontend/src/renderer/components/AddRemoteHostDialog.test.tsx \
  frontend/src/renderer/components/CreateProjectFlow.tsx frontend/src/renderer/components/CreateProjectFlowHosts.test.tsx \
  frontend/src/renderer/lib/daemon-error.ts packages/product-ui/src/ProjectViews.tsx \
  frontend/src/renderer/i18n/de.json frontend/src/renderer/i18n/en.json frontend/src/renderer/i18n/es.json frontend/src/renderer/i18n/fr.json \
  frontend/src/renderer/i18n/ja.json frontend/src/renderer/i18n/ko.json frontend/src/renderer/i18n/pt-BR.json frontend/src/renderer/i18n/zh-CN.json
git diff --cached --name-only | xargs grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 means clean)"
git status --porcelain   # must show nothing unstaged but node_modules/routeTree noise
git commit -q -m "feat(hosts): add, edit and remove remote hosts

A Host dropdown in Add-a-project lists the saved remote daemons beside
This Mac, each with its live reachability, and manages them in place:
add, edit and remove, with the connection password never leaving the
main process. Picking a remote host replaces the native folder dialog —
which would open on this machine — with an absolute path on that one,
and registers the project against that daemon over REST; the daemon owns
the verdict on its own filesystem, so a rejected path is reported in its
words rather than pre-judged here.

The dropdown is a popover rather than a Select because each row carries
Connect, Edit and Remove buttons: Radix's Select moves focus only between
options, so those buttons were mouse-only, and a listbox whose children
are buttons is not a listbox a screen reader can report. An unreachable
host stays focusable but unselectable, and says which of the four ways it
failed in text rather than colour.

All of it renders only with the Remote hosts flag on. With the flag off
no saved host is listed or probed, no row is rendered, and the flow is
byte-for-byte upstream's.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
git push -q -u origin up-a6-host-ui
git rev-parse --short HEAD
```

Expected: scrub prints nothing and `exit=1`; push succeeds; record the SHA for Task 5.

---

### Task 3: A7a — `feat(daemon): read-only directory listing at GET /api/v1/fs/dirs`

Branch `up-a7a-fs-dirs` from `upstream/main`. Ports fork #66. Go plus two generated files; it shares no code with A6 and can merge upstream in any order relative to it.

**Files:**
- Create: `backend/internal/httpd/controllers/fs.go`, `fs_test.go`
- Modify: `backend/internal/httpd/controllers/dto.go` (three types, appended after `ImportRunResponse`)
- Modify: `backend/internal/httpd/api.go` (three lines: struct field, constructor, `Register`)
- Modify: `backend/internal/httpd/apispec/specgen/build.go` (tag, three `schemaNames`, `fsOperations()`, one `append`)
- Modify: `backend/internal/httpd/lan_listener_test.go` (one assertion)
- Generated: `backend/internal/httpd/apispec/openapi.yaml`, `frontend/src/api/schema.ts`

**Interfaces:**
- Produces: `GET /api/v1/fs/dirs?path=<abs>` → `200 {path, parent, entries:[{name,path,gitRepo}], truncated?}`; `400 FS_PATH_NOT_ABSOLUTE` / `FS_NOT_A_DIRECTORY`, `403 FS_FORBIDDEN`, `404 FS_NOT_FOUND`. Consumed by A7b's `RemoteFolderPicker` via `components["schemas"]["ListDirsResponse"]`.

- [ ] **Step 1: Branch and bring the test over, watch it fail**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
export W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-100
cd "$STACK" && git checkout -q -b up-a7a-fs-dirs upstream/main
git -C "$W" show origin/develop:backend/internal/httpd/controllers/fs_test.go > backend/internal/httpd/controllers/fs_test.go
cd backend && go test ./internal/httpd/controllers/ -run TestListDirs 2>&1 | tail -5
```

Expected: a build failure — `undefined: FSController`, `undefined: ListDirsResponse`, `undefined: maxDirEntries`. The four cases pin: directories only with dotdirs and files excluded and `.git` detected as either a directory (clone) or a file (worktree); an omitted `path` defaults to the daemon user's home and reports its parent; a relative path is 400, a missing one 404, a plain file 400; and the 500-entry cap sets `truncated`.

- [ ] **Step 2: Port the controller and the DTOs**

```bash
cd "$STACK"
git -C "$W" show origin/develop:backend/internal/httpd/controllers/fs.go > backend/internal/httpd/controllers/fs.go
```

`controllers/dto.go` is heavily changed on `develop` for unrelated fork work, so append only these three types by hand, immediately after `ImportRunResponse` (upstream's `dto.go` line ~1206) and before `DevImportProjectsRequest`:

```go
// ListDirsQuery is the query string accepted by GET /api/v1/fs/dirs.
type ListDirsQuery struct {
	Path string `query:"path,omitempty" description:"Absolute directory on the daemon host to list. When omitted, the daemon user's home directory."`
}

// FSEntry is one directory in a /api/v1/fs/dirs listing.
type FSEntry struct {
	Name    string `json:"name" description:"Directory name."`
	Path    string `json:"path" description:"Absolute path of the directory on the daemon host."`
	GitRepo bool   `json:"gitRepo" description:"True when the directory carries a .git entry (clone or worktree checkout)."`
}

// ListDirsResponse is the body of GET /api/v1/fs/dirs.
type ListDirsResponse struct {
	Path      string    `json:"path" description:"Absolute path that was listed."`
	Parent    string    `json:"parent" description:"Absolute path of the listed directory's parent; equals path at the filesystem root."`
	Entries   []FSEntry `json:"entries" description:"Subdirectories, excluding dotted names."`
	Truncated bool      `json:"truncated,omitempty" description:"True when the listing hit the entry cap and more subdirectories exist."`
}
```

```bash
cd "$STACK/backend" && go test ./internal/httpd/controllers/ -run TestListDirs 2>&1 | tail -5
```

Expected: `ok  .../internal/httpd/controllers`. The endpoint is now correct but unmounted.

- [ ] **Step 3: Mount it and declare it in the spec**

`backend/internal/httpd/api.go`, three lines. In `type API struct` beside the other controllers: `fs            *controllers.FSController`. In `NewAPI`'s literal: `fs:            &controllers.FSController{},`. In `Register`, inside the same `/api/v1` group the other data controllers join: `a.fs.Register(r)`.

`backend/internal/httpd/apispec/specgen/build.go`, four edits. The tag, after the `browser` tag:

```go
		*(&openapi31.Tag{Name: "fs"}).WithDescription(
			"Read-only filesystem browsing for remote clients"),
```

Three `schemaNames` entries, beside `ControllersListSessionsQuery`:

```go
	"ControllersListDirsQuery":                            "ListDirsQuery",
	"ControllersListDirsResponse":                         "ListDirsResponse",
	"ControllersFSEntry":                                  "FSEntry",
```

The operation set, next to `importOperations`:

```go
// fsOperations declares the read-only filesystem-browsing operations. Must stay
// 1:1 with the routes FSController.Register mounts (enforced by the parity test).
func fsOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/fs/dirs", id: "listDirs", tag: "fs",
			summary:    "List the subdirectories of a directory on the daemon host",
			pathParams: []any{controllers.ListDirsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListDirsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
	}
}
```

and its registration in `operations()`, after `importOperations()`:

```go
	ops = append(ops, fsOperations()...)
```

```bash
cd "$STACK/backend" && gofmt -l internal/httpd/ && go build ./... && echo BUILD_OK
```

Expected: `BUILD_OK` and no `gofmt` output (the repo's pre-commit hook is gofmt-only, so this is the check that matters).

**Do not run `TestRouteSpecParity` yet.** It walks the real chi router against the **embedded** `openapi.yaml`, not against `operations()` in memory, so until Step 5 regenerates the spec it fails with `mounted route GET /api/v1/fs/dirs has no OpenAPI operation` — which looks like a mount bug and is not one. Step 5 runs the generators, and Step 5's own parity run is the check that the three `api.go` lines landed.

- [ ] **Step 4: Pin the LAN policy — gated, but not blocked**

Upstream's `TestLANManagerBlocksLoopbackOnlyControlRoutes` already proves the listener 401s everything unauthenticated and that a normal data route is not swallowed by the control filter. `fs/dirs` needs the second half stated for itself: being on `lanControlBlockedPrefixes` would answer 404 to an authenticated remote client and kill remote browsing silently, and no loopback test would notice. Add, in `backend/internal/httpd/lan_listener_test.go`, immediately after the existing `/api/v1/sessions` check at the end of that test:

```go
	// Browsing a remote host for a project path is the whole point of the
	// endpoint, so it must be a credential-gated data route and specifically
	// NOT a loopback-only control route — a 404 here would be a silent
	// feature kill that no loopback-side test could catch.
	fsReq, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/v1/fs/dirs", port), nil)
	fsReq.Host = "127.0.0.1" // spoofed loopback Host, as above
	fsReq.Header.Set("Authorization", "Bearer secret12")
	fsResp, err := http.DefaultClient.Do(fsReq)
	if err != nil {
		t.Fatalf("fs/dirs: request failed: %v", err)
	}
	if fsResp.StatusCode == http.StatusNotFound {
		t.Fatal("/api/v1/fs/dirs: got 404 — remote folder browsing needs it reachable over the LAN")
	}
	fsNoAuth, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/fs/dirs", port))
	if err != nil {
		t.Fatalf("fs/dirs unauthenticated: request failed: %v", err)
	}
	if fsNoAuth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/api/v1/fs/dirs unauthenticated: got %d want 401", fsNoAuth.StatusCode)
	}
```

```bash
cd "$STACK/backend" && go test ./internal/httpd/ -run TestLANManager 2>&1 | tail -3
```

Expected: `ok`. If the authenticated request returns 404, the route was mounted outside the group the LAN listener serves — fix Step 3's `Register` placement, do not exempt the test.

A passing assertion is not yet a guarding one — the listener wraps a stub handler that answers everything, so confirm it bites before trusting it:

```bash
cd "$STACK" && cp backend/internal/httpd/lan_listener.go /tmp/lan.bak
python3 -c "import pathlib;p=pathlib.Path('backend/internal/httpd/lan_listener.go');s=p.read_text();p.write_text(s.replace('var lanControlBlockedPrefixes = []string{','var lanControlBlockedPrefixes = []string{\n\t\"/api/v1/fs\",',1))"
cd backend && go test ./internal/httpd/ -run TestLANManagerBlocksLoopbackOnlyControlRoutes 2>&1 | grep -E "FAIL|remote folder browsing"
cd "$STACK" && cp /tmp/lan.bak backend/internal/httpd/lan_listener.go && git diff --stat backend/internal/httpd/lan_listener.go
```

Expected: the run **fails** with `/api/v1/fs/dirs: got 404 — remote folder browsing needs it reachable over the LAN`, and the restore leaves `lan_listener.go` with an empty diff (this branch must not modify it — only its test).

- [ ] **Step 5: Regenerate the API surface (the `api-drift` gate)**

```bash
cd "$STACK/backend" && go generate ./internal/httpd/apispec/...
cd "$STACK/frontend" && export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH" && npm run api:ts
cd "$STACK" && git diff --stat backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts
grep -n "fs/dirs" backend/internal/httpd/apispec/openapi.yaml
grep -n "ListDirsResponse\|FSEntry" frontend/src/api/schema.ts | head -4
cd "$STACK/backend" && go test ./internal/httpd/apispec/... 2>&1 | tail -3
```

Expected: both files changed and nothing else (verified 2026-08-24: `openapi.yaml +88`, `schema.ts +94`); `/api/v1/fs/dirs` present in the YAML; `ListDirsResponse` and `FSEntry` present in `schema.ts`; `apispec` and `apispec/specgen` both `ok`, which is `TestRouteSpecParity` passing now that the spec exists. Re-running both generators must then produce **no** further diff — that is what `api-drift` asserts:

```bash
cd "$STACK/backend" && go generate ./internal/httpd/apispec/... && cd ../frontend && npm run api:ts && cd .. && git diff --stat | tail -3
```

Expected: the same two files, same counts (idempotent).

- [ ] **Step 6: Full verification and commit**

```bash
cd "$STACK/backend" && go build ./... && go vet ./internal/httpd/... && go test ./internal/httpd/... 2>&1 | grep -E "FAIL|ok .*(controllers|apispec|httpd)$" | head
cd "$STACK/frontend" && node_modules/.bin/tsc --noEmit && echo TSC_OK
cd "$STACK"
git add backend/internal/httpd/controllers/fs.go backend/internal/httpd/controllers/fs_test.go \
  backend/internal/httpd/controllers/dto.go backend/internal/httpd/api.go \
  backend/internal/httpd/apispec/specgen/build.go backend/internal/httpd/apispec/openapi.yaml \
  backend/internal/httpd/lan_listener_test.go frontend/src/api/schema.ts
git diff --cached --name-only | xargs grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 means clean)"
git commit -q -m "feat(daemon): read-only directory listing at GET /api/v1/fs/dirs

A client connected to a daemon on another machine has no way to see that
machine's filesystem, so a project path there has to be typed blind. This
answers with the subdirectories of one absolute path: names, paths, and
whether each carries a .git entry (a directory for a clone, a file for a
worktree checkout). Dotted names are skipped, files are not listed, and a
listing is capped at 500 entries with truncated set.

It reads nothing but directory names, and it sits behind the same
connection credential that already authorises spawning a shell on that
host, so it grants no reach that credential did not already have. The
daemon judges the path by its own OS's rules — a remote client cannot
know what a valid absolute path looks like over there.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
git push -q -u origin up-a7a-fs-dirs
git rev-parse --short HEAD
```

Expected: no `FAIL`; `TSC_OK`; scrub `exit=1`; push succeeds; record the SHA.

---

### Task 4: A7b — `feat(projects): browse a remote host's folders when adding a project`

Branch `up-a7b-folder-picker` from an integration merge of `up-a6-host-ui` + `up-a7a-fs-dirs`, tagged `up-a7b-base`. Ports fork #69 plus #71's `response-validation.ts`. Opens upstream last of the three, after both parents have merged.

**Files:**
- Create: `frontend/src/renderer/components/RemoteFolderPicker.tsx`, `RemoteFolderPicker.test.tsx`
- Create: `frontend/src/renderer/lib/response-validation.ts`
- Create: `frontend/src/renderer/components/CreateProjectFlow.remote.test.tsx`
- Modify: `frontend/src/renderer/components/CreateProjectFlow.tsx` (the remote-path field's row, from Task 2)
- Modify: `frontend/src/renderer/i18n/{de,en,es,fr,ja,ko,pt-BR,zh-CN}.json` (11 `fsBrowse.*` keys each)

**Interfaces:**
- Consumes: `remotesBridge()` and the `remoteHost` selection (A6), `components["schemas"]["ListDirsResponse"]` / `["FSEntry"]` (A7a), `daemonErrorMessage` (A6).
- Produces: `RemoteFolderPicker` and `parseResponseArray` per the File structure section.

- [ ] **Step 1: Cut the integration base, bring both tests, watch them fail**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
export W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-100
export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
cd "$STACK" && git checkout -q -b up-a7b-folder-picker up-a6-host-ui
git merge -q --no-edit up-a7a-fs-dirs
git tag -f up-a7b-base
git status --porcelain | head -3
git -C "$W" show origin/develop:frontend/src/renderer/components/RemoteFolderPicker.test.tsx > frontend/src/renderer/components/RemoteFolderPicker.test.tsx
git -C "$W" show origin/develop:frontend/src/renderer/components/CreateProjectFlow.remote.test.tsx > frontend/src/renderer/components/CreateProjectFlow.remote.test.tsx
cd frontend && node_modules/.bin/vitest run --config vite.renderer.config.ts \
  src/renderer/components/RemoteFolderPicker.test.tsx src/renderer/components/CreateProjectFlow.remote.test.tsx 2>&1 | grep -E "Error|Tests |Test Files" | head -4
```

The merge is clean by construction — A6 is renderer + i18n + product-ui, A7a is `backend/` plus the generated `frontend/src/api/schema.ts`, and they share no file. A conflict here means Task 2 or Task 3 strayed outside its file list; `git status --porcelain` printing nothing confirms it. `up-a7b-base` is what the hand-off's rebase recipe pivots on after both parents merge upstream.

Expected: `RemoteFolderPicker.test.tsx` fails to resolve `./RemoteFolderPicker`; `CreateProjectFlow.remote.test.tsx` collects and fails on the missing Browse button (its other seven cases — remote path field, remote registration, workspace flag, the daemon's own rejection, skipping the local git preflight — already pass on the A6 half of the base, which is the point: A7b adds browsing to a flow that already works typed).

- [ ] **Step 2: Port `response-validation.ts` and `RemoteFolderPicker.tsx` verbatim**

```bash
cd "$STACK"
git -C "$W" show origin/develop:frontend/src/renderer/lib/response-validation.ts > frontend/src/renderer/lib/response-validation.ts
git -C "$W" show origin/develop:frontend/src/renderer/components/RemoteFolderPicker.tsx > frontend/src/renderer/components/RemoteFolderPicker.tsx
grep -n "host-telemetry\|ponytail:" frontend/src/renderer/components/RemoteFolderPicker.tsx ; echo "clean exit=$? (1 means clean)"
```

Both port unchanged: neither imports telemetry, and `RemoteFolderPicker`'s only cross-wave dependency is the `schema.ts` types the merge just supplied.

- [ ] **Step 3: Add the Browse button to the remote-path row**

In `CreateProjectFolderDialog`, add the two pieces of state and the wrapper. Beside `const remotePathId = useId();` (added in Task 2):

```tsx
	const [browseOpen, setBrowseOpen] = useState(false);
```

and wrap Task 2's bare `<input>` in a row with the button, mounting the picker below the hint:

```tsx
							{/* Browse is an assist, not a replacement: a typed path still wins,
							    and it is the only way in when the host refuses to list. */}
							<div className="flex items-center gap-2">
								<input
									id={remotePathId}
									autoComplete="off"
									spellCheck={false}
									className="settings-field-control h-(--size-settings-action-height) min-w-0 flex-1 font-mono"
									disabled={disabled}
									value={remotePath}
									onChange={(event) => onRemotePathChange(event.target.value)}
								/>
								<Button type="button" variant="footer" disabled={disabled} onClick={() => setBrowseOpen(true)}>
									{t("fsBrowse.browse")}
								</Button>
							</div>
							<p className="text-[12px] text-[var(--color-text-import-muted)]">{t("hosts.remotePathHint")}</p>
							<RemoteFolderPicker
								hostLabel={remoteHost.label}
								hostUrl={remoteHost.url}
								open={browseOpen}
								onOpenChange={setBrowseOpen}
								onSelect={onRemotePathChange}
							/>
```

and add the import beside the other component imports: `import { RemoteFolderPicker } from "./RemoteFolderPicker";`.

- [ ] **Step 4: Add the 11 `fsBrowse.*` keys to all eight locales**

```bash
cd "$STACK"
cat > /tmp/a7b-keys.txt <<'EOF'
fsBrowse.title
fsBrowse.hint
fsBrowse.loading
fsBrowse.up
fsBrowse.gitRepo
fsBrowse.chooseThis
fsBrowse.empty
fsBrowse.truncated
fsBrowse.failed
fsBrowse.unsupported
fsBrowse.browse
EOF
for l in de en es fr ja ko pt-BR zh-CN; do
  git -C "$W" show origin/develop:frontend/src/renderer/i18n/$l.json \
    | grep -F -f <(sed 's/^/\t"/;s/$/":/' /tmp/a7b-keys.txt) > /tmp/a7b-$l.txt
  echo "$l $(wc -l < /tmp/a7b-$l.txt)"
done
```

Expected: `11` for every locale. Splice each block in after the `hosts.*` block Task 2 added, then re-validate the JSON:

```bash
cd "$STACK/frontend"
for l in de en es fr ja ko pt-BR zh-CN; do node -e "JSON.parse(require('fs').readFileSync('src/renderer/i18n/$l.json','utf8'));console.log('$l ok')"; done
```

- [ ] **Step 5: Verify green**

```bash
cd "$STACK/frontend"
node_modules/.bin/vitest run --config vite.renderer.config.ts \
  src/renderer/components/RemoteFolderPicker.test.tsx src/renderer/components/CreateProjectFlow.remote.test.tsx \
  src/renderer/components/CreateProjectFlowHosts.test.tsx src/renderer/components/CreateProjectFlow.test.tsx \
  src/renderer/i18n/instance.test.ts 2>&1 | grep -E "×|Test Files|Tests "
node_modules/.bin/tsc --noEmit && echo TSC_OK
node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json && echo E2E_TSC_OK
cd "$STACK/backend" && go test ./internal/httpd/controllers/ -run TestListDirs 2>&1 | tail -2
```

Expected: `Test Files  5 passed (5)`, `TSC_OK`, `E2E_TSC_OK`, and the Go side still `ok` (the merge carried A7a intact).

The nine `RemoteFolderPicker` cases pin the three failures this dialog is defined by: a 200 whose body is not a listing (an older daemon whose web-UI catch-all answers any route with an HTML page) reports a version gap rather than "no subfolders"; a refused directory keeps the last good listing on screen instead of blanking; and stepping into a folder moves focus into the new list, since the row that had focus stops existing.

- [ ] **Step 6: Scrub and commit**

```bash
cd "$STACK"
git add frontend/src/renderer/components/RemoteFolderPicker.tsx frontend/src/renderer/components/RemoteFolderPicker.test.tsx \
  frontend/src/renderer/components/CreateProjectFlow.tsx frontend/src/renderer/components/CreateProjectFlow.remote.test.tsx \
  frontend/src/renderer/lib/response-validation.ts \
  frontend/src/renderer/i18n/de.json frontend/src/renderer/i18n/en.json frontend/src/renderer/i18n/es.json frontend/src/renderer/i18n/fr.json \
  frontend/src/renderer/i18n/ja.json frontend/src/renderer/i18n/ko.json frontend/src/renderer/i18n/pt-BR.json frontend/src/renderer/i18n/zh-CN.json
git diff --cached --name-only | xargs grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 means clean)"
git commit -q -m "feat(projects): browse a remote host's folders when adding a project

Browse beside the remote path field walks the selected host's directories
over GET /api/v1/fs/dirs and drops the chosen one into the field. Every
path decision stays with the daemon: this dialog never joins, normalises
or judges a path, because it may be looking at a different OS than the
one it runs on. A typed path still wins, and is still the way in when the
host will not list.

A 200 is not proof of a listing — a daemon predating the endpoint answers
unknown routes with an HTML page from its web-UI catch-all — so the body
is shape-checked once at the parse boundary and an unreadable answer is
reported as a version gap, not as an empty folder.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
git push -q -u origin up-a7b-folder-picker
git rev-parse --short HEAD
```

Expected: scrub `exit=1`; push succeeds; record the SHA.

---

### Task 5: Hand-off — extend the runbook and add three PR bodies

Runs in the AO worktree on `plan/2026-08-24-hostui`, the branch this plan's PR already tracks. Extends `docs/upstreaming-stack-status.md` from five branches to eight.

**Files:**
- Modify: `docs/upstreaming-stack-status.md`
- Create: `docs/upstreaming-pr-bodies/a6-host-ui.md`, `a7a-fs-dirs.md`, `a7b-folder-picker.md`

- [ ] **Step 1: Extend the runbook**

In `docs/upstreaming-stack-status.md`, make five edits.

Its opening line gains Plan 2: `Built by Plan 1 (…) and Plan 2 (docs/superpowers/plans/2026-08-24-upstream-host-ui-fs.md) against upstream/main @ 6cba6344c`.

**Branch topology** gains a paragraph:

> A6 builds on A5 and opens after it. A7a is cut independently from `upstream/main` — it is Go plus two generated files and shares no code with anything else in the stack, so it can be opened and merged at any point. A7b builds on a local integration merge of A6 + A7a (tag `up-a7b-base` in the stack worktree) and opens last of the three.

**Order of operations** gains steps 5 and 6:

> 5. Open A6 after A5 is squash-merged. A7a may be opened at any time — including alongside A1–A5, since it touches only `backend/` and the two generated API files.
> 6. Open A7b after both A6 and A7a have merged.

and its rebase-recipe block gains:

```
    # A6, after A5 merges:
    git rebase --onto upstream/main up-a5-clients up-a6-host-ui
    git push --force-with-lease origin up-a6-host-ui

    # A7a while waiting — plain drift, it sits on upstream/main:
    git rebase upstream/main up-a7a-fs-dirs
    git push --force-with-lease origin up-a7a-fs-dirs

    # A7b, after A6 + A7a merge:
    git rebase --onto upstream/main up-a7b-base up-a7b-folder-picker
    git push --force-with-lease origin up-a7b-folder-picker
```

**The branches** table gains three rows, with the SHAs recorded in Tasks 2–4:

| # | Branch (on `origin`) | SHA | Base | Upstream title | Non-test files | Tests it carries |
| --- | --- | --- | --- | --- | --- | --- |
| 6 | `up-a6-host-ui` | *(Task 2)* | `up-a5-clients` | feat(hosts): add, edit and remove remote hosts | 14 | useRemoteHosts ×5, HostSelect ×12, AddRemoteHostDialog ×21, CreateProjectFlowHosts ×6 |
| 7 | `up-a7a-fs-dirs` | *(Task 3)* | `upstream/main` | feat(daemon): read-only directory listing at GET /api/v1/fs/dirs | 6 | fs ×4 (Go), LAN policy ×1 |
| 8 | `up-a7b-folder-picker` | *(Task 4)* | merge(A6, A7a), tag `up-a7b-base` | feat(projects): browse a remote host's folders when adding a project | 11 | RemoteFolderPicker ×9, CreateProjectFlow.remote ×8 |

with a note after the table: *A7a is the only branch in the stack that touches Go or the OpenAPI surface, so it is the only one the `go` and `api-drift` CI jobs judge. `npm run api` is idempotent on it — verified at build time.*

**What a reviewer can verify with the flag off** gains two bullets:

> - The Add-a-project flow renders exactly upstream's tree: no Host row, `remotes.list`/`remotes.probe` never called (`CreateProjectFlowHosts.test.tsx` "shows no host picker and contacts no saved host", A6).
> - `GET /api/v1/fs/dirs` is additive and behind the existing connection credential; nothing calls it until a remote host is selected, which the flag gates (A7a/A7b).

Finally add a short section recording what wave 2 deliberately left out, so Plan 3 does not rediscover it:

> ## Deferred out of wave 2
>
> - `HostSwitcher.tsx` and its `hosts.viewing` / `hosts.allHosts` keys — it imports `HostSection` from `types/workspace.ts`, which A8a introduces. Ships with A10.
> - `lib/host-telemetry.ts` and the two `reportHostConnect` call sites in `useRemoteHosts.ts` / `AddRemoteHostDialog.tsx` — telemetry is A10, and `host_id` is not on upstream's renderer allowlist yet.
> - Twelve further `hosts.*` keys belonging to the one-tree UI (`hosts.backToLocal`, `hosts.passwordChanged`, `hosts.on`, `hosts.remoteSection`, `hosts.open`, `hosts.unreachable`, `hosts.peekEmpty`, `hosts.qualified`, `hosts.sectionFailed`, `hosts.retry`, `hosts.liveUpdatesOffline`, `hosts.liveUpdatesOffline.hint`).

- [ ] **Step 2: Write the three PR bodies**

Same shape as `a1-flag.md` … `a5-clients.md`: **What** is the first paragraph of that branch's commit message, **How** the rest, **Why** links the RFC and states the dark-landing, **Testing** names the exact command, **Checklist** is upstream's five boxes. Take the text with `git -C "$STACK" log -1 --format=%b <branch>`.

`docs/upstreaming-pr-bodies/a6-host-ui.md` — its **Why** adds: *Opens after the flag, the host primitives, the store, the proxy and the per-host clients have merged; it is the first PR where the feature is reachable by a user, and still only with the flag on.* Its **Testing**: `cd frontend && npm run typecheck && npx vitest run src/renderer/hooks/useRemoteHosts.test.tsx src/renderer/components/HostSelect.test.tsx src/renderer/components/AddRemoteHostDialog.test.tsx src/renderer/components/CreateProjectFlowHosts.test.tsx` plus `npm --prefix packages/product-ui test`. State that no Go or OpenAPI surface is touched. Add a line inviting the split §2.4 offers: *If 14 files is too much for one review, add/edit and remove split cleanly along `AddRemoteHostDialog` / `ConfirmDialog` — say the word.*

`docs/upstreaming-pr-bodies/a7a-fs-dirs.md` — its **Why** must answer the escalation question head-on, since that is the objection §2.4 predicts: *It sits behind the same connection credential that already authorises spawning a shell on the host, so it grants no reach that credential did not already have; it returns directory names only, skips dotted names, and caps a listing at 500.* Its **Testing**: `cd backend && go test ./internal/httpd/...` and `npm run api` producing no diff. Note that this is the stack's only Go PR and can be reviewed and merged independently of every other one.

`docs/upstreaming-pr-bodies/a7b-folder-picker.md` — its **Why** notes it opens after both parents. Its **Testing**: `cd frontend && npm run typecheck && npx vitest run src/renderer/components/RemoteFolderPicker.test.tsx src/renderer/components/CreateProjectFlow.remote.test.tsx`.

Each body's **Why** links the RFC issue number once it exists — leave `#RFC` for the human to fill, exactly as the five existing bodies do.

- [ ] **Step 3: Scrub, commit, push**

```bash
cd /Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-100
grep -rnE "amongstar|/Users/" docs/upstreaming-pr-bodies/ ; echo "bodies scrub exit=$? (1 means clean — the status doc may name local paths, the bodies may not)"
git add docs/upstreaming-stack-status.md docs/upstreaming-pr-bodies/
git commit -q -m "docs: hand-off for the upstream remote-hosts host-UI wave

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
git push -q origin plan/2026-08-24-hostui
```

Expected: push succeeds; the plan's PR updates in place. Report the PR link, the three branch names and SHAs, and the upstream SHA the stack is based on.

---

## Self-review

**Spec coverage.** §2.4 A6 → Task 2 (fork #65/#72/#106 plus #68's host choice, per deviation 3); §2.4 A7a → Task 3 (fork #66, all six files plus the LAN-policy assertion §2.4 names as a test); §2.4 A7b → Task 4 (fork #69 plus #71's `response-validation.ts`, which Plan 1 deferred here); §2.3's edges A5→A6, A6→A7b, A7a→A7b → the three bases in the File structure table, each justified by an import that does not resolve otherwise; §2.1 rule 1 (branch from `upstream/main`) → A7a directly, A6/A7b transitively through the Plan 1 chain, with the `--onto` recipe in Task 5 keeping it true after each squash-merge; §2.1 rule 2 (dark behind the flag) → the `remoteHostsEnabled` gate on `hostRow` and the flag-off case in `CreateProjectFlowHosts.test.tsx`; §2.1 rule 3 (≤15 non-test files) → 14 / 6 / 11; §3.3 scrub → every commit step, and specifically the `ponytail:` comment `develop` carries in `CreateProjectFlow.tsx`; §3.3 i18n-all-eight-locales → Steps 2.5 and 4.4 with a per-locale count check and a JSON re-parse.

**Where the spec did not survive contact.** Four deviations, listed with their evidence at the top: `HostSwitcher` cannot compile before A8a; `host-telemetry` belongs to A10 and its property is not on upstream's allowlist; §2.4's A6/A7b line would have shipped a host dropdown that registers on the wrong machine; `response-validation.ts` follows its only caller. None of them is a missing fork commit or a §1.4 entanglement hazard — every file §2.4 names exists on `origin/develop @ 32d089f37` and was read before this plan was written.

**Placeholder scan.** Every step carries either the exact file to copy (`git show origin/develop:<path>`) or the literal code to write. The three places where a file cannot be copied — `CreateProjectFlow.tsx`, `ProjectViews.tsx`, `dto.go`, all reformatted or heavily changed on `develop` — are written out hunk by hunk against verified upstream line anchors, with the expected `git diff --stat` that proves nothing else leaked in. The only templated spot is the PR bodies taking What/How from the commit messages, and Task 5 names the `git log` command that produces the text.

**Type consistency.** `RemoteHostView {label,url}` is what `remotes:list` returns (A3), what `useRemoteHosts` maps into `Host`, what `HostSelect`'s `onEditHost`/`onRemoveHost` emit, and what `AddRemoteHostDialog`'s `host` prop accepts — one shape, four hops, no password anywhere on it. `Host {id,label,url,status}` is the picker's row; its `id` is the saved url for a remote and `LOCAL_HOST_ID` for local, which is why `remoteHost` is found by `host.id === hostId && host.url !== null` rather than by an `isLocal` check. `CreateProjectFolderDialog`'s `remoteHost` prop is structurally `{label, url}` — narrower than `Host` on purpose, so the dialog cannot read a status it has no business rendering. `ListDirsResponse`/`FSEntry` are declared once in `dto.go` (Task 3) and reach `RemoteFolderPicker` (Task 4) only through generated `schema.ts`, never hand-mirrored.

**What could still go wrong, and where it surfaces.** Upstream moving past `6cba6344c` invalidates the whole topology — Task 1 Step 1 stops on it. A `CreateProjectFlow.tsx` hunk landing at the wrong anchor shows up as a failing upstream `CreateProjectFlow.test.tsx` in Task 2 Step 6, which is why that suite is in the run list. A route mounted outside the LAN-served group shows up as a 404 in Task 3 Step 4. A dirty octopus merge shows up as a non-empty `git status --porcelain` in Task 4 Step 1. An i18n splice error shows up as a `SyntaxError` from the per-locale `JSON.parse`, before `tsc` ever runs.
