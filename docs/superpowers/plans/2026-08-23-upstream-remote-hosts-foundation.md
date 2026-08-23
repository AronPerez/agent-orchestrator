# Upstream Remote Hosts — Plan 1: Flag + Main-Process Foundation (A0–A5)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build, on top of `upstream/main`, the first five upstream-ready branches of the remote-hosts stack (flag → host primitives → saved-host store → loopback proxy → per-host clients), each one green and dark behind the flag, plus the RFC issue text and the hand-off the human needs to open them upstream one at a time.

**Architecture:** Every branch is a port of code that already ships on our `develop`, re-cut against `upstream/main` in the order the spec's dependency graph dictates, with the spec's off-state semantics pinned by tests and the ponytail-audit cuts applied where they shrink the upstream diff. The RFC is written first — maintainer reaction is the longest pole in the whole effort, so its clock starts before any branch is built. The branches follow the code's real dependency DAG rather than a chain: `up-a1-flag`, `up-a2-hosts` and `up-a3-store` are cut independently from `upstream/main` (they share no code and can merge upstream in any order), `up-a4-proxy` builds on the store, and `up-a5-clients` builds on a tagged integration merge of the other three sides. Everything lives in a dedicated worktree and is pushed to our fork after every task. Nothing is opened against upstream: the final task writes the exact `gh pr create` commands for the human.

**Tech Stack:** Electron main (`node:http`, `node:net`, `node:tls`, `node:crypto`, `node:fs/promises`), React 19 renderer, zustand (`ui-store`), `openapi-fetch`, Vitest 4 (jsdom for renderer, node for `src/main`), TypeScript `tsc --noEmit`.

**Spec:** `docs/upstreaming-remote-sessions.md` (merged, #123). This plan implements §2.2 (the flag), §2.4 A0–A5, and the §3.3 scrub list. Plans 2 (A6–A7b), 3 (A8a–A11) and C (CLI) follow separately.

## Global Constraints

- **Do not push to, open PRs against, or comment on `Untrivial-ai/agent-orchestrator`.** All pushes go to `origin` (`AronPerez/agent-orchestrator`). The human opens upstream PRs from Task 8's hand-off.
- **Every stack branch is based on `upstream/main`** (spec §2.1 rule 1), never on `develop`. Upstream squash-merges; rebase the chain with `git rebase --onto`, never by merging.
- **Flag off ⇒ zero remote network** (spec §2.2): no `remotes.json` read, no probe, no proxy listener, no `EventSource` beyond the local one. Off is a network boundary, not a visibility toggle.
- **No secrets cross to the renderer**: only `{label, url}` and the loopback base ever leave main; the proxy never logs `req.url` (its first segment is the token) nor `entry.password`.
- **Upstream conventions** (`AGENTS.md`): surgical changes, no drive-by cleanup, conventional commits (`feat:`/`fix:`/`test:`/`docs:`), tabs in `.ts/.tsx`, every `settings.*`/`hosts.*` i18n key present in all eight locales (`de en es fr ja ko pt-BR zh-CN` — `instance.test.ts` fails otherwise), ≤15 non-test files per PR.
- **Scrub before every commit** (spec §3.3): `grep -rnE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:"` over the branch's changed files must print nothing.
- **Test commands** (from `$STACK/frontend`): unit `node_modules/.bin/vitest run --config vite.renderer.config.ts <files>`; typecheck `node_modules/.bin/tsc --noEmit`; e2e typecheck `node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json`. Node is off-PATH: `export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"`.
- **Fork source refs:** the flag lives at commit `b06520893` (parent `910f959db`, branch `ao/agent-orchestrator-96/remote-hosts-flag`, PR #125); everything else at `origin/develop`. If #125 has been squash-merged and the branch deleted, the same file contents are on `origin/develop`.
- **Deviations from the spec, decided here:** `response-validation.ts` moves from A5 to Plan 2 (it has no caller until `RemoteFolderPicker`/`useWorkspaceQuery`); `applyDaemonBaseUrl` and `syncConnectedHosts` are not ported (ponytail-audit items 4–5: no production caller); the registry's optional `startRuntime` hook is not ported (it belongs to D1, browser remote sessions); the ipc layer takes a `disconnect(url)` callback instead of a `{view, deactivate}` handle (audit item 2); and per the architectural review (2026-08-23) the RFC precedes the branches and A1/A2/A3 are cut as independent siblings, since maintainer latency is the long pole and the three roots share no code. The proxy is ported **verbatim** — the audit's "use `http.request` for the upgrade" item was withdrawn: Node's client parser would consume the upstream 101, so the 101 would have to be hand-built for the renderer instead; nothing gets simpler.

---

## File structure

Worktree `$STACK` = `/Users/amongstar/dev/agent-orchestrator-up-stack` (created in Task 1, sibling of the main checkout, not under AO's managed worktree dir). Paths below are relative to `$STACK`.

| Branch | Base | Creates | Modifies | Responsibility |
| --- | --- | --- | --- | --- |
| `up-a1-flag` | `upstream/main` | `frontend/src/renderer/stores/ui-store.test.ts` | `stores/ui-store.ts`, `components/settings/GeneralSettingsSection.tsx`, `components/GlobalSettingsForm.test.tsx`, `i18n/*.json` ×8 | `remoteHosts` flag, persisted at `ao.remoteHosts`; the switch under Developer Mode. Nothing reads it yet. |
| `up-a2-hosts` | `upstream/main` | `frontend/src/renderer/lib/hosts.ts`, `hosts.test.ts` | — | `HostId`, `Ref`, `LOCAL_HOST`, `isLocal`, `refKey`/`parseRefKey`. Pure. |
| `up-a3-store` | `upstream/main` | `frontend/src/main/remotes-store.ts`, `remote-request.ts`, `remotes-ipc.ts`, `remotes-main.ts` (+ tests) | `frontend/src/preload.ts`, `frontend/src/main.ts`, `frontend/src/renderer/test/setup.ts`, `frontend/src/renderer/lib/bridge.ts`, `frontend/e2e/support/fake-bridge.ts` | `~/.ao/remotes.json` (0600) read/write, Bearer-injected request + probe, password-free views, the six list/add/update/remove/probe/request IPC handlers. |
| `up-a4-proxy` | `up-a3-store` | `frontend/src/main/remote-proxy.ts`, `remote-registry.ts` (+ tests) | `remotes-main.ts`, `preload.ts`, `main.ts`, the three bridge stubs | Token-gated loopback proxy per host; N live proxies; connect/disconnect/connected IPC; teardown on quit. |
| `up-a5-clients` | merge(A1, A2, A4), tag `up-a5-base` | `frontend/src/renderer/lib/host-clients.ts`, `active-host.ts` (+ tests) | `frontend/src/renderer/main.tsx` | `clientFor(host)`, connected-host registry, flag-gated `initHosts()` with live toggle. First consumer of the flag. |
| (develop) | `origin/develop` | `docs/upstreaming-rfc-remote-hosts.md`, `docs/upstreaming-stack-status.md` | — | RFC issue text and the hand-off: per-branch PR body + the exact commands the human runs. |

Module boundaries, fixed here so tasks agree on names:

- `remotes-store.ts` — `RemoteEntry {label,url,password}`, `readRemotes(path)`, `addRemote(path, entry)`, `updateRemote(path, url, changes)`, `removeRemote(path, url)`, `applyRemoteChanges(entry, changes)`, `RemoteChanges = Partial<RemoteEntry>`, `RemotesFilePermissionError`.
- `remote-request.ts` — `remoteRequest(entry, init, fetchImpl?, signal?) → {status, body}`, `probeRemote(entry, fetchImpl?, timeoutMs?) → RemoteHealth`, types `RemoteRequestInit {method,path,body?}`, `RemoteResponse`, `RemoteHealth = "online"|"unauthorized"|"offline"|"not-a-daemon"`.
- `remotes-ipc.ts` — `RemoteHostView {label,url}`, `toHostViews(entries)`, `findRemote(path, url)`, `updateSavedRemote(path, url, changes, disconnect, probe?)`, `removeSavedRemote(path, url, disconnect)`. `disconnect: (url: string) => Promise<void>`.
- `remotes-main.ts` — `remotesFilePath()`, `registerRemotesIpc(ipcMain, deps)`. Task 5: `deps = { file, disconnect, probe? }`; Task 6 replaces with `deps = { file, registry, probe? }`.
- `remote-registry.ts` — `ConnectedHostView {label,url,base}`, `class RemoteRegistry { constructor(start: (entry) => Promise<ActiveProxy>); connect(entry); disconnect(url); views(); closeAll() }`.
- `remote-proxy.ts` — `ActiveProxy {base,url,close}`, `startRemoteProxy(entry) → Promise<ActiveProxy>`.
- `host-clients.ts` — `registerHostBase(host, base, label?)`, `forgetHost(host)`, `connectedHosts()`, `subscribeConnectedHosts(listener)`, `hostLabelFor(host)`, `baseUrlFor(host)`, `isHostReady(host)`, `clientFor(host)`, `connectHost(url)`, `disconnectHost(url)`.
- `active-host.ts` — `initHosts()`.

---

### Task 1: Stack worktree and baseline

**Files:**
- Create: worktree `$STACK` at `upstream/main`
- Create: `$STACK/frontend/node_modules` (symlink, untracked — never stage it)

- [ ] **Step 1: Fetch upstream and create the worktree (detached — each task cuts its own branch)**

Run from this worktree (`/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-96`):

```bash
git fetch upstream --quiet && git fetch origin --quiet
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
git worktree add --detach "$STACK" upstream/main
git -C "$STACK" log -1 --format='%h %ad %s' --date=short
```

Expected: the last line prints upstream's tip (`3cf4df384 2026-08-23 fix: move Queue/Steer chips …` or newer). If newer, note the SHA in the hand-off (Task 8).

- [ ] **Step 2: Give the worktree a toolchain**

```bash
export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
ln -s /Users/amongstar/dev/agent-orchestrator/frontend/node_modules "$STACK/frontend/node_modules"
npm ci --prefix "$STACK/packages/product-ui" --silent
git -C "$STACK" check-ignore -q frontend/node_modules || echo "node_modules symlink is NOT ignored — stage by path only, never git add -A"
```

Expected: the warning line prints (the `.gitignore` pattern `node_modules/` does not match a symlink). Product-ui's install exists so `tsc` does not report its missing deps.

- [ ] **Step 3: Baseline — prove the upstream tree is green before touching it**

```bash
cd "$STACK/frontend"
node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/components/GlobalSettingsForm.test.tsx src/renderer/i18n/instance.test.ts 2>&1 | grep -E "×|Test Files|Tests "
node_modules/.bin/tsc --noEmit && echo TSC_OK
```

Expected: `Test Files  2 passed`, `TSC_OK`. If `tsc` fails here the environment is wrong — fix it before any task; nothing in this plan has touched code yet.

---

### Task 2: A0, part 1 — the RFC text, first (lands on `develop`)

The RFC is the highest-latency dependency in the whole effort — a maintainer reaction gates every upstream PR, and spec §4 Q1 decides whether the multi-host half proceeds as designed — so it is produced before any branch is built and handed to the human immediately. Runs in the AO worktree, not `$STACK`. Nothing is posted upstream: the human posts it.

**Files:**
- Create: `docs/upstreaming-rfc-remote-hosts.md`

- [ ] **Step 1: Branch off develop in the AO worktree**

```bash
cd /Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-96
git fetch origin --quiet && git checkout -q -b ao/agent-orchestrator-96/upstream-handoff origin/develop
```

- [ ] **Step 2: Write the RFC issue body**

Create `docs/upstreaming-rfc-remote-hosts.md` with exactly this content (the human pastes it into a new issue on `Untrivial-ai/agent-orchestrator`, title *RFC: connect the desktop app to AO daemons on other machines (multi-host, behind a flag)*):

```markdown
## Problem

Agents are long-running and machine-bound. With a laptop and a desktop (or a VM), the only ways to see both today are SSH or pointing the whole app at one host at a time. #3853 and discussion #2855 ask for this; #3883, #4084 and #4309 each propose a slice of it.

## Proposal

Let one desktop app connect to **N** AO daemons at once — the local one plus saved remote hosts — and show every host's projects and sessions in one tree, each remote row labelled with its host. Open, watch and type into a session on any host without a mode switch.

**Mechanism (renderer-side fan-out, per-host loopback proxy):**

- Remote hosts reuse the existing opt-in LAN listener and its connection password (ADR 0001). **No daemon change is required**: the listener already accepts `Authorization: Bearer`.
- The renderer cannot set that header on `EventSource`/`WebSocket`, and `app://renderer` has no CORS standing with a remote daemon, so Electron **main** runs one loopback proxy per host: `127.0.0.1:<ephemeral>/<128-bit token>/…`, token stripped before forwarding, Bearer injected, renderer `Origin` stripped, SSE/WebSocket streamed. No token ⇒ 404 and nothing forwarded. Torn down on disconnect and on quit.
- `~/.ao/remotes.json` (mode 0600, refused if looser) is the saved-host store, shared verbatim with `ao --url`. The password never enters the renderer process.
- Every addressable thing becomes a `Ref = {host, id}`; ids are never rewritten. This is load-bearing: a project id is `filepath.Base(path)` on every machine, so bare ids collide by construction.
- Hosts connect after first paint; a sleeping host is a labelled failed section with a retry, never a blank tree.
- **Everything ships dark behind a `Remote hosts (experimental)` switch** in Settings, modelled on Developer Mode, default off. Off means no saved host is read, probed or connected — a reviewer can verify the off state from the network side.

**Trust boundary:** one operator, machines they own, a trusted network (LAN or Tailscale). Plaintext HTTP on the LAN path is unchanged from Connect Mobile; https upstreams use TLS; an `ssh -N -L` recipe and `"bind": "127.0.0.1"` take the port off the network entirely.

**Already built and verified** on two real machines in a fork, with a security review of the proxy/token/credential path (four fixes folded in) and an accessibility pass. ~2,400 tests.

## Proposed PR series (each independently mergeable, each dark behind the flag)

1. `feat(settings)`: the `remoteHosts` flag (~60 lines)
2. `feat(hosts)`: `HostId`/`Ref` primitives (30 lines)
3. `feat(remotes)`: saved-host store, authenticated request, password-free IPC
4. `feat(remotes)`: token-gated loopback proxy + registry — *requests a security reviewer*
5. `feat(hosts)`: per-host clients + flag-gated boot
6. `feat(hosts)`: add/edit/remove hosts UI
7. `feat(daemon)`: `GET /api/v1/fs/dirs` (read-only, names only, capped) + remote folder picker
8. `refactor(hosts)`: thread `Ref` through reads; host-qualified routes `/host/$hostId/…`
9. `feat(hosts)`: per-host workspace queries, SSE and terminals
10. `refactor(hosts)`: route writes by `Ref`
11. `feat(hosts)`: one tree across hosts, telemetry, hostile-daemon tests
12. `docs`: setup, trust boundary, ADR 0003

## Questions for maintainers

1. Which remote model do you want — one active workspace over SSH (#3883), one active remote over Tailscale HTTPS (#4084), or N hosts at once (this)? This decides the back half of the series.
2. Is a loopback proxy with a path-borne per-activation token acceptable as a standing mechanism? (Header injection via `webRequest` and CORS negotiation were rejected for concrete reasons — happy to write them up.)
3. Flag placement: sibling of Developer Mode (proposed) or nested under it?
4. `remotes.json` as the store shared with the CLI: accept the 0600 file, or require `safeStorage` for the app at the cost of forking the store?
5. `GET /api/v1/fs/dirs`: acceptable as an authenticated read-only endpoint, or absolute-path entry only?
6. Any objection to the `/host/$hostId/…` URL shape (user-visible, permanent)?
7. Relationship to #4309 (browser client): if it merges we contribute our rebinding/credential-gating tests to it rather than a competing design.
8. Can a maintainer own review for ~6 weeks, with a quiet window for the two mechanical `Ref` PRs?
```

- [ ] **Step 3: Scrub, commit, push, and open the PR into develop**

```bash
cd /Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-96
grep -rnE "amongstar|/Users/" docs/upstreaming-rfc-remote-hosts.md ; echo "rfc scrub exit=$? (1 means clean)"
git add docs/upstreaming-rfc-remote-hosts.md
git commit -q -m "docs: RFC text for the upstream remote-hosts stack

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HnqdDyvae5s7L7KYwtKPd7"
git push -q -u origin ao/agent-orchestrator-96/upstream-handoff
gh pr create --repo AronPerez/agent-orchestrator --base develop --head ao/agent-orchestrator-96/upstream-handoff \
  --title "docs: RFC text and hand-off for the upstream remote-hosts stack" \
  --body "RFC body ready to post on Untrivial-ai/agent-orchestrator once approved — posting it starts the maintainer clock while Tasks 3-7 build the branches. The stack hand-off lands on this same branch in Task 8."
```

Expected: PR URL printed. **Report it to the human now** — they can post the RFC while the rest of this plan executes.

---

### Task 3: A1 — `feat(settings): add an experimental Remote hosts flag`

Branch `ao/agent-orchestrator-96/up-a1-flag` from `upstream/main`. Port of fork commit `b06520893`, restricted to the files that exist upstream.

**Files:**
- Modify: `frontend/src/renderer/stores/ui-store.ts` (upstream lines 57, 94, 126, 137, 156, 184–187 are the `developerMode` lines the hunks attach to)
- Modify: `frontend/src/renderer/components/settings/GeneralSettingsSection.tsx:103-104,176-182`
- Modify: `frontend/src/renderer/i18n/{de,en,es,fr,ja,ko,pt-BR,zh-CN}.json` (one key after `settings.developerMode`)
- Create: `frontend/src/renderer/stores/ui-store.test.ts`
- Modify: `frontend/src/renderer/components/GlobalSettingsForm.test.tsx:149` (+ one new test)

**Interfaces:**
- Produces: `useUiStore.getState().remoteHosts: boolean`, `setRemoteHosts(enabled: boolean)`, localStorage key `"ao.remoteHosts"`, i18n key `settings.remoteHosts`.

- [ ] **Step 1: Bring over the tests only, and watch them fail**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-96
export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
cd "$STACK" && git checkout -q -b ao/agent-orchestrator-96/up-a1-flag upstream/main
git -C "$W" diff 910f959db b06520893 -- \
  frontend/src/renderer/stores/ui-store.test.ts \
  frontend/src/renderer/components/GlobalSettingsForm.test.tsx \
  | git apply --3way
cd frontend && node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/stores/ui-store.test.ts src/renderer/components/GlobalSettingsForm.test.tsx 2>&1 | grep -E "×|Tests "
```

Expected: `× is off until the user turns it on`, `× persists the switch …`, `× reads a stored choice back at startup`, `× offers Remote hosts as a switch right below Developer Mode and persists it`; `Tests  4 failed | … passed`. The test content, for reference (this is what the patch adds):

```ts
// frontend/src/renderer/stores/ui-store.test.ts
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "./ui-store";

beforeEach(() => {
	window.localStorage.clear();
	vi.resetModules();
});

// A fresh module sees exactly what a booting renderer sees: only storage.
async function bootStore() {
	return (await import("./ui-store")).useUiStore;
}

describe("remoteHosts flag", () => {
	it("is off until the user turns it on", async () => {
		expect((await bootStore()).getState().remoteHosts).toBe(false);
	});

	it("persists the switch so the choice survives a restart", () => {
		useUiStore.getState().setRemoteHosts(true);
		expect(useUiStore.getState().remoteHosts).toBe(true);
		expect(window.localStorage.getItem("ao.remoteHosts")).toBe("true");
		useUiStore.getState().setRemoteHosts(false);
	});

	it("reads a stored choice back at startup", async () => {
		window.localStorage.setItem("ao.remoteHosts", "true");
		expect((await bootStore()).getState().remoteHosts).toBe(true);
	});
});
```

```ts
// added to GlobalSettingsForm.test.tsx, before "shows the available feature builds …";
// beforeEach line 149 becomes: useUiStore.setState({ developerMode: false, remoteHosts: false });
	it("offers Remote hosts as a switch right below Developer Mode and persists it", async () => {
		const user = userEvent.setup();
		renderForm();
		const developerMode = await screen.findByRole("switch", { name: "Developer Mode" });
		const remoteHosts = screen.getByRole("switch", { name: "Remote hosts (experimental)" });
		expect(remoteHosts).toHaveAttribute("aria-checked", "false");
		// "Underneath Developer Mode": the next switch in document order.
		expect(developerMode.compareDocumentPosition(remoteHosts) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
		expect(screen.getAllByRole("switch").indexOf(remoteHosts)).toBe(screen.getAllByRole("switch").indexOf(developerMode) + 1);

		await user.click(remoteHosts);
		expect(window.localStorage.getItem("ao.remoteHosts")).toBe("true");
		expect(useUiStore.getState().remoteHosts).toBe(true);
	});
```

- [ ] **Step 2: Port the implementation hunks**

```bash
cd "$STACK"
git -C "$W" diff 910f959db b06520893 -- \
  frontend/src/renderer/stores/ui-store.ts \
  frontend/src/renderer/components/settings/GeneralSettingsSection.tsx \
  frontend/src/renderer/i18n/ \
  | git apply --3way
git status --porcelain
```

Expected: 12 files `M`/`A`, no `U` (unmerged). Verified 2026-08-23: `ui-store.ts` applies via 3-way fallback, everything else cleanly. What the hunks contain, so a conflict can be resolved by hand:

```ts
// ui-store.ts — four additions mirroring developerMode
	/** Experimental: connect to AO daemons on other machines. Default off; off means no remote host is ever contacted. */
	remoteHosts: boolean;
	// …in the actions block:
	setRemoteHosts: (enabled: boolean) => void;
	// …after developerModeStorageKey:
const remoteHostsStorageKey = "ao.remoteHosts";
function initialRemoteHosts() {
	return getLocalStorage()?.getItem(remoteHostsStorageKey) === "true";
}
	// …in create(): 
	remoteHosts: initialRemoteHosts(),
	setRemoteHosts: (remoteHosts) => {
		getLocalStorage()?.setItem(remoteHostsStorageKey, String(remoteHosts));
		set({ remoteHosts });
	},
```

```tsx
// GeneralSettingsSection.tsx — after the two developerMode selectors:
	const remoteHosts = useUiStore((state) => state.remoteHosts);
	const setRemoteHosts = useUiStore((state) => state.setRemoteHosts);
// …directly after the Developer Mode <SettingsRow>:
			<SettingsRow label={t("settings.remoteHosts")}>
				<Switch
					aria-label={t("settings.remoteHosts")}
					checked={remoteHosts}
					onCheckedChange={setRemoteHosts}
				/>
			</SettingsRow>
```

i18n, one line after `"settings.developerMode"` in each file: en `"Remote hosts (experimental)"`, de `"Remote-Hosts (experimentell)"`, es `"Hosts remotos (experimental)"`, fr `"Hôtes distants (expérimental)"`, ja `"リモートホスト（実験的）"`, ko `"원격 호스트 (실험적)"`, pt-BR `"Hosts remotos (experimental)"`, zh-CN `"远程主机（实验性）"`.

- [ ] **Step 3: Verify green, including locale parity and typecheck**

```bash
cd "$STACK/frontend"
node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/stores/ui-store.test.ts src/renderer/components/GlobalSettingsForm.test.tsx src/renderer/i18n/instance.test.ts 2>&1 | grep -E "×|Test Files|Tests "
node_modules/.bin/tsc --noEmit && echo TSC_OK
```

Expected: `Test Files  3 passed (3)`, `Tests  45 passed (45)` (verified on upstream/main 2026-08-23), `TSC_OK`.

- [ ] **Step 4: Scrub and commit**

```bash
cd "$STACK"
git add frontend/src/renderer/stores/ui-store.ts frontend/src/renderer/stores/ui-store.test.ts \
  frontend/src/renderer/components/settings/GeneralSettingsSection.tsx frontend/src/renderer/components/GlobalSettingsForm.test.tsx \
  frontend/src/renderer/i18n/de.json frontend/src/renderer/i18n/en.json frontend/src/renderer/i18n/es.json frontend/src/renderer/i18n/fr.json \
  frontend/src/renderer/i18n/ja.json frontend/src/renderer/i18n/ko.json frontend/src/renderer/i18n/pt-BR.json frontend/src/renderer/i18n/zh-CN.json
git diff --cached --name-only | xargs grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 means clean)"
git commit -q -m "feat(settings): add an experimental Remote hosts flag

A Remote hosts switch directly below Developer Mode, modelled on it:
remoteHosts in ui-store, persisted at ao.remoteHosts, default off.
Nothing reads the flag yet; the remote-host feature lands behind it in
the following PRs, so with it off there is no behaviour change at all.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HnqdDyvae5s7L7KYwtKPd7"
git push -q -u origin ao/agent-orchestrator-96/up-a1-flag
```

Expected: scrub prints nothing and `exit=1`; push succeeds.

---

### Task 4: A2 — `feat(hosts): host identity primitives`

Branch `ao/agent-orchestrator-96/up-a2-hosts` from `upstream/main` — it shares no code with A1 and can merge in any order relative to it.

**Files:**
- Create: `frontend/src/renderer/lib/hosts.ts`
- Create: `frontend/src/renderer/lib/hosts.test.ts`

**Interfaces:**
- Produces: `type HostId = string`, `const LOCAL_HOST: HostId = "local"`, `type Ref = { host: HostId; id: string }`, `isLocal(host)`, `refKey(ref): string`, `parseRefKey(key): Ref`.

- [ ] **Step 1: Branch**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-96
export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
cd "$STACK" && git checkout -q -b ao/agent-orchestrator-96/up-a2-hosts upstream/main
```

- [ ] **Step 2: Write the failing test**

Create `frontend/src/renderer/lib/hosts.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { isLocal, LOCAL_HOST, parseRefKey, refKey, type Ref } from "./hosts";

describe("refKey", () => {
	it("round-trips a local ref", () => {
		const ref: Ref = { host: LOCAL_HOST, id: "skyvern-cloud" };
		expect(refKey(ref)).toBe("local:skyvern-cloud");
		expect(parseRefKey(refKey(ref))).toEqual(ref);
	});

	it("round-trips a remote ref whose host url contains colons and slashes", () => {
		const ref: Ref = { host: "http://192.0.2.1:3011", id: "skyvern-cloud" };
		expect(parseRefKey(refKey(ref))).toEqual(ref);
	});

	it("round-trips an id that itself contains a colon", () => {
		// Ids come from another machine; nothing guarantees they are colon-free.
		const ref: Ref = { host: "http://192.0.2.1:3011", id: "weird:id" };
		expect(parseRefKey(refKey(ref))).toEqual(ref);
	});

	it("distinguishes the same id on two hosts", () => {
		expect(refKey({ host: LOCAL_HOST, id: "skyvern-cloud" })).not.toBe(
			refKey({ host: "http://192.0.2.1:3011", id: "skyvern-cloud" }),
		);
	});
});

describe("isLocal", () => {
	it("is true only for the local host", () => {
		expect(isLocal(LOCAL_HOST)).toBe(true);
		expect(isLocal("http://192.0.2.1:3011")).toBe(false);
	});
});
```

- [ ] **Step 3: Run it to verify it fails**

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/lib/hosts.test.ts 2>&1 | grep -E "Error|Tests " | head -3
```

Expected: `Error: Failed to resolve import "./hosts"` (or `Cannot find module`), no tests run.

- [ ] **Step 4: Write the module**

Create `frontend/src/renderer/lib/hosts.ts`:

```ts
// Host identity. Local is a host like any other — the one whose requests skip
// the proxy — so no code path has to special-case "is this remote?".
export type HostId = string;

export const LOCAL_HOST: HostId = "local";

/** Anything addressable across hosts. A bare id is never enough to act on. */
export type Ref = {
	host: HostId;
	id: string;
};

export function isLocal(host: HostId): boolean {
	return host === LOCAL_HOST;
}

// A host id is a URL and an id may contain anything, so the key encodes both
// halves rather than relying on a separator being absent from either.
export function refKey(ref: Ref): string {
	return `${encodeURIComponent(ref.host)}:${encodeURIComponent(ref.id)}`;
}

export function parseRefKey(key: string): Ref {
	const separator = key.indexOf(":");
	if (separator === -1) throw new Error(`malformed ref key: ${key}`);
	return {
		host: decodeURIComponent(key.slice(0, separator)),
		id: decodeURIComponent(key.slice(separator + 1)),
	};
}
```

- [ ] **Step 5: Verify green and commit**

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/lib/hosts.test.ts 2>&1 | grep -E "×|Tests "
cd "$STACK" && git add frontend/src/renderer/lib/hosts.ts frontend/src/renderer/lib/hosts.test.ts && git commit -q -m "feat(hosts): host identity primitives

HostId, Ref = {host, id}, LOCAL_HOST and a composite refKey. Local is a
host like any other so no code path special-cases \"is this remote?\";
a project id is filepath.Base(path) on every machine, so a bare id is
never enough to act on and Ref qualifies it at the addressing boundary.
No importer yet.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HnqdDyvae5s7L7KYwtKPd7" && git push -q -u origin ao/agent-orchestrator-96/up-a2-hosts
```

Expected: `Tests  5 passed (5)`.

---

### Task 5: A3 — `feat(remotes): saved-host store, authenticated requests, password-free IPC`

Branch `ao/agent-orchestrator-96/up-a3-store` from `upstream/main` — none of its modules import the flag (A1) or `hosts.ts` (A2), so it stands alone. Ports fork #64, #73 and the AO-79 fixes #1/#4 (all already inside the fork files), with audit cuts 2 and 9 applied.

**Files:**
- Create: `frontend/src/main/remotes-store.ts`, `remotes-store.test.ts` (verbatim from `origin/develop`)
- Create: `frontend/src/main/remote-request.ts`, `remote-request.test.ts` (verbatim)
- Create: `frontend/src/main/remotes-ipc.ts`, `remotes-ipc.test.ts` (rewritten below)
- Create: `frontend/src/main/remotes-main.ts`, `remotes-main.test.ts` (new)
- Modify: `frontend/src/preload.ts` (types after line 19; `remotes` block between `featureBuilds` ending at line 410 and `cloud` at 411)
- Modify: `frontend/src/main.ts` (import after line 104; one call before `ipcMain.handle("app:chooseDirectory"` at line 1716)
- Modify: `frontend/src/renderer/test/setup.ts:248-251`, `frontend/src/renderer/lib/bridge.ts:194-197`, `frontend/e2e/support/fake-bridge.ts:206-209,643-646` (stub after each `featureBuilds` block)

**Interfaces:**
- Consumes: `parseDaemonProbe` from `frontend/src/shared/daemon-attach.ts` (exists upstream, line 57).
- Produces: everything listed under `remotes-store.ts`, `remote-request.ts`, `remotes-ipc.ts`, `remotes-main.ts` in the File structure section; `aoBridge.remotes.{list,add,update,remove,probe,request}`; IPC channels `remotes:list|add|update|remove|probe|request`.

- [ ] **Step 1: Branch, bring the verbatim modules' tests first, watch them fail**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-96
export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
cd "$STACK" && git checkout -q -b ao/agent-orchestrator-96/up-a3-store upstream/main
for f in remotes-store remote-request; do git -C "$W" show origin/develop:frontend/src/main/$f.test.ts > frontend/src/main/$f.test.ts; done
cd frontend && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remotes-store.test.ts src/main/remote-request.test.ts 2>&1 | grep -E "Error|Test Files" | head -3
```

Expected: both files fail to resolve `./remotes-store` / `./remote-request`.

- [ ] **Step 2: Port the two verbatim modules, verify green**

```bash
cd "$STACK"
for f in remotes-store remote-request; do git -C "$W" show origin/develop:frontend/src/main/$f.ts > frontend/src/main/$f.ts; done
cd frontend && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remotes-store.test.ts src/main/remote-request.test.ts 2>&1 | grep -E "×|Tests "
```

Expected: 0 failed. These two suites cover: 0600 created/refused/win32-exempt and add/update/remove semantics (store); Bearer sent, 4xx returned not thrown, `@`-userinfo path refused, prefix kept, probe distinguishing unauthorized/offline/not-a-daemon, the 5s bound (request). Both verified green on upstream/main 2026-08-23 (62 tests across the five ported main-process suites).

- [ ] **Step 3: Write the failing ipc test (callback form)**

Create `frontend/src/main/remotes-ipc.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest";
import { chmod, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { findRemote, removeSavedRemote, toHostViews, updateSavedRemote } from "./remotes-ipc";
import type { RemoteEntry } from "./remotes-store";

const TWO_HOSTS =
	'{"remotes":[{"label":"workbox","url":"http://192.0.2.1:1","password":"old"},{"label":"mini","url":"http://192.0.2.9:9","password":"m"}]}';

async function tempFile(contents = TWO_HOSTS, mode = 0o600): Promise<string> {
	const dir = await mkdtemp(join(tmpdir(), "ao-remotes-ipc-"));
	const path = join(dir, "remotes.json");
	await writeFile(path, contents, "utf8");
	await chmod(path, mode);
	return path;
}

const online = async () => "online" as const;
const dropped = () => vi.fn<(url: string) => Promise<void>>().mockResolvedValue(undefined);

describe("toHostViews", () => {
	it("strips the password before anything crosses to the renderer", () => {
		const views = toHostViews([{ label: "workbox", url: "http://192.0.2.1:3011", password: "supersecret" }]);
		expect(views).toEqual([{ label: "workbox", url: "http://192.0.2.1:3011" }]);
		expect(JSON.stringify(views)).not.toContain("supersecret");
	});
});

describe("findRemote", () => {
	it("returns the saved entry for a url", async () => {
		const path = await tempFile();
		await expect(findRemote(path, "http://192.0.2.9:9")).resolves.toEqual({ label: "mini", url: "http://192.0.2.9:9", password: "m" });
	});

	it("refuses a url it has never saved", async () => {
		const path = await tempFile();
		await expect(findRemote(path, "http://192.0.2.5:5")).rejects.toThrow(/no saved host/);
	});
});

describe("updateSavedRemote", () => {
	it("probes the merged entry before it writes anything", async () => {
		const path = await tempFile();
		const probed: RemoteEntry[] = [];
		const health = await updateSavedRemote(path, "http://192.0.2.1:1", { password: "rotated" }, dropped(), async (entry) => {
			probed.push(entry);
			return "online";
		});
		expect(health).toBe("online");
		// Probed with the new password against the saved address, not with either half.
		expect(probed).toEqual([{ label: "workbox", url: "http://192.0.2.1:1", password: "rotated" }]);
	});

	it("saves nothing and drops nothing when the edited host does not answer", async () => {
		const path = await tempFile();
		const disconnect = dropped();
		const health = await updateSavedRemote(path, "http://192.0.2.1:1", { password: "wrong" }, disconnect, async () => "unauthorized");
		expect(health).toBe("unauthorized");
		expect(await readFile(path, "utf8")).toBe(TWO_HOSTS);
		expect(disconnect).not.toHaveBeenCalled();
	});

	// A live proxy holds the address and password that were saved when it
	// started; after an edit both may be stale, so it does not get to keep serving.
	it("drops the edited host's proxy by its old url", async () => {
		const path = await tempFile();
		const disconnect = dropped();
		await updateSavedRemote(path, "http://192.0.2.1:1", { url: "http://192.0.2.5:5" }, disconnect, online);
		expect(disconnect).toHaveBeenCalledTimes(1);
		expect(disconnect).toHaveBeenCalledWith("http://192.0.2.1:1");
	});
});

describe("removeSavedRemote", () => {
	it("forgets the host and drops its proxy", async () => {
		const path = await tempFile();
		const disconnect = dropped();
		await removeSavedRemote(path, "http://192.0.2.1:1", disconnect);
		expect(JSON.parse(await readFile(path, "utf8")).remotes).toEqual([{ label: "mini", url: "http://192.0.2.9:9", password: "m" }]);
		expect(disconnect).toHaveBeenCalledWith("http://192.0.2.1:1");
	});

	it("refuses to touch a file others can read", async () => {
		const path = await tempFile(TWO_HOSTS, 0o644);
		const disconnect = dropped();
		await expect(removeSavedRemote(path, "http://192.0.2.1:1", disconnect)).rejects.toThrow(/chmod 600/);
		expect(disconnect).not.toHaveBeenCalled();
	});
});
```

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remotes-ipc.test.ts 2>&1 | grep -E "Error|Tests " | head -2
```

Expected: fails to resolve `./remotes-ipc`.

- [ ] **Step 4: Write `remotes-ipc.ts`**

```ts
import { probeRemote, type RemoteHealth } from "./remote-request";
import {
	applyRemoteChanges,
	readRemotes,
	removeRemote,
	updateRemote,
	type RemoteChanges,
	type RemoteEntry,
} from "./remotes-store";

// What the renderer is allowed to see. The password stays in the main process.
export type RemoteHostView = {
	label: string;
	url: string;
};

export function toHostViews(entries: RemoteEntry[]): RemoteHostView[] {
	return entries.map(({ label, url }) => ({ label, url }));
}

export async function findRemote(path: string, url: string): Promise<RemoteEntry> {
	const entry = (await readRemotes(path)).find((candidate) => candidate.url === url);
	if (!entry) throw new Error(`no saved host for ${url}`);
	return entry;
}

// Whatever is serving this url must stop: after an edit it holds a stale
// address or password, after a removal it is an open door with no doorman.
// Called unconditionally — dropping a host that was never connected is a no-op.
type Disconnect = (url: string) => Promise<void>;

/**
 * Edit a saved host in place. Probes before saving exactly as adding does — an
 * edit is how a host gets fixed, and one that lands somewhere unreachable only
 * looks fixed.
 */
export async function updateSavedRemote(
	path: string,
	url: string,
	changes: RemoteChanges,
	disconnect: Disconnect,
	probe: (entry: RemoteEntry) => Promise<RemoteHealth> = probeRemote,
): Promise<RemoteHealth> {
	const health = await probe(applyRemoteChanges(await findRemote(path, url), changes));
	if (health !== "online") return health;
	await updateRemote(path, url, changes);
	await disconnect(url);
	return health;
}

/** Forget a saved host. */
export async function removeSavedRemote(path: string, url: string, disconnect: Disconnect): Promise<void> {
	await removeRemote(path, url);
	await disconnect(url);
}
```

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remotes-ipc.test.ts 2>&1 | grep -E "×|Tests "
```

Expected: `Tests  8 passed (8)`.

- [ ] **Step 5: Write the failing test for the IPC registration**

Create `frontend/src/main/remotes-main.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest";
import { chmod, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { registerRemotesIpc } from "./remotes-main";

type Handler = (event: unknown, ...args: unknown[]) => Promise<unknown>;

// ipcMain stand-in: records what was registered and lets a test invoke it.
function fakeIpc() {
	const handlers = new Map<string, Handler>();
	return {
		ipcMain: { handle: (channel: string, handler: Handler) => void handlers.set(channel, handler) },
		invoke: (channel: string, ...args: unknown[]) => {
			const handler = handlers.get(channel);
			if (!handler) throw new Error(`no handler for ${channel}`);
			return handler({}, ...args);
		},
		channels: () => [...handlers.keys()].sort(),
	};
}

async function tempFile(): Promise<string> {
	const dir = await mkdtemp(join(tmpdir(), "ao-remotes-main-"));
	const path = join(dir, "remotes.json");
	await writeFile(path, '{"remotes":[{"label":"workbox","url":"http://192.0.2.1:1","password":"old"}]}', "utf8");
	await chmod(path, 0o600);
	return path;
}

describe("registerRemotesIpc", () => {
	it("registers the saved-host surface", async () => {
		const ipc = fakeIpc();
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), disconnect: async () => undefined });
		expect(ipc.channels()).toEqual([
			"remotes:add",
			"remotes:list",
			"remotes:probe",
			"remotes:remove",
			"remotes:request",
			"remotes:update",
		]);
	});

	it("lists hosts without their passwords", async () => {
		const ipc = fakeIpc();
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), disconnect: async () => undefined });
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([{ label: "workbox", url: "http://192.0.2.1:1" }]);
	});

	it("saves a new host only after it answers as a daemon", async () => {
		const ipc = fakeIpc();
		const file = await tempFile();
		const probe = vi.fn().mockResolvedValueOnce("offline" as const).mockResolvedValueOnce("online" as const);
		registerRemotesIpc(ipc.ipcMain, { file, disconnect: async () => undefined, probe });
		const mini = { label: "mini", url: "http://192.0.2.9:9", password: "m" };

		await expect(ipc.invoke("remotes:add", mini)).resolves.toBe("offline");
		expect(JSON.parse(await readFile(file, "utf8")).remotes).toHaveLength(1);

		await expect(ipc.invoke("remotes:add", mini)).resolves.toBe("online");
		expect(JSON.parse(await readFile(file, "utf8")).remotes).toHaveLength(2);
	});

	it("drops the proxy of a removed host", async () => {
		const ipc = fakeIpc();
		const disconnect = vi.fn().mockResolvedValue(undefined);
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), disconnect });
		await ipc.invoke("remotes:remove", "http://192.0.2.1:1");
		expect(disconnect).toHaveBeenCalledWith("http://192.0.2.1:1");
	});
});
```

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remotes-main.test.ts 2>&1 | grep -E "Error|Tests " | head -2
```

Expected: fails to resolve `./remotes-main`.

- [ ] **Step 6: Write `remotes-main.ts`**

```ts
import os from "node:os";
import path from "node:path";
import { probeRemote, remoteRequest, type RemoteHealth, type RemoteRequestInit } from "./remote-request";
import { findRemote, removeSavedRemote, toHostViews, updateSavedRemote } from "./remotes-ipc";
import { addRemote, readRemotes, type RemoteChanges, type RemoteEntry } from "./remotes-store";

// The CLI resolves this file through config.StateDir(), which is ~/.ao
// unconditionally — it does NOT honour AO_DATA_DIR (that points at the daemon's
// data dir). Following AO_DATA_DIR here would make the app read a different
// file than `ao --url` writes, which defeats sharing one host list and one
// credential store.
export function remotesFilePath(): string {
	return path.join(os.homedir(), ".ao", "remotes.json");
}

// The slice of Electron's ipcMain these handlers need, so tests need no Electron.
type IpcMainLike = {
	// eslint-disable-next-line @typescript-eslint/no-explicit-any -- the listener
	// args must unify Electron's IpcMain (any[]) with a test fake (unknown[]),
	// and `never[]` rejects both; `any[]` here leaks nowhere past registration.
	handle(channel: string, listener: (event: unknown, ...args: any[]) => Promise<unknown>): void;
};

export type RemotesIpcDeps = {
	file: string;
	/** Stop whatever is serving this url; a no-op for a host that is not connected. */
	disconnect: (url: string) => Promise<void>;
	probe?: (entry: RemoteEntry) => Promise<RemoteHealth>;
};

/**
 * Saved AO daemons, shared with the CLI's ~/.ao/remotes.json. Everything the
 * renderer receives back is password-free (see remotes-ipc.ts); the plaintext
 * password only ever travels renderer -> main, on `add`.
 */
export function registerRemotesIpc(ipcMain: IpcMainLike, { file, disconnect, probe = probeRemote }: RemotesIpcDeps): void {
	ipcMain.handle("remotes:list", async () => toHostViews(await readRemotes(file)));
	ipcMain.handle("remotes:add", async (_event, input: RemoteEntry) => {
		// Probe before saving: a host that never answered is worse than no host,
		// because it looks configured.
		const health = await probe(input);
		if (health === "online") await addRemote(file, input);
		return health;
	});
	ipcMain.handle("remotes:probe", async (_event, url: string) => probe(await findRemote(file, url)));
	ipcMain.handle("remotes:request", async (_event, url: string, init: RemoteRequestInit) =>
		remoteRequest(await findRemote(file, url), init),
	);
	ipcMain.handle("remotes:update", async (_event, url: string, changes: RemoteChanges) =>
		updateSavedRemote(file, url, changes, disconnect, probe),
	);
	ipcMain.handle("remotes:remove", async (_event, url: string) => removeSavedRemote(file, url, disconnect));
}
```

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remotes-main.test.ts 2>&1 | grep -E "×|Tests "
```

Expected: `Tests  4 passed (4)`.

- [ ] **Step 7: Expose the surface in preload, wire main, and stub the three bridges**

`frontend/src/preload.ts` — after line 19 (`import type { DaemonStatus } from "./shared/daemon-status";`) add:

```ts
import type { RemoteHostView } from "./main/remotes-ipc";
import type { RemoteHealth, RemoteRequestInit, RemoteResponse } from "./main/remote-request";
```

and between the `featureBuilds: { … },` block (ends line 410) and `cloud: {` (line 411) insert:

```ts
	// Saved AO daemons, shared with the CLI's ~/.ao/remotes.json. Everything the
	// renderer receives back is password-free (see main/remotes-ipc.ts); the
	// plaintext password only ever travels renderer -> main, on `add`.
	remotes: {
		list: () => ipcRenderer.invoke("remotes:list") as Promise<RemoteHostView[]>,
		add: (input: { label: string; url: string; password: string }) =>
			ipcRenderer.invoke("remotes:add", input) as Promise<RemoteHealth>,
		// An edit carries only what changed: an omitted password keeps the saved
		// one, so a rotated credential is fixed without the renderer ever holding
		// the old one.
		update: (url: string, changes: { label?: string; url?: string; password?: string }) =>
			ipcRenderer.invoke("remotes:update", url, changes) as Promise<RemoteHealth>,
		remove: (url: string) => ipcRenderer.invoke("remotes:remove", url) as Promise<void>,
		probe: (url: string) => ipcRenderer.invoke("remotes:probe", url) as Promise<RemoteHealth>,
		request: (url: string, init: RemoteRequestInit) =>
			ipcRenderer.invoke("remotes:request", url, init) as Promise<RemoteResponse>,
	},
```

`frontend/src/main.ts` — after line 104 (`import { parseOpenFolderPathArg } from "./main/open-folder-arg";`) add:

```ts
import { registerRemotesIpc, remotesFilePath } from "./main/remotes-main";
```

and immediately before `ipcMain.handle("app:chooseDirectory", …` (line 1716) add:

```ts
registerRemotesIpc(ipcMain, {
	file: remotesFilePath(),
	// No host is ever connected yet; the proxy registry that owns live
	// connections lands in the next change and replaces this.
	disconnect: async () => undefined,
});
```

The same stub block goes into all three bridge fakes, each directly after its `featureBuilds: { … },` block — `frontend/src/renderer/test/setup.ts` (after line 251), `frontend/src/renderer/lib/bridge.ts` (after line 197, inside the `satisfies AoBridge` fallback; precede it with the comment below), and `frontend/e2e/support/fake-bridge.ts` (after lines 209 and 646 — both `satisfies AoBridge` objects):

```ts
		remotes: {
			list: async () => [],
			add: async () => "offline" as const,
			update: async () => "offline" as const,
			remove: async () => undefined,
			probe: async () => "offline" as const,
			request: async () => ({ status: 0, body: null }),
		},
```

Comment for `bridge.ts` only, above its block:

```ts
		// The daemon-served web build has no Electron bridge and so no access to
		// ~/.ao/remotes.json. Reporting no hosts leaves the UI showing local only,
		// which is the truth there.
```

- [ ] **Step 8: Verify everything, including both typechecks**

```bash
cd "$STACK/frontend"
node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remotes-store.test.ts src/main/remote-request.test.ts src/main/remotes-ipc.test.ts src/main/remotes-main.test.ts src/renderer/components/GlobalSettingsForm.test.tsx 2>&1 | grep -E "×|Test Files|Tests "
node_modules/.bin/tsc --noEmit && echo TSC_OK
node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json && echo E2E_TSC_OK
```

Expected: `Test Files  5 passed`, 0 failed, `TSC_OK`, `E2E_TSC_OK`. A `TS2322`/`TS1360` on any of the three stubs means a key is missing or extra relative to `preload.ts` — the stub must have exactly the six entries.

- [ ] **Step 9: Scrub and commit**

```bash
cd "$STACK"
git add frontend/src/main/remotes-store.ts frontend/src/main/remotes-store.test.ts frontend/src/main/remote-request.ts frontend/src/main/remote-request.test.ts \
  frontend/src/main/remotes-ipc.ts frontend/src/main/remotes-ipc.test.ts frontend/src/main/remotes-main.ts frontend/src/main/remotes-main.test.ts \
  frontend/src/preload.ts frontend/src/main.ts frontend/src/renderer/test/setup.ts frontend/src/renderer/lib/bridge.ts frontend/e2e/support/fake-bridge.ts
git diff --cached --name-only | xargs grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 means clean)"
git commit -q -m "feat(remotes): saved-host store, authenticated requests, password-free IPC

The desktop app reads and writes the CLI's ~/.ao/remotes.json (mode 0600,
refused if looser; win32 exempt because Node reports 0o666 there), probes
a host through /healthz with the saved connection password as a Bearer
token, and exposes list/add/update/remove/probe/request over IPC. Only
{label, url} ever crosses to the renderer; a request path that would
redirect the credential off-host is refused before anything is sent.

Nothing in the renderer calls this yet; it lands dark.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HnqdDyvae5s7L7KYwtKPd7"
git push -q -u origin ao/agent-orchestrator-96/up-a3-store
```

---

### Task 6: A4 — `feat(remotes): token-gated loopback proxy for remote daemons`

Branch `ao/agent-orchestrator-96/up-a4-proxy` from `up-a3-store`. Ports fork #67, #77, #80 and AO-79 #2/#3 (inside the fork's `remote-proxy.ts`) verbatim, and the registry without its browser-runtime hook.

**Files:**
- Create: `frontend/src/main/remote-proxy.ts`, `remote-proxy.test.ts` (verbatim from `origin/develop`)
- Create: `frontend/src/main/remote-registry.ts`, `remote-registry.test.ts` (trimmed, below)
- Modify: `frontend/src/main/remotes-main.ts`, `remotes-main.test.ts`
- Modify: `frontend/src/preload.ts` (`remotes` block gains three entries; one more type import)
- Modify: `frontend/src/main.ts` (registry construction; `before-quit` at upstream line 2190)
- Modify: the three bridge stubs (three more entries each)

**Interfaces:**
- Consumes: `RemoteEntry`, `findRemote`, `probeRemote` from Task 5.
- Produces: `startRemoteProxy(entry) → {base, url, close}`; `RemoteRegistry`; `aoBridge.remotes.{connect,disconnect,connected}`; IPC `remotes:connect|disconnect|connected`; `ConnectedHostView {label,url,base}`. `RemotesIpcDeps` becomes `{ file, registry, probe? }`.

- [ ] **Step 1: Branch, bring the proxy test, watch it fail, port the proxy**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-96
export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
cd "$STACK" && git checkout -q -b ao/agent-orchestrator-96/up-a4-proxy ao/agent-orchestrator-96/up-a3-store
git -C "$W" show origin/develop:frontend/src/main/remote-proxy.test.ts > frontend/src/main/remote-proxy.test.ts
cd frontend && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remote-proxy.test.ts 2>&1 | grep -E "Error|Tests " | head -2
cd "$STACK" && git -C "$W" show origin/develop:frontend/src/main/remote-proxy.ts > frontend/src/main/remote-proxy.ts
cd frontend && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remote-proxy.test.ts 2>&1 | grep -E "×|Tests "
```

Expected: first run fails to resolve `./remote-proxy`; second run `Tests  15 passed (15)` — token stripped + credential injected, https never in the clear, prefix restored, no-token 404 with nothing forwarded, near-miss token refused, preflight answered locally, CORS on real responses, 502 when unreachable, lifecycle logs carry no secret, loopback only, upgraded socket closed on `close()`, SSE delivered as written, WebSocket tunnelled, token-less upgrade destroyed. Verified on upstream/main 2026-08-23.

- [ ] **Step 2: Write the registry test (no runtime-link cases, plus the no-op guarantee ipc relies on)**

Create `frontend/src/main/remote-registry.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest";
import { RemoteRegistry } from "./remote-registry";

const WORKBOX = { label: "workbox", url: "http://192.0.2.1:3011", password: "pw-one" };
const MINI = { label: "mini", url: "http://192.0.2.9:3011", password: "pw-two" };

function fakeProxies() {
	const closed: string[] = [];
	const start = vi.fn(async (entry: { url: string }) => ({
		base: `http://127.0.0.1:9999/${entry.url.replace(/\W/g, "")}`,
		url: entry.url,
		close: async () => {
			closed.push(entry.url);
		},
	}));
	return { start, closed };
}

describe("RemoteRegistry", () => {
	it("keeps several hosts connected at once", async () => {
		const { start } = fakeProxies();
		const registry = new RemoteRegistry(start);
		await registry.connect(WORKBOX);
		await registry.connect(MINI);
		expect(registry.views().map((view) => view.url)).toEqual([WORKBOX.url, MINI.url]);
		expect(start).toHaveBeenCalledTimes(2);
	});

	it("connecting the same url twice reuses the live proxy", async () => {
		const { start } = fakeProxies();
		const registry = new RemoteRegistry(start);
		const first = await registry.connect(WORKBOX);
		const second = await registry.connect(WORKBOX);
		expect(second).toEqual(first);
		expect(start).toHaveBeenCalledTimes(1);
	});

	it("disconnect closes only that host's proxy", async () => {
		const { start, closed } = fakeProxies();
		const registry = new RemoteRegistry(start);
		await registry.connect(WORKBOX);
		await registry.connect(MINI);
		await registry.disconnect(WORKBOX.url);
		expect(closed).toEqual([WORKBOX.url]);
		expect(registry.views().map((view) => view.url)).toEqual([MINI.url]);
	});

	it("disconnecting a host that was never connected is a no-op", async () => {
		const { start, closed } = fakeProxies();
		const registry = new RemoteRegistry(start);
		await expect(registry.disconnect(WORKBOX.url)).resolves.toBeUndefined();
		expect(closed).toEqual([]);
		expect(start).not.toHaveBeenCalled();
	});

	it("never exposes the password", async () => {
		const registry = new RemoteRegistry(fakeProxies().start);
		const view = await registry.connect(WORKBOX);
		expect(JSON.stringify(view)).not.toContain("pw-one");
		expect(JSON.stringify(registry.views())).not.toContain("pw-one");
	});

	it("a host that fails to start does not join the registry", async () => {
		const registry = new RemoteRegistry(async () => {
			throw new Error("EADDRNOTAVAIL");
		});
		await expect(registry.connect(WORKBOX)).rejects.toThrow("EADDRNOTAVAIL");
		expect(registry.views()).toEqual([]);
	});

	it("closeAll tears every proxy down", async () => {
		const { start, closed } = fakeProxies();
		const registry = new RemoteRegistry(start);
		await registry.connect(WORKBOX);
		await registry.connect(MINI);
		await registry.closeAll();
		expect(closed.sort()).toEqual([WORKBOX.url, MINI.url].sort());
		expect(registry.views()).toEqual([]);
	});
});
```

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remote-registry.test.ts 2>&1 | grep -E "Error|Tests " | head -2
```

Expected: fails to resolve `./remote-registry`.

- [ ] **Step 3: Write `remote-registry.ts`**

```ts
import type { ActiveProxy } from "./remote-proxy";
import type { RemoteEntry } from "./remotes-store";

/** What the renderer may know about a connected host. Password stays here. */
export type ConnectedHostView = {
	label: string;
	url: string;
	base: string;
};

type StartProxy = (entry: RemoteEntry) => Promise<ActiveProxy>;

// N hosts live at once, one proxy each, keyed by url: the app does not view
// one host, it talks to several.
export class RemoteRegistry {
	private readonly live = new Map<string, { view: ConnectedHostView; proxy: ActiveProxy }>();

	constructor(private readonly start: StartProxy) {}

	async connect(entry: RemoteEntry): Promise<ConnectedHostView> {
		const existing = this.live.get(entry.url);
		// Reuse rather than restart: a second connect would strand the first
		// proxy's port with the renderer still holding streams against it.
		if (existing) return existing.view;

		const proxy = await this.start(entry);
		const view = { label: entry.label, url: entry.url, base: proxy.base };
		this.live.set(entry.url, { view, proxy });
		return view;
	}

	async disconnect(url: string): Promise<void> {
		const entry = this.live.get(url);
		if (!entry) return;
		this.live.delete(url);
		await entry.proxy.close();
	}

	views(): ConnectedHostView[] {
		return [...this.live.values()].map(({ view }) => view);
	}

	async closeAll(): Promise<void> {
		const entries = [...this.live.values()];
		this.live.clear();
		await Promise.all(entries.map(({ proxy }) => proxy.close()));
	}
}
```

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remote-registry.test.ts 2>&1 | grep -E "×|Tests "
```

Expected: `Tests  7 passed (7)`.

- [ ] **Step 4: Extend the IPC test for connect/disconnect/connected (failing first)**

In `frontend/src/main/remotes-main.test.ts` change the import and every `registerRemotesIpc(...)` call: the deps become `{ file, registry, probe? }`. Replace the file's `describe` block with:

```ts
import { RemoteRegistry } from "./remote-registry";
// (keep the other imports)

function registryOf(closed: string[] = []) {
	return new RemoteRegistry(async (entry) => ({
		base: "http://127.0.0.1:9999/tok",
		url: entry.url,
		close: async () => {
			closed.push(entry.url);
		},
	}));
}

describe("registerRemotesIpc", () => {
	it("registers the saved-host and connection surface", async () => {
		const ipc = fakeIpc();
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), registry: registryOf() });
		expect(ipc.channels()).toEqual([
			"remotes:add",
			"remotes:connect",
			"remotes:connected",
			"remotes:disconnect",
			"remotes:list",
			"remotes:probe",
			"remotes:remove",
			"remotes:request",
			"remotes:update",
		]);
	});

	it("lists hosts without their passwords", async () => {
		const ipc = fakeIpc();
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), registry: registryOf() });
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([{ label: "workbox", url: "http://192.0.2.1:1" }]);
	});

	it("saves a new host only after it answers as a daemon", async () => {
		const ipc = fakeIpc();
		const file = await tempFile();
		const probe = vi.fn().mockResolvedValueOnce("offline" as const).mockResolvedValueOnce("online" as const);
		registerRemotesIpc(ipc.ipcMain, { file, registry: registryOf(), probe });
		const mini = { label: "mini", url: "http://192.0.2.9:9", password: "m" };

		await expect(ipc.invoke("remotes:add", mini)).resolves.toBe("offline");
		expect(JSON.parse(await readFile(file, "utf8")).remotes).toHaveLength(1);

		await expect(ipc.invoke("remotes:add", mini)).resolves.toBe("online");
		expect(JSON.parse(await readFile(file, "utf8")).remotes).toHaveLength(2);
	});

	it("connects a saved host only after it answers as a daemon, and hands back a password-free view", async () => {
		const ipc = fakeIpc();
		const probe = vi.fn().mockResolvedValueOnce("offline" as const).mockResolvedValueOnce("online" as const);
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), registry: registryOf(), probe });

		await expect(ipc.invoke("remotes:connect", "http://192.0.2.1:1")).rejects.toThrow(/is offline/);
		await expect(ipc.invoke("remotes:connected")).resolves.toEqual([]);

		const view = await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
		expect(view).toEqual({ label: "workbox", url: "http://192.0.2.1:1", base: "http://127.0.0.1:9999/tok" });
		expect(JSON.stringify(await ipc.invoke("remotes:connected"))).not.toContain("old");
	});

	it("removing a connected host closes its proxy", async () => {
		const ipc = fakeIpc();
		const closed: string[] = [];
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), registry: registryOf(closed), probe: async () => "online" });
		await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
		await ipc.invoke("remotes:remove", "http://192.0.2.1:1");
		expect(closed).toEqual(["http://192.0.2.1:1"]);
		await expect(ipc.invoke("remotes:connected")).resolves.toEqual([]);
	});

	it("disconnect closes the proxy and forgets the view", async () => {
		const ipc = fakeIpc();
		const closed: string[] = [];
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), registry: registryOf(closed), probe: async () => "online" });
		await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
		await ipc.invoke("remotes:disconnect", "http://192.0.2.1:1");
		expect(closed).toEqual(["http://192.0.2.1:1"]);
		await expect(ipc.invoke("remotes:connected")).resolves.toEqual([]);
	});
});
```

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remotes-main.test.ts 2>&1 | grep -E "×|Tests "
```

Expected: 4 failures (the three connection channels are unregistered and the connect flow is missing; the two file-only handlers still pass).

- [ ] **Step 5: Update `remotes-main.ts` to own the registry**

Replace the `RemotesIpcDeps` type and the `registerRemotesIpc` function with:

```ts
import type { RemoteRegistry } from "./remote-registry";
// (keep the other imports)

export type RemotesIpcDeps = {
	file: string;
	registry: RemoteRegistry;
	probe?: (entry: RemoteEntry) => Promise<RemoteHealth>;
};

/**
 * Saved AO daemons, shared with the CLI's ~/.ao/remotes.json. Everything the
 * renderer receives back is password-free (see remotes-ipc.ts); the plaintext
 * password only ever travels renderer -> main, on `add`.
 */
export function registerRemotesIpc(ipcMain: IpcMainLike, { file, registry, probe = probeRemote }: RemotesIpcDeps): void {
	const disconnect = (url: string) => registry.disconnect(url);

	ipcMain.handle("remotes:list", async () => toHostViews(await readRemotes(file)));
	ipcMain.handle("remotes:add", async (_event, input: RemoteEntry) => {
		// Probe before saving: a host that never answered is worse than no host,
		// because it looks configured.
		const health = await probe(input);
		if (health === "online") await addRemote(file, input);
		return health;
	});
	ipcMain.handle("remotes:probe", async (_event, url: string) => probe(await findRemote(file, url)));
	ipcMain.handle("remotes:request", async (_event, url: string, init: RemoteRequestInit) =>
		remoteRequest(await findRemote(file, url), init),
	);
	ipcMain.handle("remotes:update", async (_event, url: string, changes: RemoteChanges) =>
		updateSavedRemote(file, url, changes, disconnect, probe),
	);
	ipcMain.handle("remotes:remove", async (_event, url: string) => removeSavedRemote(file, url, disconnect));

	ipcMain.handle("remotes:connect", async (_event, url: string) => {
		const entry = await findRemote(file, url);
		// Probe before starting a proxy: a reachable port may serve something
		// other than an AO daemon, and exposing it as connected can wedge the
		// app at boot.
		const health = await probe(entry);
		if (health !== "online") throw new Error(`host ${url} is ${health}`);
		return registry.connect(entry);
	});
	ipcMain.handle("remotes:disconnect", async (_event, url: string) => disconnect(url));
	ipcMain.handle("remotes:connected", async () => registry.views());
}
```

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remotes-main.test.ts src/main/remotes-ipc.test.ts 2>&1 | grep -E "×|Tests "
```

Expected: `Tests  14 passed (14)`.

- [ ] **Step 6: Wire main.ts, preload and the stubs**

`frontend/src/main.ts`:

```ts
// import line (replace the Task 5 import):
import { registerRemotesIpc, remotesFilePath } from "./main/remotes-main";
import { RemoteRegistry } from "./main/remote-registry";
import { startRemoteProxy } from "./main/remote-proxy";

// replace the Task 5 registerRemotesIpc call with:
const remoteRegistry = new RemoteRegistry(startRemoteProxy);
registerRemotesIpc(ipcMain, { file: remotesFilePath(), registry: remoteRegistry });
```

and in the `before-quit` handler (upstream line 2190) change

```ts
			browserQuitCleanupPromise = disposeAllBrowserViewHosts().finally(() => {
```

to

```ts
			browserQuitCleanupPromise = Promise.all([disposeAllBrowserViewHosts(), remoteRegistry.closeAll()])
				.then(() => undefined)
				.finally(() => {
```

(the rest of that block is unchanged — `closeAll` drops every tunnel so quit cannot deadlock on an upgraded socket, fork #77).

`frontend/src/preload.ts` — add the type import and three entries at the end of the `remotes` block:

```ts
import type { ConnectedHostView } from "./main/remote-registry";
// …
		connect: (url: string) => ipcRenderer.invoke("remotes:connect", url) as Promise<ConnectedHostView>,
		disconnect: (url: string) => ipcRenderer.invoke("remotes:disconnect", url) as Promise<void>,
		connected: () => ipcRenderer.invoke("remotes:connected") as Promise<ConnectedHostView[]>,
```

Stubs — append to the `remotes` block in `renderer/test/setup.ts` and both `e2e/support/fake-bridge.ts` objects:

```ts
			connect: async () => ({ label: "", url: "", base: "" }),
			disconnect: async () => undefined,
			connected: async () => [],
```

and in `renderer/lib/bridge.ts` (browser build — a remote host needs main):

```ts
			connect: async () => {
				throw new Error("remote hosts need the desktop app");
			},
			disconnect: async () => undefined,
			connected: async () => [],
```

- [ ] **Step 7: Verify, scrub, commit**

```bash
cd "$STACK/frontend"
node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/ 2>&1 | grep -E "×|Test Files|Tests "
node_modules/.bin/tsc --noEmit && echo TSC_OK
node_modules/.bin/tsc --noEmit -p tsconfig.e2e.json && echo E2E_TSC_OK
cd "$STACK"
git add frontend/src/main/remote-proxy.ts frontend/src/main/remote-proxy.test.ts frontend/src/main/remote-registry.ts frontend/src/main/remote-registry.test.ts \
  frontend/src/main/remotes-main.ts frontend/src/main/remotes-main.test.ts frontend/src/preload.ts frontend/src/main.ts \
  frontend/src/renderer/test/setup.ts frontend/src/renderer/lib/bridge.ts frontend/e2e/support/fake-bridge.ts
git diff --cached --name-only | xargs grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 means clean)"
git commit -q -m "feat(remotes): token-gated loopback proxy for remote daemons

The renderer cannot authenticate to a remote daemon itself: EventSource
and WebSocket cannot set Authorization, and app://renderer has no CORS
standing there. Main starts one loopback proxy per connected host bound
to 127.0.0.1 on an ephemeral port; the renderer addresses it as
http://127.0.0.1:<port>/<128-bit token>/, the proxy strips the token and
the renderer Origin, injects the saved Bearer credential, restores the
host's path prefix, speaks TLS to an https host, and streams SSE and
WebSocket frames as they arrive. A request without the token is answered
404 and forwarded nowhere. Proxies are torn down on disconnect and on
quit (tunnelled sockets included, so quit cannot hang on one).

Still dark: nothing in the renderer connects a host yet.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HnqdDyvae5s7L7KYwtKPd7"
git push -q -u origin ao/agent-orchestrator-96/up-a4-proxy
```

Expected: every `src/main/` test file green (the proxy's 15, registry 7, ipc 8, remotes-main 6, plus the store and request suites), both typechecks OK.

---

### Task 7: A5 — `feat(hosts): per-host API clients and flag-gated host boot`

Branch `ao/agent-orchestrator-96/up-a5-clients` from an integration merge of `up-a1-flag` + `up-a2-hosts` + `up-a4-proxy`, tagged `up-a5-base`. First consumer of the flag; opens upstream last, after all three sides have merged.

**Files:**
- Create: `frontend/src/renderer/lib/host-clients.ts`, `host-clients.test.ts` (fork version minus `syncConnectedHosts`)
- Create: `frontend/src/renderer/lib/active-host.ts`, `active-host.test.ts`
- Modify: `frontend/src/renderer/main.tsx` (import after line 18; call inside `renderApp` after the `render(...)` call, before line 92's closing brace)

**Interfaces:**
- Consumes: `useUiStore().remoteHosts` (Task 3), `LOCAL_HOST`, `isLocal`, `HostId` (Task 4), `aoBridge.remotes.{connect,disconnect}` (Task 6), `getApiBaseUrl`/`hasTrustedApiBaseUrl` from `lib/api-client.ts` (upstream).
- Produces: the `host-clients.ts` and `active-host.ts` signatures in the File structure section.

- [ ] **Step 1: Cut the integration base, bring the host-clients test (minus the sync case), watch it fail**

```bash
export STACK=/Users/amongstar/dev/agent-orchestrator-up-stack
W=/Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-96
export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"
cd "$STACK" && git checkout -q -b ao/agent-orchestrator-96/up-a5-clients ao/agent-orchestrator-96/up-a4-proxy
git merge -q --no-edit ao/agent-orchestrator-96/up-a1-flag ao/agent-orchestrator-96/up-a2-hosts
git tag -f up-a5-base
git status --porcelain | head -3
git -C "$W" show origin/develop:frontend/src/renderer/lib/host-clients.test.ts > frontend/src/renderer/lib/host-clients.test.ts
```

The octopus merge is clean by construction — A1 (settings/i18n), A2 (`lib/hosts`) and A4 (`src/main` + preload) touch disjoint files; a conflict here means an earlier task strayed. `up-a5-base` is what the hand-off's rebase recipe pivots on after the three sides merge upstream.

Then edit the file: delete `syncConnectedHosts,` from the import list (line 16) and delete the whole `it("re-binds every proxy main already has connected", …)` case (the last test, from line 137 to the file's closing `});` of that `it`). Run:

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/lib/host-clients.test.ts 2>&1 | grep -E "Error|Tests " | head -2
```

Expected: fails to resolve `./host-clients`.

- [ ] **Step 2: Write `host-clients.ts`**

```ts
import createClient from "openapi-fetch";
import type { paths } from "../../api/schema";
import { getApiBaseUrl, hasTrustedApiBaseUrl } from "./api-client";
import { aoBridge } from "./bridge";
import { isLocal, type HostId } from "./hosts";

// One client per host. Local reads the live daemon base (which still moves when
// the daemon restarts on a new port); a remote reads the loopback proxy base its
// main-process proxy is listening on.
const remoteBases = new Map<HostId, string>();
const remoteLabels = new Map<HostId, string>();
const clients = new Map<HostId, ReturnType<typeof createClient<paths>>>();
const listeners = new Set<() => void>();
let connectedSnapshot: HostId[] = [];

function publishConnectedHosts(): void {
	connectedSnapshot = [...remoteBases.keys()];
	for (const listener of listeners) listener();
}

export function registerHostBase(host: HostId, base: string, label = host): void {
	remoteLabels.set(host, label);
	if (remoteBases.get(host) === base) return;
	remoteBases.set(host, base);
	// A changed base invalidates the cached client bound to the old one.
	clients.delete(host);
	publishConnectedHosts();
}

export function forgetHost(host: HostId): void {
	const removed = remoteBases.delete(host);
	remoteLabels.delete(host);
	clients.delete(host);
	if (removed) publishConnectedHosts();
}

export function connectedHosts(): HostId[] {
	return connectedSnapshot;
}

export function subscribeConnectedHosts(listener: () => void): () => void {
	listeners.add(listener);
	return () => listeners.delete(listener);
}

export function hostLabelFor(host: HostId): string {
	return remoteLabels.get(host) ?? host;
}

export function baseUrlFor(host: HostId): string | null {
	if (isLocal(host)) return hasTrustedApiBaseUrl() ? getApiBaseUrl() : null;
	return remoteBases.get(host) ?? null;
}

export function isHostReady(host: HostId): boolean {
	return baseUrlFor(host) !== null;
}

export function clientFor(host: HostId) {
	const base = baseUrlFor(host);
	if (base === null) throw new Error(`host ${host} is not connected`);
	// The local client is not cached: its base follows the daemon across restarts
	// and a stale cached client would keep talking to a dead port.
	if (isLocal(host)) return createClient<paths>({ baseUrl: base });
	const cached = clients.get(host);
	if (cached) return cached;
	const client = createClient<paths>({ baseUrl: base });
	clients.set(host, client);
	return client;
}

/** Start (or reuse) a proxy for a saved host and bind a client to its base. */
export async function connectHost(url: HostId): Promise<void> {
	const view = await aoBridge.remotes.connect(url);
	registerHostBase(view.url, view.base, view.label);
}

export async function disconnectHost(url: HostId): Promise<void> {
	forgetHost(url);
	await aoBridge.remotes.disconnect(url);
}
```

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/lib/host-clients.test.ts 2>&1 | grep -E "×|Tests "
```

Expected: `Tests  9 passed (9)` — local resolves to the daemon base, unregistered remote not ready, remote routed through its proxy base, local routed direct, local follows a port move, forget makes not-ready, connect binds the base main returned, connected-host changes publish, forget precedes the proxy close.

- [ ] **Step 3: Write the failing boot test**

Create `frontend/src/renderer/lib/active-host.test.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "./bridge";
import { setApiBaseUrl } from "./api-client";
import { initHosts } from "./active-host";
import { baseUrlFor, forgetHost } from "./host-clients";
import { useUiStore } from "../stores/ui-store";

const WORKBOX = "http://192.0.2.1:3011";
const MINI = "http://192.0.2.9:3011";

beforeEach(() => {
	localStorage.clear();
	forgetHost(WORKBOX);
	forgetHost(MINI);
	setApiBaseUrl(null);
	vi.restoreAllMocks();
	useUiStore.setState({ remoteHosts: true });
});

function savedHosts() {
	vi.spyOn(aoBridge.remotes, "list").mockResolvedValue([
		{ label: "workbox", url: WORKBOX },
		{ label: "mini", url: MINI },
	]);
	vi.spyOn(aoBridge.remotes, "connect").mockImplementation(async (url) => ({
		label: url === WORKBOX ? "workbox" : "mini",
		url,
		base: url === WORKBOX ? "http://127.0.0.1:9001/one" : "http://127.0.0.1:9002/two",
	}));
}

describe("remoteHosts flag", () => {
	it("never reads the saved hosts while the flag is off", async () => {
		useUiStore.setState({ remoteHosts: false });
		savedHosts();

		await initHosts();

		expect(aoBridge.remotes.list).not.toHaveBeenCalled();
		expect(aoBridge.remotes.connect).not.toHaveBeenCalled();
		expect(baseUrlFor(WORKBOX)).toBeNull();
	});

	it("connects the saved hosts when the flag is turned on", async () => {
		useUiStore.setState({ remoteHosts: false });
		savedHosts();
		await initHosts();
		expect(baseUrlFor(MINI)).toBeNull();

		useUiStore.getState().setRemoteHosts(true);

		await vi.waitFor(() => expect(baseUrlFor(MINI)).toBe("http://127.0.0.1:9002/two"));
		expect(baseUrlFor(WORKBOX)).toBe("http://127.0.0.1:9001/one");
	});

	it("disconnects every remote host when the flag is turned off", async () => {
		savedHosts();
		const disconnect = vi.spyOn(aoBridge.remotes, "disconnect").mockResolvedValue(undefined);
		await initHosts();
		expect(baseUrlFor(WORKBOX)).not.toBeNull();

		useUiStore.getState().setRemoteHosts(false);

		await vi.waitFor(() => expect(disconnect).toHaveBeenCalledTimes(2));
		expect(disconnect).toHaveBeenCalledWith(WORKBOX);
		expect(disconnect).toHaveBeenCalledWith(MINI);
		expect(baseUrlFor(WORKBOX)).toBeNull();
		expect(baseUrlFor(MINI)).toBeNull();
	});
});

describe("multi-host boot", () => {
	it("connects every saved host", async () => {
		savedHosts();

		await initHosts();

		expect(aoBridge.remotes.connect).toHaveBeenCalledTimes(2);
		expect(baseUrlFor(WORKBOX)).toBe("http://127.0.0.1:9001/one");
		expect(baseUrlFor(MINI)).toBe("http://127.0.0.1:9002/two");
	});

	it("keeps connecting other saved hosts when one is unavailable", async () => {
		vi.spyOn(aoBridge.remotes, "list").mockResolvedValue([
			{ label: "workbox", url: WORKBOX },
			{ label: "mini", url: MINI },
		]);
		vi.spyOn(aoBridge.remotes, "connect").mockImplementation(async (url) => {
			if (url === WORKBOX) throw new Error("offline");
			return { label: "mini", url, base: "http://127.0.0.1:9002/two" };
		});

		await initHosts();

		expect(baseUrlFor(WORKBOX)).toBeNull();
		expect(baseUrlFor(MINI)).toBe("http://127.0.0.1:9002/two");
	});

	it("treats an unreadable saved-host list as no remote hosts", async () => {
		vi.spyOn(aoBridge.remotes, "list").mockRejectedValue(new Error("chmod 600 required"));

		await expect(initHosts()).resolves.toBeUndefined();
		expect(baseUrlFor(WORKBOX)).toBeNull();
		expect(baseUrlFor(MINI)).toBeNull();
	});
});
```

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/lib/active-host.test.ts 2>&1 | grep -E "Error|Tests " | head -2
```

Expected: fails to resolve `./active-host`.

- [ ] **Step 4: Write `active-host.ts`**

```ts
import { useUiStore } from "../stores/ui-store";
import { aoBridge } from "./bridge";
import { connectedHosts, connectHost, disconnectHost } from "./host-clients";

/** Connect every saved host without making any one host own the window. */
async function connectSavedHosts(): Promise<void> {
	const saved = await aoBridge.remotes.list().catch(() => []);
	await Promise.allSettled(saved.map(({ url }) => connectHost(url)));
}

async function disconnectAllHosts(): Promise<void> {
	await Promise.allSettled(connectedHosts().map((host) => disconnectHost(host)));
}

let watchingFlag = false;

/**
 * Boot the remote-host layer, honouring the Remote hosts flag. Off means no
 * saved host is read, probed or connected — not "connected but hidden" — so a
 * reviewer can verify the off state from the network side. Flipping the switch
 * connects or tears down without a restart.
 */
export async function initHosts(): Promise<void> {
	if (!watchingFlag) {
		watchingFlag = true;
		useUiStore.subscribe((state, previous) => {
			if (state.remoteHosts === previous.remoteHosts) return;
			void (state.remoteHosts ? connectSavedHosts() : disconnectAllHosts());
		});
	}
	if (useUiStore.getState().remoteHosts) await connectSavedHosts();
}
```

```bash
cd "$STACK/frontend" && node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/lib/active-host.test.ts 2>&1 | grep -E "×|Tests "
```

Expected: `Tests  6 passed (6)` (verified on upstream/main 2026-08-23 with the stub from Task 5 in place).

- [ ] **Step 5: Call it at boot**

`frontend/src/renderer/main.tsx` — after line 18 add `import { initHosts } from "./lib/active-host";`, and inside `renderApp()` directly after the `createRoot(...).render(...)` statement (before the function's closing brace on line 92) add:

```ts
	// Saved hosts are additive. A bad credential file or sleeping machine must
	// never block local first paint; the reactive host registry adds each proxy
	// to the workspace fan-out as it connects. Inert while the Remote hosts
	// flag is off.
	void initHosts();
```

- [ ] **Step 6: Verify, scrub, commit**

```bash
cd "$STACK/frontend"
node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer/lib/ src/renderer/stores/ src/main/ 2>&1 | grep -E "×|Test Files|Tests "
node_modules/.bin/tsc --noEmit && echo TSC_OK
cd "$STACK"
git add frontend/src/renderer/lib/host-clients.ts frontend/src/renderer/lib/host-clients.test.ts frontend/src/renderer/lib/active-host.ts frontend/src/renderer/lib/active-host.test.ts frontend/src/renderer/main.tsx
git diff --cached --name-only | xargs grep -nE "amongstar|AronPerez|/Users/|AO-[0-9]+|\(#[0-9]{2,3}\)|ponytail:" ; echo "scrub exit=$? (1 means clean)"
git commit -q -m "feat(hosts): per-host API clients and flag-gated host boot

clientFor(host) binds an openapi-fetch client to each connected host's
proxy base (local keeps reading the live daemon base). initHosts() runs
after first paint and connects every saved host — but only while the
Remote hosts flag is on: off means the saved-host file is never read and
no proxy is started, and turning the flag off tears every remote proxy
down without a restart. With the flag off this change is inert.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HnqdDyvae5s7L7KYwtKPd7"
git push -q -u origin ao/agent-orchestrator-96/up-a5-clients
```

Expected: all listed suites green, `TSC_OK`.

---

### Task 8: A0, part 2 — the hand-off (lands on `develop`)

Extends the Task 2 branch and PR with the finished stack's hand-off. Runs in the AO worktree.

**Files:**
- Create: `docs/upstreaming-stack-status.md`
- Create: `docs/upstreaming-pr-bodies/a1-flag.md` … `a5-clients.md`

Per orchestrator direction during execution: publish each final SHA additionally to a
**clean ref** (`up-a1-flag` … `up-a5-clients`) with `git push origin <sha>:refs/heads/<name>`
and write the hand-off against those — upstream PRs must not advertise session-internal
branch names. The namespaced refs stay for AO tracking.

- [ ] **Step 1: Return to the hand-off branch**

```bash
cd /Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-96
git checkout -q ao/agent-orchestrator-96/upstream-handoff
```

- [ ] **Step 2: Write the hand-off**

Create `docs/upstreaming-stack-status.md`:

```markdown
# Upstream remote-hosts stack — status and hand-off

Built by Plan 1 (`docs/superpowers/plans/2026-08-23-upstream-remote-hosts-foundation.md`) against `upstream/main @ <SHA from Task 1>`. Branches live on `origin` (our fork); **no PR has been opened upstream**. The RFC text is `docs/upstreaming-rfc-remote-hosts.md`.

## Branch topology

A1, A2 and A3 are cut independently from `upstream/main` and share no code — they can be opened, reviewed and merged **in any order**. A4 builds on A3. A5 builds on a local integration merge of A1+A2+A4 (tagged `up-a5-base` in the stack worktree) and opens last.

## Order of operations (human)

1. Post the RFC as an issue on `Untrivial-ai/agent-orchestrator`; ping Discord (daily sync 10:00 PM IST). Wait for a maintainer reaction — especially on its question 1, which decides whether the multi-host half (A2, A5 and later plans) proceeds as designed.
2. Once there is a reaction, open A1, A2 and A3 — any order, all three at once is fine; none depends on another and each is dark behind the flag.
3. Open A4 after A3 is squash-merged (rebase first, below). Open A5 after A1, A2 and A4 have all merged.
4. Rebase recipes (run in the stack worktree; upstream squash-merges, so always `--onto` across a merged parent, never a merge):

       # A1/A2/A3 while waiting — plain drift, nothing of ours merged yet:
       git fetch upstream
       git rebase upstream/main ao/agent-orchestrator-96/up-a1-flag
       git push --force-with-lease origin ao/agent-orchestrator-96/up-a1-flag
       # (same two lines for up-a2-hosts and up-a3-store)

       # A4, after A3 merges:
       git rebase --onto upstream/main ao/agent-orchestrator-96/up-a3-store ao/agent-orchestrator-96/up-a4-proxy
       git push --force-with-lease origin ao/agent-orchestrator-96/up-a4-proxy

       # A5, after A1+A2+A4 merge:
       git rebase --onto upstream/main up-a5-base ao/agent-orchestrator-96/up-a5-clients
       git push --force-with-lease origin ao/agent-orchestrator-96/up-a5-clients

## The branches

| # | Branch (on `origin`) | Base | Upstream title | Non-test files | Tests it carries |
| --- | --- | --- | --- | --- | --- |
| 1 | `ao/agent-orchestrator-96/up-a1-flag` | `upstream/main` | feat(settings): add an experimental Remote hosts flag | 10 | ui-store ×3, settings switch ×1 |
| 2 | `ao/agent-orchestrator-96/up-a2-hosts` | `upstream/main` | feat(hosts): host identity primitives | 1 | hosts ×5 |
| 3 | `ao/agent-orchestrator-96/up-a3-store` | `upstream/main` | feat(remotes): saved-host store, authenticated requests, password-free IPC | 9 | store + request suites, ipc ×8, main ×4 |
| 4 | `ao/agent-orchestrator-96/up-a4-proxy` | `up-a3-store` | feat(remotes): token-gated loopback proxy for remote daemons | 9 | proxy ×15, registry ×7, main ×6 |
| 5 | `ao/agent-orchestrator-96/up-a5-clients` | merge(A1, A2, A4), tag `up-a5-base` | feat(hosts): per-host API clients and flag-gated host boot | 3 | host-clients ×9, active-host ×6 |

## Opening a PR (A1/A2/A3 in any order; A4 and A5 in sequence)

    gh pr create --repo Untrivial-ai/agent-orchestrator --base main \
      --head AronPerez:ao/agent-orchestrator-96/up-a1-flag \
      --title "feat(settings): add an experimental Remote hosts flag" \
      --body-file docs/upstreaming-pr-bodies/a1-flag.md

Bodies follow upstream's template (What / Why / How / Testing / Checklist) and are in `docs/upstreaming-pr-bodies/`. Each body's "Why" links the RFC issue number once it exists — fill `#RFC` in before opening.

## What a reviewer can verify with the flag off, on every branch

- The Settings modal shows one new row; nothing else in the UI differs (A1).
- `initHosts()` never calls `remotes.list` (`active-host.test.ts`, A5).
- Main opens no socket without an IPC call (`remote-registry.test.ts` "never connected is a no-op", A4).
- `connectedHosts()` is `[]` so every later fan-out is a loop of one (A5 onward).
```

Also create `docs/upstreaming-pr-bodies/a1-flag.md` … `a5-clients.md` — five files, each exactly:

```markdown
## What

<the first paragraph of that branch's commit message>

## Why

Part of the remote-hosts series proposed in #RFC. This slice lands dark: with the Remote hosts flag off there is no behaviour change.

## How

<the remaining paragraphs of that branch's commit message>

## Testing

`cd frontend && npm run typecheck && npx vitest run <the branch's test files>` — counts as in the table in `docs/upstreaming-stack-status.md`. CI: `frontend` (typecheck, typecheck:e2e, vitest) and, for PRs 3–4, nothing in `go`/`api-drift` is touched.

## Checklist

- [x] Branched from `main`
- [x] One focused change; links the related issue
- [x] Follows AGENTS.md conventions and PR hygiene
- [x] Tests added for user-visible behavior
- [x] Relevant CI checks pass for the area touched
```

Take each commit message with `git -C "$STACK" log -1 --format=%b <branch>`; the first paragraph is "What", the rest is "How".


- [ ] **Step 3: Scrub, commit, push (the Task 2 PR updates in place)**

```bash
cd /Users/amongstar/.ao/data/worktrees/agent-orchestrator/agent-orchestrator-96
grep -rnE "amongstar|/Users/" docs/upstreaming-pr-bodies/ ; echo "bodies scrub exit=$? (1 means clean — the status doc may name local paths, the RFC and bodies may not)"
git add docs/upstreaming-stack-status.md docs/upstreaming-pr-bodies/
git commit -q -m "docs: hand-off for the upstream remote-hosts stack

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01HnqdDyvae5s7L7KYwtKPd7"
git push -q origin ao/agent-orchestrator-96/upstream-handoff
```

Expected: push succeeds. Report the PR link, the five branch names, and the upstream SHA the stack is based on.

---


## Self-review

**Spec coverage.** §2.2 flag name/scope/default/surface → Task 3; off-state semantics and live toggle → Task 7 (`active-host.test.ts`: never reads, connects on, disconnects off); "main opens no socket without an IPC call" → Task 6 registry no-op test; A1–A5 → Tasks 3–7 in dependency order (A2 before A5, A3 before A4); A0 RFC + the questions in §4 → Tasks 2 and 8; §2.1 "branch from upstream/main", squash-rebase recipe → Tasks 1 and 8; §3.3 scrub list → every commit step. Topology: A1/A2/A3 are siblings because their import lists share nothing (`hosts.ts` imports nothing; A3's modules import neither `ui-store` nor `hosts.ts`); A5 consumes all three sides, so it alone sits on an integration merge. Not covered here, by design: `response-validation.ts` (Plan 2), `HostSelect`/`useRemoteHosts` gating (Plan 2, where those files arrive), D1's `startRuntime` hook.

**Placeholder scan.** Every code step carries its code; every port step carries a verified command and the expected test count. The one templated spot — the PR bodies taking their What/How from the commit messages — names the exact `git log` command that produces the text.

**Type consistency.** `RemotesIpcDeps` is `{file, disconnect, probe?}` in Task 5 and is *replaced* by `{file, registry, probe?}` in Task 6 — stated in both tasks and in the File structure section. `disconnect: (url: string) => Promise<void>` is the same shape in `remotes-ipc.ts` (Task 5), the Task 5 `main.ts` stub, and the `registry.disconnect` closure in Task 6. `ConnectedHostView {label,url,base}` is what `remotes:connect`/`connected` return (Task 6) and what `connectHost` consumes via `registerHostBase(view.url, view.base, view.label)` (Task 7). `RemoteHostView {label,url}` is what `remotes:list` returns (Task 5) and what `initHosts` maps over `({ url })` (Task 7).
