# Remote-Session Editor Handoff (A-lean) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. (This project's AO worker sessions must not use subagent delegation — execute inline.) Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the desktop app's "Open in editor" work for sessions on remote hosts by attaching a locally installed VS Code-family editor to the remote workspace over the user's own SSH, and render remote-ness as a neutral state instead of the current red "Session workspace is not available" error.

**Architecture:** The renderer addresses the handoff by `Ref` (`{host, id}`) like every other cross-host surface; Electron main picks a per-host strategy: local sessions keep today's flow, remote sessions resolve the workspace path through a new credential-gated daemon route (served through the existing per-host proxy, which injects the Bearer) and open `vscode-remote://ssh-remote+<dest><path>` folder URIs with the locally installed editor CLI. The SSH destination (`user@host`) is one new optional field on the saved-host entry; the deliberately LAN-blocked `/api/v1/desktop` surface is left untouched.

**Tech Stack:** Electron main (TypeScript), React renderer (vitest + testing-library), Go daemon (chi, specgen-generated OpenAPI), openapi-typescript.

**Spec:** `docs/remote-sessions-edd.md` § "SSH transport spike — AO-82" (verified facts this plan leans on: the Go CLI tolerates unknown fields in `remotes.json`; SSH-over-the-app was recommended "documentation first, then the small version"). The decision record below stands in for the un-written option-B spec.

## Decision record (why this shape — do not re-litigate in-task)

- **Rejected: launching an editor via the remote daemon ("option B").** It requires unblocking `/api/v1/desktop`, which `lanControlBlockedPrefixes` (`backend/internal/httpd/lan_listener.go:143`) + `TestEveryLANRouteIsCredentialGated` + the AGENTS.md LAN-exemption hard rule keep off the network by design; the AO-82 spike already rejected the adjacent tunnel-to-loopback design for the same class of reason; and it opens the editor **on the remote machine's screen**, which is not the feature.
- **Rejected: chaining this behind AO-82 step 2 (SSH *transport* for the daemon connection).** VS Code Remote-SSH does its own SSH; the tunnel manager buys password-off-the-wire for the daemon protocol, an orthogonal goal. The only shared atom is the `sshDestination` store field (Task 3), which a future AO-82 implementation can consume unchanged.
- **Kept invariant:** absolute workspace paths never cross the preload bridge to the renderer. Main consumes them; the renderer sees availability booleans and labels only.
- **New gated read is justified:** an authenticated remote client can already read workspace *contents* (`/api/v1/sessions/{sessionId}/workspace/file`, see `specgen/build.go:1615`), so exposing the workspace *path* on the same credential-gated surface leaks no new information class.

## Global Constraints

- Branch: `ao/agent-orchestrator-107/remote-editor-handoff` off `origin/develop`. One PR, opened as **draft**, base `develop`.
- Worktree toolchain (from memory `frontend-test-preview-workflow`): `export PATH="$HOME/.nvm/versions/node/v24.14.1/bin:$PATH"`; symlink deps `ln -sfn ~/dev/agent-orchestrator/frontend/node_modules frontend/node_modules` and `ln -sfn ~/dev/agent-orchestrator/packages/product-ui/node_modules packages/product-ui/node_modules`; **remove both symlinks before every commit** (`rm -f frontend/node_modules packages/product-ui/node_modules` — `git add -A` would commit them; check `git status --porcelain` for `??`).
- Frontend tests: `cd frontend && node_modules/.bin/vitest run --config vite.renderer.config.ts <file>`. Typecheck: `node_modules/.bin/tsc --noEmit` (expect 0 lines with both symlinks present).
- Backend tests: `cd backend && go test ./internal/httpd/...` — run **after** spec regen (`TestRouteSpecParity` reads the embedded `openapi.yaml`; regenerating after the test makes a correct route look unmounted).
- Spec regen: `npm run api:spec` (repo root) then `npm run api:ts` (repo root; rewrites `frontend/src/api/schema.ts`). Commit regenerated files with the route change.
- Every new i18n key lands in all 8 locales (`en, de, es, fr, ja, ko, pt-BR, zh-CN`) — `frontend/src/renderer/i18n/instance.test.ts` fails the build on a missing key.
- `/api/v1/desktop` stays in `lanControlBlockedPrefixes`. Do not add exemptions to `unauthenticatedLANRoutes`.
- Conventional commits. The repo pre-commit hook is gofmt-only; a gofmt failure is real.
- Known pre-existing flakes (never evidence about this diff): `_shell-index-redirect.test.tsx` (~20% on clean develop) and `useDiffHighlight.test.ts` under parallel load.
- Main/preload/IPC changes do NOT hot-reload — restart Electron (`rs` in the Forge terminal) before manual verification.

## File structure

| File | Responsibility after this plan |
|---|---|
| `frontend/src/shared/editor-handoff.ts` | Wire types for the Ref-addressed IPC contract, incl. the new `remote` state block |
| `frontend/src/main/editor-handoff.ts` | Pure per-host strategy logic (local launch, remote URI launch, neutral states); owns `RemoteHostInfo` dep type |
| `frontend/src/main.ts` | Impure edges only: host-aware workspace resolution (local daemon vs proxy base), `remoteHost` lookup, IPC validation |
| `frontend/src/main/remotes-store.ts` | `RemoteEntry` gains optional `sshDestination` |
| `frontend/src/main/remotes-ipc.ts` | `RemoteHostView` carries `sshDestination` to the renderer (never the password) |
| `frontend/src/preload.ts` | Widened `editorHandoff` + `remotes` bridge types |
| `frontend/src/renderer/hooks/useEditorHandoff.ts` | Host-keyed query + host-carrying mutation |
| `frontend/src/renderer/components/TopbarOpenEditorButton.tsx` | Renders the three remote states without the red error |
| `frontend/src/renderer/components/ShellTopbar.tsx` | Passes `session.host` (1 line) |
| `frontend/src/renderer/components/AddRemoteHostDialog.tsx` | Optional SSH-destination field |
| `backend/internal/httpd/controllers/desktop_workspace.go` | Registers the gated `/sessions/{sessionId}/workspace-location` twin |
| `backend/internal/httpd/apispec/specgen/build.go` | Spec entry for the new route |

Task order: 1 → (2 and 3 in either order) → 4. Tasks 1–3 are each shippable/reviewable alone; Task 4 consumes 2+3.

---

### Task 1: Ref-address the handoff; render remote as a neutral state

Ships alone: kills the false red error on every remote session and stops querying the local daemon about sessions it does not own (which also closes the cross-host id-collision wrong-folder path).

**Files:**
- Modify: `frontend/src/shared/editor-handoff.ts`
- Modify: `frontend/src/main/editor-handoff.ts`
- Modify: `frontend/src/main.ts:760-806` (resolver), `frontend/src/main.ts:1884-1891` (IPC handlers)
- Modify: `frontend/src/preload.ts:306-311`
- Modify: `frontend/src/renderer/hooks/useEditorHandoff.ts`
- Modify: `frontend/src/renderer/components/TopbarOpenEditorButton.tsx`
- Modify: `frontend/src/renderer/components/ShellTopbar.tsx:366-371`
- Modify: `frontend/src/renderer/i18n/*.json` (8 files)
- Test: `frontend/src/main/editor-handoff.test.ts`, `frontend/src/renderer/components/TopbarOpenEditorButton.test.tsx`

**Interfaces:**
- Produces (Tasks 2–4 rely on these exact names):
  - `EditorHandoffStateInput = { host: string; sessionId: string }`
  - `OpenSessionTargetInput = { host: string; sessionId: string; targetId?: OpenTargetId }`
  - `EditorHandoffState.remote?: { hostLabel: string; sshConfigured: boolean }`
  - `EditorHandoffDeps.resolveWorkspace(host: string, sessionId: string): Promise<string>`
  - `EditorHandoffDeps.remoteHost(host: string): Promise<RemoteHostInfo | null>` with `export type RemoteHostInfo = { label: string; sshDestination?: string }` (null ⇔ local; the `host === "local"` sentinel comparison lives only in main.ts's dep impl)
  - i18n keys `editor.remoteOn`, `editor.remoteNeedsSsh`

- [ ] **Step 1: Write the failing tests**

Append to `frontend/src/main/editor-handoff.test.ts` (inside `describe("editor handoff")`), and add `remoteHost: vi.fn().mockResolvedValue(null),` to the `deps()` factory:

```ts
	it("reports a remote session as a neutral remote state without asking the local daemon", async () => {
		const input = deps({ remoteHost: vi.fn().mockResolvedValue({ label: "Mini" }) });
		const handoff = createEditorHandoff(input);
		const state = await handoff.getState({ host: "http://192.0.2.1:3011", sessionId: "adopt-14732" });
		expect(state.workspaceAvailable).toBe(false);
		expect(state.remote).toEqual({ hostLabel: "Mini", sshConfigured: false });
		// The false error came from resolving a remote id against the local daemon.
		expect(input.resolveWorkspace).not.toHaveBeenCalled();
		expect(state.unavailableReason).toBeUndefined();
	});

	it("refuses to open a remote workspace while nothing can open it, naming the host", async () => {
		const handoff = createEditorHandoff(deps({ remoteHost: vi.fn().mockResolvedValue({ label: "Mini" }) }));
		await expect(handoff.open({ host: "http://192.0.2.1:3011", sessionId: "adopt-14732", targetId: "vscode" }))
			.rejects.toThrow("The workspace for this session is on Mini.");
	});
```

Update every existing call in the file to the new shapes: `handoff.getState("ao-1")` → `handoff.getState({ host: "local", sessionId: "ao-1" })`; `handoff.open({ sessionId: "ao-1", ... })` → `handoff.open({ host: "local", sessionId: "ao-1", ... })`. The `launch` assertion is unchanged.

Append to `frontend/src/renderer/components/TopbarOpenEditorButton.test.tsx` (its `renderButton` must pass the new prop: `<TopbarOpenEditorButton host="local" sessionId="sess-1" projectId="proj-1" />`; add a variant `renderButton(host = "local")`):

```ts
	it("renders a remote session as informational, not as an error", async () => {
		setState({
			...availableState,
			workspaceAvailable: false,
			remote: { hostLabel: "Mini", sshConfigured: false },
		});
		renderButton("http://192.0.2.1:3011");
		const button = await screen.findByRole("button", { name: "Open in Cursor" });
		expect(button).toBeDisabled();
		expect(button).toHaveAttribute("title", "Workspace is on Mini");
		// The old dead end: a red topbar error on every remote session.
		expect(screen.queryByText(/session workspace is not available/i)).not.toBeInTheDocument();
	});
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from `frontend/`): `node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/editor-handoff.test.ts src/renderer/components/TopbarOpenEditorButton.test.tsx`
Expected: FAIL — type errors on the new input shape and the missing `remote` field/`remoteHost` dep.

- [ ] **Step 3: Update the shared wire types**

In `frontend/src/shared/editor-handoff.ts`, replace the `EditorHandoffState` and `OpenSessionTargetInput` declarations with:

```ts
export type EditorHandoffState = {
	targets: OpenTarget[];
	preferredEditorId: EditorId;
	workspaceAvailable: boolean;
	unavailableReason?: string;
	/**
	 * Present when the session lives on a remote host. The renderer renders
	 * remote-ness from this block — it never receives a filesystem path.
	 */
	remote?: {
		hostLabel: string;
		/** An SSH destination is saved for the host, so remote open can work. */
		sshConfigured: boolean;
	};
};

export type EditorHandoffStateInput = {
	/** "local" for the local daemon, else the saved host url. */
	host: string;
	sessionId: string;
};

export type OpenSessionTargetInput = {
	host: string;
	sessionId: string;
	targetId?: OpenTargetId;
};
```

- [ ] **Step 4: Thread the host through main's handoff module**

In `frontend/src/main/editor-handoff.ts`:

Add next to `ResolvedCommand`:

```ts
/** What main knows about a saved remote host; null means the local daemon. */
export type RemoteHostInfo = {
	label: string;
	sshDestination?: string;
};
```

In `EditorHandoffDeps`, change `resolveWorkspace` and add `remoteHost`:

```ts
	resolveWorkspace: (host: string, sessionId: string) => Promise<string>;
	remoteHost: (host: string) => Promise<RemoteHostInfo | null>;
```

Change the `EditorHandoff` surface:

```ts
export type EditorHandoff = {
	getState(input: EditorHandoffStateInput): Promise<EditorHandoffState>;
	open(input: OpenSessionTargetInput): Promise<OpenSessionTargetResult>;
};
```

(import `EditorHandoffStateInput` from the shared module.)

Replace `getState` with:

```ts
		async getState({ host, sessionId }) {
			void sessionId;
			const preferredEditorId = await deps.readPreference();
			const remote = await deps.remoteHost(host);
			if (remote) {
				// A remote workspace is a place, not a failure: no unavailableReason,
				// so the renderer has nothing to paint red. Nothing can open it yet.
				return {
					targets,
					preferredEditorId,
					workspaceAvailable: false,
					remote: { hostLabel: remote.label, sshConfigured: false },
				};
			}
			try {
				await deps.resolveWorkspace(host, sessionId);
				return { targets, preferredEditorId, workspaceAvailable: true };
			} catch (error) {
				return {
					targets,
					preferredEditorId,
					workspaceAvailable: false,
					unavailableReason: workspaceUnavailable(error),
				};
			}
		},
```

At the top of `open`, after the `sessionId` guard, add:

```ts
			const remote = await deps.remoteHost(input.host);
			if (remote) throw new Error(`The workspace for this session is on ${remote.label}.`);
```

and change the one `deps.resolveWorkspace(sessionId)` call to `deps.resolveWorkspace(input.host, sessionId)`.

- [ ] **Step 5: Update main.ts edges**

In `frontend/src/main.ts`, change the resolver signature (`:760`) and add the local guard:

```ts
async function resolveSessionWorkspaceForDesktop(host: string, sessionId: string): Promise<string> {
	if (host !== "local") throw new Error("Remote workspaces are resolved through the host's own daemon.");
	// ...existing body unchanged...
```

In the `createEditorHandoff({ ... })` deps (`:791`), keep `resolveWorkspace: resolveSessionWorkspaceForDesktop,` (signature now matches) and add:

```ts
	// null ⇔ local. The "local" sentinel is the renderer's LOCAL_HOST
	// (renderer/lib/hosts.ts); it crosses the IPC as a plain string.
	remoteHost: async (host: string) => {
		if (host === "local") return null;
		const view = registry.views().find((candidate) => candidate.url === host);
		return { label: view?.label ?? host };
	},
```

`registry` is declared later in the file (`const registry = new RemoteRegistry(...)`, ~`:2082`); this closure only runs after boot, so the TDZ is never hit — do not reorder declarations for it.

Replace the two IPC handlers (`:1884-1891`) with:

```ts
ipcMain.handle("editorHandoff:getState", (event, input) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
	const shaped = input && typeof input === "object" ? input as { host?: unknown; sessionId?: unknown } : {};
	return editorHandoff.getState({
		host: typeof shaped.host === "string" ? shaped.host : "local",
		sessionId: typeof shaped.sessionId === "string" ? shaped.sessionId : "",
	});
});
ipcMain.handle("editorHandoff:open", (event, input) => {
	if (event.sender !== getShellWebContents()) throw new Error("Untrusted editor handoff request.");
	const shaped = input && typeof input === "object" ? input as Record<string, unknown> : {};
	return editorHandoff.open({
		host: typeof shaped.host === "string" ? shaped.host : "local",
		sessionId: typeof shaped.sessionId === "string" ? shaped.sessionId : "",
		...(typeof shaped.targetId === "string" ? { targetId: shaped.targetId as OpenTargetId } : {}),
	} as OpenSessionTargetInput);
});
```

(`OpenTargetId` is already imported in main.ts via the shared module — if not, add it to the existing `shared/editor-handoff` import.)

In `frontend/src/preload.ts:306-311`, replace the `editorHandoff` block with:

```ts
	editorHandoff: {
		getState: (input: EditorHandoffStateInput) =>
			ipcRenderer.invoke("editorHandoff:getState", input) as Promise<EditorHandoffState>,
		open: (input: OpenSessionTargetInput) =>
			ipcRenderer.invoke("editorHandoff:open", input) as Promise<OpenSessionTargetResult>,
	},
```

adding `EditorHandoffStateInput` to preload's existing `shared/editor-handoff` type import.

- [ ] **Step 6: Update renderer hook and components**

`frontend/src/renderer/hooks/useEditorHandoff.ts` — key by host, carry host:

```ts
export const editorHandoffQueryKey = (host: string, sessionId: string) => ["editor-handoff", host, sessionId] as const;

export function useEditorHandoffState(host: string, sessionId: string) {
	return useQuery({
		queryKey: editorHandoffQueryKey(host, sessionId),
		enabled: Boolean(sessionId),
		staleTime: 10_000,
		retry: false,
		queryFn: () => aoBridge.editorHandoff.getState({ host, sessionId }),
	});
}
```

`OpenSessionTargetMutationInput` gains `host: string`; the mutation fn becomes `aoBridge.editorHandoff.open({ host, sessionId, ...(targetId ? { targetId } : {}) })`; the `onSuccess` `setQueryData` key becomes `editorHandoffQueryKey(input.host, input.sessionId)`.

`frontend/src/renderer/components/TopbarOpenEditorButton.tsx` — add the prop and the neutral remote derivation:

```ts
export function TopbarOpenEditorButton({
	host,
	sessionId,
	projectId,
	style,
}: {
	host: string;
	sessionId: string;
	projectId: string;
	style?: React.CSSProperties;
}) {
```

`useEditorHandoffState(host, sessionId)`; `launch` passes `{ sessionId, projectId, host, ... }`. Then replace the `guidance`/`mainTitle` derivation with:

```ts
	const remote = state?.remote ?? null;
	// A remote workspace nothing can open yet is information, not an error:
	// it gets a title, never the red TopbarActionError.
	const remoteNotice = remote && !workspaceAvailable
		? remote.sshConfigured
			? null
			: t("editor.remoteOn", { host: remote.hostLabel })
		: null;
	const guidance = !stateQuery.isPending && !workspaceAvailable && !remote
		? state?.unavailableReason ?? t("editor.workspaceUnavailable")
		: !stateQuery.isPending && !remote && editors.length === 0
			? t("editor.noEditorGuidance", { fileManager: fileManagerName, terminal: terminalName })
			: null;
	const mainLabel = open.isPending ? t("editor.opening") : preferred ? t("editor.open") : t("editor.chooseEditor");
	const mainTitle = remoteNotice
		?? guidance
		?? (preferred ? t("editor.openWorkspaceInTitle", { name: preferred.name }) : t("editor.chooseEditorTitle"));
```

(the existing error banner condition `launchError || guidance` is untouched — `remoteNotice` deliberately never reaches it).

`frontend/src/renderer/components/ShellTopbar.tsx:366-371` — add `host={session.host}` to the `<TopbarOpenEditorButton>` call.

i18n — add to all 8 locale files, after the `"editor.workspaceUnavailable"` key:

| locale | `editor.remoteOn` | `editor.remoteNeedsSsh` |
|---|---|---|
| en | `Workspace is on {{host}}` | `To open it here, add an SSH destination for {{host}} in its host settings.` |
| de | `Arbeitsbereich liegt auf {{host}}` | `Zum Öffnen hier ein SSH-Ziel für {{host}} in den Host-Einstellungen hinterlegen.` |
| es | `El espacio de trabajo está en {{host}}` | `Para abrirlo aquí, añade un destino SSH para {{host}} en su configuración de host.` |
| fr | `L'espace de travail est sur {{host}}` | `Pour l'ouvrir ici, ajoutez une destination SSH pour {{host}} dans ses réglages d'hôte.` |
| ja | `ワークスペースは {{host}} 上にあります` | `ここで開くには、ホスト設定で {{host}} の SSH 接続先を追加してください。` |
| ko | `워크스페이스가 {{host}}에 있습니다` | `여기서 열려면 호스트 설정에서 {{host}}의 SSH 대상을 추가하세요.` |
| pt-BR | `O workspace está em {{host}}` | `Para abri-lo aqui, adicione um destino SSH para {{host}} nas configurações do host.` |
| zh-CN | `工作区位于 {{host}}` | `要在此处打开，请在主机设置中为 {{host}} 添加 SSH 目标。` |

(`editor.remoteNeedsSsh` is consumed in Task 4; adding it now keeps the locale files touched once.)

- [ ] **Step 7: Run the tests and typecheck**

Run: `node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/editor-handoff.test.ts src/renderer/components/TopbarOpenEditorButton.test.tsx src/renderer/i18n` then `node_modules/.bin/tsc --noEmit`
Expected: PASS; tsc 0 lines. (tsc is what proves every `useEditorHandoffState`/`open` call site was updated.)

- [ ] **Step 8: Commit**

```bash
rm -f frontend/node_modules packages/product-ui/node_modules
git add -A && git commit -m "fix(hosts): address editor handoff by host, render remote as a state not an error"
ln -sfn ~/dev/agent-orchestrator/frontend/node_modules frontend/node_modules
ln -sfn ~/dev/agent-orchestrator/packages/product-ui/node_modules packages/product-ui/node_modules
```

---

### Task 2: Credential-gated workspace-location route + host-aware resolver in main

**Files:**
- Modify: `backend/internal/httpd/controllers/desktop_workspace.go`
- Modify: `backend/internal/httpd/controllers/dto.go:347-354` (comment only)
- Modify: `backend/internal/httpd/apispec/specgen/build.go:1624-1634` (add sibling entry)
- Modify: `frontend/src/main.ts:760-790`
- Test: `backend/internal/httpd/controllers/desktop_workspace_test.go`
- Regenerated: `backend/internal/httpd/apispec/openapi.yaml`, `frontend/src/api/schema.ts`

**Interfaces:**
- Consumes: nothing from other tasks (independent of Task 1 at the daemon; the main.ts hunk applies on top of Task 1's signature).
- Produces: `GET /api/v1/sessions/{sessionId}/workspace-location` → `200 {sessionId, workspacePath}` | `404` (`SESSION_NOT_FOUND` / `SESSION_WORKSPACE_NOT_FOUND`); `resolveSessionWorkspaceForDesktop(host, sessionId)` resolving remote hosts through their proxy base. Task 4 calls the latter via `deps.resolveWorkspace`.

- [ ] **Step 1: Write the failing Go test**

Append to `backend/internal/httpd/controllers/desktop_workspace_test.go`, following its existing `doRequest` style (copy the setup lines of the 200-case test at the top of that file — same server/session fixture, only the path differs):

```go
// The desktop app resolves a REMOTE session's workspace through this gated
// twin: /api/v1/desktop is deliberately LAN-blocked, so the desktop route can
// never answer over the network, while this one is served with auth.
func TestSessionWorkspaceLocationGatedTwin(t *testing.T) {
	server, seed := newDesktopWorkspaceServer(t) // reuse this file's existing fixture helper; match its real name and seeded session id
	_ = seed

	body, status, _ := doRequest(t, server, http.MethodGet, "/api/v1/sessions/ao-1/workspace-location", "")
	if status != http.StatusOK {
		t.Fatalf("gated workspace-location = %d, want 200; body=%s", status, body)
	}
	var got struct {
		SessionID     string `json:"sessionId"`
		WorkspacePath string `json:"workspacePath"`
	}
	mustJSON(t, body, &got)
	if got.SessionID != "ao-1" || got.WorkspacePath == "" {
		t.Fatalf("body = %+v, want sessionId ao-1 and a workspacePath", got)
	}

	body, status, _ = doRequest(t, server, http.MethodGet, "/api/v1/sessions/does-not-exist/workspace-location", "")
	assertErrorCode(t, body, status, http.StatusNotFound, "SESSION_NOT_FOUND")
}
```

Adapt the fixture call to the file's actual helper (read the first test in the file; it constructs the server and a session whose workspace exists — reuse exactly that, keeping this test's assertions verbatim).

- [ ] **Step 2: Run to verify it fails**

Run: `cd backend && go test ./internal/httpd/controllers/ -run TestSessionWorkspaceLocationGatedTwin`
Expected: FAIL — 404/405 on the unregistered path.

- [ ] **Step 3: Register the twin route**

In `backend/internal/httpd/controllers/desktop_workspace.go`, parameterize the handler by route (the nil-Svc `NotImplemented` branch must report the right path) and register both:

```go
// Register mounts the desktop-only workspace-location route and its
// credential-gated twin. The twin lives outside /api/v1/desktop on purpose:
// lanControlBlockedPrefixes keeps /desktop off the network, while the twin is
// meant to be served — behind the connection password — so the desktop app can
// resolve a REMOTE session's workspace through that host's authenticated API.
// An authenticated client can already read workspace file contents, so the
// path itself is no new information class.
func (c *DesktopWorkspaceController) Register(r chi.Router) {
	r.Get("/desktop/sessions/{sessionId}/workspace", c.location("/api/v1/desktop/sessions/{sessionId}/workspace"))
	r.Get("/sessions/{sessionId}/workspace-location", c.location("/api/v1/sessions/{sessionId}/workspace-location"))
}

func (c *DesktopWorkspaceController) location(route string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c.Svc == nil {
			apispec.NotImplemented(w, r, http.MethodGet, route)
			return
		}
		id := domain.SessionID(chi.URLParam(r, "sessionId"))
		workspacePath, err := c.Svc.WorkspaceLocation(r.Context(), id)
		if err != nil {
			envelope.WriteError(w, r, err)
			return
		}
		envelope.WriteJSON(w, http.StatusOK, DesktopWorkspaceLocationResponse{
			SessionID:     id,
			WorkspacePath: workspacePath,
		})
	}
}
```

In `dto.go`, update the `DesktopWorkspaceLocationResponse` comment to:

```go
// DesktopWorkspaceLocationResponse is returned by the LAN-blocked desktop
// handoff route and by its credential-gated twin under /sessions. Electron main
// consumes the absolute path and never exposes it through the preload bridge.
```

In `specgen/build.go`, directly after the `getDesktopSessionWorkspace` entry (`:1624-1634`), add:

```go
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/workspace-location", id: "getSessionWorkspaceLocation", tag: "sessions",
			summary:    "Resolve a session workspace location over the authenticated API",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.DesktopWorkspaceLocationResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
```

- [ ] **Step 4: Regenerate spec + schema, run backend suite**

Run (repo root): `npm run api:spec && npm run api:ts`
Then: `cd backend && gofmt -l ./internal/... && go test ./internal/httpd/...`
Expected: gofmt silent; suite PASS — including `TestRouteSpecParity` (route now in the embedded spec) and `TestEveryLANRouteIsCredentialGated` (the new `/sessions/...` route is auth-gated on the LAN listener automatically; **no** `unauthenticatedLANRoutes` entry).

- [ ] **Step 5: Make main's resolver host-aware**

In `frontend/src/main.ts`, replace the Task-1 remote guard in `resolveSessionWorkspaceForDesktop` so both branches share the fetch/parse body:

```ts
async function resolveSessionWorkspaceForDesktop(host: string, sessionId: string): Promise<string> {
	let target: string;
	if (host === "local") {
		if (daemonStatus.state !== "ready" || !daemonStatus.port) {
			throw new Error("AO daemon is not ready.");
		}
		target = `http://127.0.0.1:${daemonStatus.port}/api/v1/desktop/sessions/${encodeURIComponent(sessionId)}/workspace`;
	} else {
		// The remote daemon LAN-blocks /desktop; its gated twin is served with
		// auth, and the per-host proxy injects the Bearer on the way through.
		const view = registry.views().find((candidate) => candidate.url === host);
		if (!view) throw new Error("That host is not connected.");
		target = `${view.base}/api/v1/sessions/${encodeURIComponent(sessionId)}/workspace-location`;
	}
	const controller = new AbortController();
	const timer = setTimeout(() => controller.abort(), DAEMON_PROBE_TIMEOUT_MS);
	try {
		const response = await net.fetch(target, { signal: controller.signal });
		const body = await response.json() as Record<string, unknown>;
		if (!response.ok) {
			const message = typeof body.message === "string" ? body.message : "Session workspace is not available.";
			throw new Error(message);
		}
		const workspacePath = body.workspacePath;
		if (typeof workspacePath !== "string" || !path.isAbsolute(workspacePath)) {
			throw new Error("Session workspace is not available.");
		}
		return workspacePath;
	} catch (error) {
		if (error instanceof Error && error.name === "AbortError") {
			throw new Error("Timed out while resolving the session workspace.");
		}
		throw error;
	} finally {
		clearTimeout(timer);
	}
}
```

(Note: do **not** `filepath`-clean or re-normalize the remote path — it belongs to the remote machine's namespace; the daemon already cleaned it.)

- [ ] **Step 6: Typecheck + frontend suite spot-check**

Run: `cd frontend && node_modules/.bin/tsc --noEmit && node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/editor-handoff.test.ts`
Expected: 0 tsc lines; PASS (nothing calls the remote branch yet — `getState` still early-returns for remote).

- [ ] **Step 7: Commit**

```bash
rm -f frontend/node_modules packages/product-ui/node_modules
git add -A && git commit -m "feat(api): credential-gated session workspace-location twin for remote editor handoff"
ln -sfn ~/dev/agent-orchestrator/frontend/node_modules frontend/node_modules
ln -sfn ~/dev/agent-orchestrator/packages/product-ui/node_modules packages/product-ui/node_modules
```

---

### Task 3: `sshDestination` on saved hosts + dialog field

**Files:**
- Modify: `frontend/src/main/remotes-store.ts`
- Modify: `frontend/src/main/remotes-ipc.ts:12-20`
- Modify: `frontend/src/preload.ts:545-559` (both inline `remotes` types)
- Modify: `frontend/src/renderer/hooks/useRemoteHosts.ts:28` (`RemoteHostView`)
- Modify: `frontend/src/renderer/components/AddRemoteHostDialog.tsx`
- Modify: `frontend/src/renderer/i18n/*.json` (8 files)
- Test: `frontend/src/main/remotes-store.test.ts`, `frontend/src/renderer/components/AddRemoteHostDialog.test.tsx`

**Interfaces:**
- Produces: `RemoteEntry.sshDestination?: string` (trimmed; absent when empty), surfaced on `RemoteHostView.sshDestination?: string`. Task 4's main.ts `remoteHost` dep reads `entry.sshDestination`.
- Go CLI compatibility: **verified by the AO-82 spike against the real binary** — `lookupRemoteEntry` is the store's only Go reader, tolerates unknown fields, and there is no Go writer to drop them (EDD § "Store shape"). No Go change.

- [ ] **Step 1: Write the failing store test**

Append to `frontend/src/main/remotes-store.test.ts`, following its existing tmp-file pattern (it writes a remotes.json with mode 0600 and reads it back — reuse its helper for creating the file):

```ts
	it("round-trips an SSH destination and keeps hosts without one unchanged", async () => {
		const file = await writeRemotesFixture([
			{ label: "Mini", url: "http://192.0.2.1:3011", password: "pw", sshDestination: "aron@mini.local" },
			{ label: "Plain", url: "http://192.0.2.2:3011", password: "pw" },
		]); // adapt to this file's fixture helper name; keep contents verbatim
		const remotes = await readRemotes(file);
		expect(remotes[0].sshDestination).toBe("aron@mini.local");
		expect(remotes[1].sshDestination).toBeUndefined();
	});

	it("edits, keeps, and clears the SSH destination independently of other fields", async () => {
		const entry = { label: "Mini", url: "http://192.0.2.1:3011", password: "pw", sshDestination: "aron@mini.local" };
		expect(applyRemoteChanges(entry, { label: "Mini2" }).sshDestination).toBe("aron@mini.local");
		expect(applyRemoteChanges(entry, { sshDestination: "aron@10.0.0.9" }).sshDestination).toBe("aron@10.0.0.9");
		// Empty string is the dialog's "remove it" — stored as absent, not "".
		expect(applyRemoteChanges(entry, { sshDestination: "" }).sshDestination).toBeUndefined();
		expect(applyRemoteChanges(entry, { sshDestination: "  aron@mini.local  " }).sshDestination).toBe("aron@mini.local");
	});
```

- [ ] **Step 2: Run to verify it fails**

Run: `node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remotes-store.test.ts`
Expected: FAIL — type error (`sshDestination` not on `RemoteEntry`) and undefined behavior in `applyRemoteChanges`.

- [ ] **Step 3: Implement the store field**

In `frontend/src/main/remotes-store.ts`:

```ts
export type RemoteEntry = {
	label: string;
	url: string;
	password: string;
	/**
	 * Optional `user@host` the desktop app uses to SSH-attach a local editor to
	 * this host's workspaces. Uses the user's own ssh config/keys — no credential
	 * is stored here. The Go CLI tolerates and ignores unknown fields in this
	 * file (verified in the AO-82 spike; cli/remote.go has no writer to drop it).
	 */
	sshDestination?: string;
};
```

In `applyRemoteChanges`, add one line to the returned object:

```ts
		sshDestination: (changes.sshDestination ?? entry.sshDestination)?.trim() || undefined,
```

(`""` clears — `"" ?? x` keeps the `""`, then `|| undefined` drops it; an omitted field keeps the saved value, preserving the structured-clone-of-undefined rule documented above the function.)

In `addRemote`, normalize the same way so a freshly added host never stores `""`:

```ts
export async function addRemote(path: string, entry: RemoteEntry): Promise<void> {
	const existing = await readRemotes(path);
	const normalized = { ...entry, sshDestination: entry.sshDestination?.trim() || undefined };
	await writeRemotes(path, [...existing.filter((candidate) => candidate.url !== entry.url), normalized]);
}
```

- [ ] **Step 4: Surface it renderer-side (never the password)**

`frontend/src/main/remotes-ipc.ts` — widen the view and mapping:

```ts
export type RemoteHostView = {
	label: string;
	url: string;
	/** Present when the user configured SSH-attach for this host. Not a secret. */
	sshDestination?: string;
};

export function toHostViews(entries: RemoteEntry[]): RemoteHostView[] {
	return entries.map(({ label, url, sshDestination }) => ({ label, url, ...(sshDestination ? { sshDestination } : {}) }));
}
```

`frontend/src/preload.ts` — add `sshDestination?: string` to both inline types: the `add:` input (`:545`) and the `update:` changes (`:552`).

`frontend/src/renderer/hooks/useRemoteHosts.ts:28` — `export type RemoteHostView = { label: string; url: string; sshDestination?: string };`

- [ ] **Step 5: Write the failing dialog test, then add the field**

Append to `frontend/src/renderer/components/AddRemoteHostDialog.test.tsx` (reuse its existing render/mocks — it mocks `aoBridge.remotes`; follow the file's first test's arrangement):

```ts
	it("saves an SSH destination with the host and prefills it on edit", async () => {
		renderDialog(); // the file's existing helper for the add form
		await userEvent.type(screen.getByLabelText(/name/i), "Mini");
		await userEvent.type(screen.getByLabelText(/address/i), "192.0.2.1:3011");
		await userEvent.type(screen.getByLabelText(/password/i), "pw");
		await userEvent.type(screen.getByLabelText(/ssh destination/i), "aron@mini.local");
		await userEvent.click(screen.getByRole("button", { name: /^connect$/i }));
		await waitFor(() =>
			expect(addMock).toHaveBeenCalledWith(
				expect.objectContaining({ url: "http://192.0.2.1:3011", sshDestination: "aron@mini.local" }),
			),
		);
	});
```

Then in `AddRemoteHostDialog.tsx`:
- state: `const sshId = useId();` and `const [sshDestination, setSshDestination] = useState("");`
- prefill effect (`:95-100`): add `setSshDestination(open ? (editing?.sshDestination ?? "") : "");` and `editing?.sshDestination` to the dep array.
- submit: include `sshDestination: sshDestination.trim()` in **both** payloads — the `update(...)` changes object and the `add({...})` entry (the store normalizes `""` to absent; on edit, always sending it makes clearing the field actually clear the value).
- JSX, after the password field's `</div>`:

```tsx
						<div className="flex flex-col gap-1.5">
							<label className="settings-field-label" htmlFor={sshId}>
								{t("hosts.add.ssh")}
							</label>
							<input
								id={sshId}
								autoComplete="off"
								spellCheck={false}
								className="settings-field-control h-(--size-settings-action-height) font-mono"
								value={sshDestination}
								onChange={(event) => {
									setError(null);
									setSshDestination(event.target.value);
								}}
							/>
							<p className="text-caption leading-4 text-settings-muted">
								{t("hosts.add.sshHint")}
							</p>
						</div>
```

i18n — all 8 locales, after `"hosts.add.passwordHint"`:

| locale | `hosts.add.ssh` | `hosts.add.sshHint` |
|---|---|---|
| en | `SSH destination (optional)` | `user@host for opening this host's workspaces in your editor. Uses your own SSH keys; verify the host once with ssh in a terminal first.` |
| de | `SSH-Ziel (optional)` | `user@host, um Arbeitsbereiche dieses Hosts im Editor zu öffnen. Nutzt deine eigenen SSH-Schlüssel; den Host zuerst einmal per ssh im Terminal verifizieren.` |
| es | `Destino SSH (opcional)` | `user@host para abrir los espacios de trabajo de este host en tu editor. Usa tus propias claves SSH; verifica el host una vez con ssh en una terminal.` |
| fr | `Destination SSH (facultatif)` | `user@host pour ouvrir les espaces de travail de cet hôte dans votre éditeur. Utilise vos clés SSH ; vérifiez l'hôte une fois avec ssh dans un terminal.` |
| ja | `SSH 接続先（任意）` | `このホストのワークスペースをエディターで開くための user@host。自分の SSH 鍵を使用します。最初に一度ターミナルの ssh でホストを確認してください。` |
| ko | `SSH 대상(선택)` | `이 호스트의 워크스페이스를 에디터에서 열기 위한 user@host. 자신의 SSH 키를 사용하며, 먼저 터미널에서 ssh로 호스트를 한 번 확인하세요.` |
| pt-BR | `Destino SSH (opcional)` | `user@host para abrir os workspaces deste host no seu editor. Usa suas próprias chaves SSH; verifique o host uma vez com ssh em um terminal.` |
| zh-CN | `SSH 目标（可选）` | `用于在编辑器中打开该主机工作区的 user@host。使用你自己的 SSH 密钥；请先在终端用 ssh 验证一次该主机。` |

(The "verify with ssh once" copy is the spike's host-key stance: `BatchMode=yes` delegation to the user's own `known_hosts` — the editor's Remote-SSH does the same.)

- [ ] **Step 6: Run tests + typecheck**

Run: `node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/remotes-store.test.ts src/main/remotes-ipc.test.ts src/renderer/components/AddRemoteHostDialog.test.tsx src/renderer/i18n && node_modules/.bin/tsc --noEmit`
Expected: PASS; 0 tsc lines.

- [ ] **Step 7: Commit**

```bash
rm -f frontend/node_modules packages/product-ui/node_modules
git add -A && git commit -m "feat(hosts): optional SSH destination on saved hosts"
ln -sfn ~/dev/agent-orchestrator/frontend/node_modules frontend/node_modules
ln -sfn ~/dev/agent-orchestrator/packages/product-ui/node_modules packages/product-ui/node_modules
```

---

### Task 4: Remote open strategies (VS Code-family folder URIs)

**Files:**
- Modify: `frontend/src/main/editor-handoff.ts`
- Modify: `frontend/src/main.ts` (`remoteHost` dep reads the saved entry)
- Modify: `frontend/src/renderer/components/TopbarOpenEditorButton.tsx` (needs-ssh title)
- Test: `frontend/src/main/editor-handoff.test.ts`, `frontend/src/renderer/components/TopbarOpenEditorButton.test.tsx`

**Interfaces:**
- Consumes: `resolveWorkspace(host, sessionId)` remote branch (Task 2); `RemoteHostInfo.sshDestination` populated from `RemoteEntry.sshDestination` (Task 3); i18n `editor.remoteNeedsSsh` (Task 1).
- Produces: remote `getState` → `targets` filtered to remote-capable editors, `workspaceAvailable` true when SSH configured and the remote daemon confirms the path; remote `open` → `launch(<editor-cli>, ["--folder-uri", "vscode-remote://ssh-remote+<dest><path>"], <homeDir>)`.

**Scope cuts (deliberate, name them in the PR):** remote Finder and remote terminal targets are excluded (no SSH-attach analog for Finder; terminal needs per-platform `ssh -t` wrapping — follow-up). Zed and JetBrains excluded from v1 (different remote CLI shapes / Gateway). Dock-only editor installs (resolved via `open -a`, i.e. `argsBeforeWorkspace` present) are excluded from remote open — the `--folder-uri` flag needs the real CLI binary; the state simply lists them as unavailable remotely.

- [ ] **Step 1: Write the failing tests**

Append to `frontend/src/main/editor-handoff.test.ts`:

```ts
	const MINI = { label: "Mini", sshDestination: "aron@mini.local" };
	const REMOTE_HOST = "http://192.0.2.1:3011";

	it("offers only CLI-resolved VS Code-family editors for a remote host", async () => {
		// Fixture: vscode resolves via /bin/code (CLI), cursor via /Applications
		// (open -a) — only the CLI one can take --folder-uri.
		const handoff = createEditorHandoff(deps({ remoteHost: vi.fn().mockResolvedValue(MINI) }));
		const state = await handoff.getState({ host: REMOTE_HOST, sessionId: "adopt-14732" });
		expect(state.targets.map(({ id }) => id)).toEqual(["vscode"]);
		expect(state.remote).toEqual({ hostLabel: "Mini", sshConfigured: true });
		expect(state.workspaceAvailable).toBe(true);
	});

	it("opens a remote workspace as an ssh-remote folder URI from the home directory", async () => {
		const input = deps({
			remoteHost: vi.fn().mockResolvedValue(MINI),
			resolveWorkspace: vi.fn().mockResolvedValue("/Users/stormbreaker/Desktop/skyvern-cloud"),
		});
		const handoff = createEditorHandoff(input);
		await expect(handoff.open({ host: REMOTE_HOST, sessionId: "adopt-14732", targetId: "vscode" }))
			.resolves.toMatchObject({ id: "vscode", kind: "editor" });
		expect(input.resolveWorkspace).toHaveBeenCalledWith(REMOTE_HOST, "adopt-14732");
		expect(input.launch).toHaveBeenCalledWith(
			"/bin/code",
			["--folder-uri", "vscode-remote://ssh-remote+aron@mini.local/Users/stormbreaker/Desktop/skyvern-cloud"],
			"/Users/tester", // a remote path cannot be a local cwd
		);
		expect(input.writePreference).toHaveBeenCalledWith("vscode");
	});

	it("keeps remote state neutral when no SSH destination is configured", async () => {
		const input = deps({ remoteHost: vi.fn().mockResolvedValue({ label: "Mini" }) });
		const handoff = createEditorHandoff(input);
		const state = await handoff.getState({ host: REMOTE_HOST, sessionId: "adopt-14732" });
		expect(state.remote).toEqual({ hostLabel: "Mini", sshConfigured: false });
		expect(state.workspaceAvailable).toBe(false);
		expect(state.unavailableReason).toBeUndefined();
		expect(input.resolveWorkspace).not.toHaveBeenCalled();
		await expect(handoff.open({ host: REMOTE_HOST, sessionId: "adopt-14732", targetId: "vscode" }))
			.rejects.toThrow("Add an SSH destination for Mini to open its workspaces here.");
	});

	it("refuses non-attachable targets on a remote host", async () => {
		const handoff = createEditorHandoff(deps({ remoteHost: vi.fn().mockResolvedValue(MINI) }));
		await expect(handoff.open({ host: REMOTE_HOST, sessionId: "adopt-14732", targetId: "file-manager" }))
			.rejects.toThrow("That open target is not available on Mini.");
	});

	it("reports a workspace the remote daemon cannot confirm as unavailable, in red", async () => {
		const handoff = createEditorHandoff(deps({
			remoteHost: vi.fn().mockResolvedValue(MINI),
			resolveWorkspace: vi.fn().mockRejectedValue(new Error("Session workspace is not available")),
		}));
		const state = await handoff.getState({ host: REMOTE_HOST, sessionId: "adopt-14732" });
		expect(state.workspaceAvailable).toBe(false);
		expect(state.unavailableReason).toBe("Session workspace is not available");
	});
```

(Task 1's "refuses to open a remote workspace" test asserted the interim message — update that test now to expect the needs-SSH message above, since the interim behavior is superseded.)

Append to `TopbarOpenEditorButton.test.tsx`:

```ts
	it("explains what is missing when a remote host has no SSH destination", async () => {
		setState({
			...availableState,
			targets: [{ id: "vscode", name: "VS Code", kind: "editor" }],
			preferredEditorId: "vscode",
			workspaceAvailable: false,
			remote: { hostLabel: "Mini", sshConfigured: false },
		});
		renderButton("http://192.0.2.1:3011");
		const button = await screen.findByRole("button", { name: "Open in VS Code" });
		expect(button).toBeDisabled();
		expect(button).toHaveAttribute("title", "To open it here, add an SSH destination for Mini in its host settings.");
		expect(screen.queryByText(/session workspace is not available/i)).not.toBeInTheDocument();
	});

	it("enables remote open once SSH is configured and the workspace is confirmed", async () => {
		setState({
			...availableState,
			targets: [{ id: "vscode", name: "VS Code", kind: "editor" }],
			preferredEditorId: "vscode",
			remote: { hostLabel: "Mini", sshConfigured: true },
		});
		renderButton("http://192.0.2.1:3011");
		const button = await screen.findByRole("button", { name: "Open in VS Code" });
		expect(button).toBeEnabled();
		await userEvent.click(button);
		await waitFor(() => expect(openMock).toHaveBeenCalledWith({ host: "http://192.0.2.1:3011", sessionId: "sess-1" }));
	});
```

- [ ] **Step 2: Run to verify they fail**

Run: `node_modules/.bin/vitest run --config vite.renderer.config.ts src/main/editor-handoff.test.ts src/renderer/components/TopbarOpenEditorButton.test.tsx`
Expected: FAIL — remote getState still returns unfiltered targets with `sshConfigured: false`; remote open still throws the Task-1 message.

- [ ] **Step 3: Implement the remote strategy in editor-handoff.ts**

Above `createEditorHandoff`:

```ts
// VS Code-family CLIs accept --folder-uri with a vscode-remote ssh URI; the
// flag needs the real CLI binary, so a Dock-only install (resolved through
// `open -a`, i.e. argsBeforeWorkspace present) does not qualify.
const REMOTE_CAPABLE_EDITORS = new Set<EditorId>(["vscode", "vscode-insiders", "vscodium", "cursor", "windsurf"]);

function supportsRemoteOpen(id: OpenTargetId, command: ResolvedCommand): boolean {
	return REMOTE_CAPABLE_EDITORS.has(id as EditorId) && !command.argsBeforeWorkspace;
}
```

Replace the Task-1 remote early-return in `getState` with:

```ts
			if (remote) {
				const remoteTargets = editors
					.filter(({ target, command }) => supportsRemoteOpen(target.id, command))
					.map(({ target }) => target);
				const remoteState = { hostLabel: remote.label, sshConfigured: Boolean(remote.sshDestination) };
				if (!remote.sshDestination) {
					// Not an error: nothing is broken, one setting is absent.
					return { targets: remoteTargets, preferredEditorId, workspaceAvailable: false, remote: remoteState };
				}
				try {
					await deps.resolveWorkspace(host, sessionId);
					return { targets: remoteTargets, preferredEditorId, workspaceAvailable: true, remote: remoteState };
				} catch (error) {
					// The host itself says the workspace is gone — that IS an error.
					return {
						targets: remoteTargets,
						preferredEditorId,
						workspaceAvailable: false,
						unavailableReason: workspaceUnavailable(error),
						remote: remoteState,
					};
				}
			}
```

Replace the Task-1 remote guard in `open` with:

```ts
			const remote = await deps.remoteHost(input.host);
			if (remote) {
				if (!remote.sshDestination) {
					throw new Error(`Add an SSH destination for ${remote.label} to open its workspaces here.`);
				}
				const capable = editors.find(({ target: candidate, command }) =>
					candidate.id === targetId && supportsRemoteOpen(candidate.id, command));
				if (!capable) throw new Error(`That open target is not available on ${remote.label}.`);
				const workspacePath = await deps.resolveWorkspace(input.host, sessionId);
				const folderUri = `vscode-remote://ssh-remote+${remote.sshDestination}${workspacePath}`;
				try {
					// cwd is home: the workspace path exists on the other machine.
					await deps.launch(capable.command.command, ["--folder-uri", folderUri], deps.homeDir);
				} catch (error) {
					deps.logError?.(`failed to open remote session target ${capable.target.id}`, error);
					throw new Error(`Could not open ${capable.target.name}. Check that it is installed and try again.`);
				}
				await deps.writePreference(capable.target.id as EditorId);
				return capable.target;
			}
```

(placed before the local `resolveTarget` lookup; the local path is untouched below it).

- [ ] **Step 4: Feed sshDestination from the saved entry in main.ts**

Replace the Task-1 `remoteHost` dep impl with (import `readRemotes` is already in main.ts for the remotes IPC — verify; `remotesFilePath()` likewise):

```ts
	remoteHost: async (host: string) => {
		if (host === "local") return null;
		const view = registry.views().find((candidate) => candidate.url === host);
		const entry = (await readRemotes(remotesFilePath()).catch(() => []))
			.find((candidate) => candidate.url === host);
		return {
			label: view?.label ?? entry?.label ?? host,
			...(entry?.sshDestination ? { sshDestination: entry.sshDestination } : {}),
		};
	},
```

- [ ] **Step 5: Surface the needs-SSH title in the button**

In `TopbarOpenEditorButton.tsx`, replace the Task-1 `remoteNotice`/`guidance` derivation with the final rule — needs-SSH when an editor that *could* attach is installed, plain "Workspace is on X" when none is, and a red `unavailableReason` only when the remote host itself confirmed the workspace missing (`sshConfigured` true but unavailable):

```ts
	const remote = state?.remote ?? null;
	const remoteNotice = remote && !workspaceAvailable
		? remote.sshConfigured
			? null // the host itself said the workspace is gone — fall through to red guidance
			: targets.length > 0
				? t("editor.remoteNeedsSsh", { host: remote.hostLabel })
				: t("editor.remoteOn", { host: remote.hostLabel })
		: null;
	const guidance = !stateQuery.isPending && !workspaceAvailable && (!remote || remote.sshConfigured)
		? (state?.unavailableReason ?? (remote ? null : t("editor.workspaceUnavailable")))
		: !stateQuery.isPending && !remote && editors.length === 0
			? t("editor.noEditorGuidance", { fileManager: fileManagerName, terminal: terminalName })
			: null;
```

- [ ] **Step 6: Run tests + typecheck + full renderer suite**

Run: `node_modules/.bin/vitest run --config vite.renderer.config.ts src/main src/renderer/components/TopbarOpenEditorButton.test.tsx src/renderer/i18n && node_modules/.bin/tsc --noEmit`
Then the full sweep: `node_modules/.bin/vitest run --config vite.renderer.config.ts src/renderer`
Expected: PASS (modulo the two known pre-existing flakes listed in Global Constraints — verify any failure against that list by reachability before investigating).

- [ ] **Step 7: Live verification against a real remote host** (requires the user's Mini)

1. Restart the dev app (`rs` in the Forge terminal — main/preload changed).
2. Edit the Mini host in the app, set SSH destination (e.g. `aron@mini.local`), confirm `ssh aron@mini.local true` works from a terminal first.
3. Open a Mini session in the app: the topbar button must be enabled; click → local VS Code/Cursor opens attached to `ssh-remote+…` with the session worktree.
4. Empirical flag check if an editor misbehaves: `code --help | grep folder-uri` / `cursor --help | grep folder-uri` — if a fork lacks the flag, remove its id from `REMOTE_CAPABLE_EDITORS` and note it in the PR.
5. Also confirm a local session's button still opens locally (regression).

Record the observed results (not assumptions) in the PR body.

- [ ] **Step 8: Commit, push, open the draft PR**

```bash
rm -f frontend/node_modules packages/product-ui/node_modules
git add -A && git commit -m "feat(hosts): open remote session workspaces via SSH-attached local editors"
git push -u origin ao/agent-orchestrator-107/remote-editor-handoff
```

PR: draft, base `develop`, titled `feat(hosts): SSH-attach editor handoff for remote sessions`. Body: root cause (host-blind handoff + namespace gap), the decision record from this plan's header (B rejected and why; AO-82 relationship), scope cuts (Finder/terminal/Zed/JetBrains/Dock-only installs), test evidence, and the live-verification observations from Step 7.

---

## Self-review (done at authoring)

- **Spec coverage:** neutral remote state (T1), path over gated API (T2), SSH destination config (T3), attach + guards (T4); `/desktop` untouched everywhere; renderer never receives a path (state carries only booleans/labels — checked each state shape above).
- **Placeholders:** two deliberate adapt-to-fixture notes remain (Go test harness helper name in T2S1, remotes-store fixture helper in T3S1) — both point at the exact file whose existing first test supplies the pattern, with assertions given verbatim; everything else is literal code.
- **Type consistency:** `remoteHost` returns `RemoteHostInfo | null` in T1S4, consumed with `.sshDestination` in T4S3; `resolveWorkspace(host, sessionId)` consistent across T1S4/T2S5/T4S1; `EditorHandoffState.remote` shape identical in T1S3/T4S3; i18n keys `editor.remoteOn`/`editor.remoteNeedsSsh` defined in T1S6, consumed in T4S5; `renderButton(host)` variant used by both component test batches.
