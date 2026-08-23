# Terminal Renderer Resilience Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a terminal that loses its GPU renderer report itself — to telemetry and to the user — instead of degrading into a silently blank pane, and capture enough context to identify what kills the renderer in the first place.

**Architecture:** `loadRenderer` moves out of the 1082-line `XtermTerminal.tsx` into a focused, unit-testable module that reports which renderer is live (`webgl` / `canvas` / `none`) through a status callback. `XtermTerminal` translates that status into a telemetry event carrying the fields that discriminate the leading trigger hypotheses, and into an existing-pattern failure surface when no renderer is left. No new UI component: the "no renderer" state reuses the full-pane message block that already handles construction failure.

**Tech Stack:** TypeScript, React 19, `@xterm/xterm` 5.5.0 (`addon-webgl`, `addon-canvas`), Vitest + Testing Library (jsdom), i18next, PostHog via `lib/telemetry.ts`.

**Spec:** No formal spec. This plan implements the two follow-ups explicitly deferred in PR #124's "Scope" section:

- PR #124 — `fix(terminal): load the canvas fallback when the lost WebGL addon won't dispose` (the root-cause fix this builds on)
- `/Users/amongstar/.ao/briefs/debug-terminal-reattach-hang.md` — the investigation brief

## Background — why this work exists

A user reported "the terminal hangs and doesn't load the text" when switching away from a session and back. Root cause, confirmed live: the WebGL context died, xterm fired `onContextLoss`, and `webgl.dispose()` threw out of `AddonManager._wrappedAddonDispose` (reaching an undefined `_isDisposed`). The throw escaped the callback, so `loadCanvasFallback()` never ran and the terminal was left with **no renderer at all**.

What made it cost days rather than minutes:

1. **Every other signal looked healthy.** PTY connected, buffer filling (selecting the "blank" pane copied out the full text), keystrokes reaching the agent. Only drawing was gone.
2. **The only evidence was a `console.warn` and an uncaught `TypeError`** — and on macOS the app shell's DevTools could not be opened at all (fixed separately in PR #119), so neither was reachable.
3. **Nothing was reported anywhere.** No telemetry, no UI, no daemon log.

PR #124 fixed the escape. It did **not** fix the reporting gap, and it did not explain what kills the context. Both are this plan.

## Global Constraints

- **Locale parity is enforced.** `src/renderer/i18n/instance.test.ts:150-154` asserts every key in `en.json` exists in all other catalogs. A new key must be added to **all 8** files: `de.json`, `en.json`, `es.json`, `fr.json`, `ja.json`, `ko.json`, `pt-BR.json`, `zh-CN.json`.
- **No raw English literals in renderer chrome.** `src/renderer/i18n/renderer-coverage.test.ts` fails on user-visible strings that are not translation keys. Route all copy through `t(...)`.
- **Telemetry event names use the `ao.renderer.*` prefix** (see `POSTHOG_EVENT_NAME_ALIASES` in `lib/telemetry.ts:17-23`). New events need no alias entry.
- **Telemetry is rate-limited per event name**: 5/minute and 200/day (`lib/telemetry.ts:38-39`). Do not emit an event on every terminal mount.
- **Run all commands from `frontend/`.** The worktree needs a `node_modules` symlink and ambient AO env cleared:
  ```bash
  ln -sfn /Users/amongstar/dev/agent-orchestrator/frontend/node_modules frontend/node_modules
  cd frontend
  env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_SESSION_ID -u AO_URL npx vitest run <path>
  ```
- **Never `git add` `frontend/node_modules`.** Stage files explicitly by path; never `git add -A`.
- **Never bare `git stash`** — the stash is shared across AO worktrees.
- **Branch naming:** work under `ao/agent-orchestrator-93/<topic>`, based on `origin/develop`.
- **Known-noise tests** (fail on clean `develop`, not your problem): `src/landing/**`, `src/annotate-preload`, and `__tests__/_shell-index-redirect.test.tsx` under machine load.
- **`tsc --noEmit` reports pre-existing errors in `../packages/product-ui`** (no install in this worktree). Only `^src/` errors count: `npx tsc --noEmit -p tsconfig.json 2>&1 | grep '^src/'` must be empty.

## File Structure

| File | Responsibility |
|---|---|
| `src/renderer/lib/terminal-renderer.ts` **(create)** | Owns renderer selection and fallback for one xterm instance. Reports the live renderer through a status callback. The only place that knows about `WebglAddon` / `CanvasAddon`. |
| `src/renderer/lib/terminal-renderer.test.ts` **(create)** | Unit tests for every renderer-status transition, including the throwing-dispose path from PR #124. |
| `src/renderer/components/XtermTerminal.tsx` **(modify)** | Drops its private `loadRenderer`; imports the module, maps status → telemetry + `onError`. |
| `src/renderer/components/XtermTerminal.test.tsx` **(modify)** | Existing renderer tests move to the new module; keeps one integration test that a dead renderer reaches `onError`. |
| `src/renderer/components/TerminalPane.tsx` **(modify)** | Renders the "renderer unavailable" message using the block that already handles construction failure. |
| `src/renderer/components/TerminalPane.test.tsx` **(modify)** | Asserts the message renders when the terminal reports a dead renderer. |
| `src/renderer/i18n/*.json` **(modify, 8 files)** | `terminal.rendererLost` copy. |
| `docs/debugging/terminal-renderer-loss.md` **(create)** | The `WEBGL_lose_context` recipe that reproduces this class on demand. |

Task 1 is self-contained (new module, no callers). Task 2 switches the caller over and adds instrumentation. Task 3 adds the user-facing surface. Task 4 verifies the whole chain against a real GPU and lands the repro recipe.

---

### Task 1: Extract the renderer loader into a testable module

**Files:**
- Create: `frontend/src/renderer/lib/terminal-renderer.ts`
- Test: `frontend/src/renderer/lib/terminal-renderer.test.ts`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type TerminalRendererStatus = "webgl" | "canvas" | "none"`
  - `type RendererStatusListener = (status: TerminalRendererStatus, detail?: unknown) => void`
  - `function loadRenderer(term: Terminal, onStatus: RendererStatusListener): void`

`onStatus` fires once per transition: `"webgl"` when the GPU renderer loads, `"canvas"` when the 2D fallback takes over (initially or after a context loss), `"none"` when neither is drawing. `detail` carries the causing error, or the string `"context-loss"` when the fallback follows a clean dispose.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/renderer/lib/terminal-renderer.test.ts`:

```tsx
import type { Terminal } from "@xterm/xterm";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loadRenderer, type TerminalRendererStatus } from "./terminal-renderer";

const state = vi.hoisted(() => ({
	webglConstructThrows: false,
	webglDisposeThrows: false,
	canvasConstructThrows: false,
	contextLoss: null as null | (() => void),
	canvasLoads: 0,
}));

vi.mock("@xterm/addon-webgl", () => ({
	WebglAddon: class FakeWebglAddon {
		constructor() {
			if (state.webglConstructThrows) throw new Error("WebGL unavailable");
		}
		onContextLoss(listener: () => void) {
			state.contextLoss = listener;
		}
		dispose() {
			// The real failure from PR #124: xterm throws out of AddonManager's
			// dispose chain when the GL context is already gone.
			if (state.webglDisposeThrows) {
				throw new TypeError("Cannot read properties of undefined (reading '_isDisposed')");
			}
		}
	},
}));

vi.mock("@xterm/addon-canvas", () => ({
	CanvasAddon: class FakeCanvasAddon {
		constructor() {
			if (state.canvasConstructThrows) throw new Error("canvas unavailable");
			state.canvasLoads += 1;
		}
	},
}));

function fakeTerminal(): Terminal {
	return { loadAddon: vi.fn() } as unknown as Terminal;
}

function record() {
	const seen: TerminalRendererStatus[] = [];
	return { seen, listener: (status: TerminalRendererStatus) => void seen.push(status) };
}

beforeEach(() => {
	state.webglConstructThrows = false;
	state.webglDisposeThrows = false;
	state.canvasConstructThrows = false;
	state.contextLoss = null;
	state.canvasLoads = 0;
});

describe("loadRenderer", () => {
	it("reports webgl when the GPU renderer loads", () => {
		const { seen, listener } = record();
		loadRenderer(fakeTerminal(), listener);
		expect(seen).toEqual(["webgl"]);
		expect(state.canvasLoads).toBe(0);
	});

	it("falls back to canvas when WebGL is unavailable", () => {
		state.webglConstructThrows = true;
		const { seen, listener } = record();
		loadRenderer(fakeTerminal(), listener);
		expect(seen).toEqual(["canvas"]);
		expect(state.canvasLoads).toBe(1);
	});

	it("swaps to canvas when the GPU context is lost", () => {
		const { seen, listener } = record();
		loadRenderer(fakeTerminal(), listener);
		state.contextLoss?.();
		expect(seen).toEqual(["webgl", "canvas"]);
		expect(state.canvasLoads).toBe(1);
	});

	it("still swaps to canvas when disposing the lost addon throws", () => {
		// The regression from PR #124: the throw used to escape onContextLoss and
		// skip the fallback, leaving the terminal with nothing drawing it.
		state.webglDisposeThrows = true;
		const { seen, listener } = record();
		loadRenderer(fakeTerminal(), listener);
		state.contextLoss?.();
		expect(seen).toEqual(["webgl", "canvas"]);
		expect(state.canvasLoads).toBe(1);
	});

	it("reports none when neither renderer can draw", () => {
		state.webglConstructThrows = true;
		state.canvasConstructThrows = true;
		const { seen, listener } = record();
		loadRenderer(fakeTerminal(), listener);
		expect(seen).toEqual(["none"]);
	});

	it("passes the causing error as detail when nothing is drawing", () => {
		state.webglConstructThrows = true;
		state.canvasConstructThrows = true;
		const details: unknown[] = [];
		loadRenderer(fakeTerminal(), (_status, detail) => void details.push(detail));
		expect((details[0] as Error).message).toBe("canvas unavailable");
	});

	it("only installs the canvas fallback once across repeated context losses", () => {
		const { seen, listener } = record();
		loadRenderer(fakeTerminal(), listener);
		state.contextLoss?.();
		state.contextLoss?.();
		expect(state.canvasLoads).toBe(1);
		expect(seen).toEqual(["webgl", "canvas"]);
	});
});
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd frontend
env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_SESSION_ID -u AO_URL \
  npx vitest run src/renderer/lib/terminal-renderer.test.ts
```

Expected: FAIL — `Failed to resolve import "./terminal-renderer"`.

- [ ] **Step 3: Write the implementation**

Create `frontend/src/renderer/lib/terminal-renderer.ts`:

```ts
import { CanvasAddon } from "@xterm/addon-canvas";
import { WebglAddon } from "@xterm/addon-webgl";
import type { Terminal } from "@xterm/xterm";

/** Which renderer is currently drawing a terminal, if any. */
export type TerminalRendererStatus = "webgl" | "canvas" | "none";

/**
 * Called once per renderer transition. `detail` carries the error that forced
 * the change, or the string "context-loss" when the GPU context went away
 * without an accompanying failure.
 */
export type RendererStatusListener = (status: TerminalRendererStatus, detail?: unknown) => void;

// Prefer the WebGL renderer, fall back to 2D canvas. Both rasterize box-drawing
// glyphs themselves onto a fixed cell grid; the DOM renderer does not, so TUI
// borders would drift. Loaded after open().
//
// A terminal with no renderer is invisible rather than obviously broken: the
// PTY stays connected, the buffer keeps filling, keystrokes still reach the
// agent, and selecting the "empty" pane copies out the full text. Only drawing
// stops. That is why every exit reports a status instead of a console warning.
export function loadRenderer(term: Terminal, onStatus: RendererStatusListener): void {
	let fallbackLoaded = false;
	const loadCanvasFallback = (detail?: unknown) => {
		if (fallbackLoaded) return;
		fallbackLoaded = true;
		try {
			term.loadAddon(new CanvasAddon());
			onStatus("canvas", detail);
		} catch (error) {
			onStatus("none", error);
		}
	};

	try {
		const webgl = new WebglAddon();
		webgl.onContextLoss(() => {
			// xterm can throw disposing a renderer whose GL context is already gone
			// (AddonManager._wrappedAddonDispose reaching an undefined
			// `_isDisposed`). Dispose is a courtesy; the fallback is the point.
			let disposeError: unknown;
			try {
				webgl.dispose();
			} catch (error) {
				disposeError = error;
			}
			loadCanvasFallback(disposeError ?? "context-loss");
		});
		term.loadAddon(webgl);
		onStatus("webgl");
	} catch (error) {
		// WebGL context unavailable — fall through to the canvas renderer.
		loadCanvasFallback(error);
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd frontend
env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_SESSION_ID -u AO_URL \
  npx vitest run src/renderer/lib/terminal-renderer.test.ts
```

Expected: PASS — 7 tests.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/renderer/lib/terminal-renderer.ts frontend/src/renderer/lib/terminal-renderer.test.ts
git commit -m "refactor(terminal): extract renderer loading with status reporting"
```

---

### Task 2: Report renderer status to telemetry from XtermTerminal

**Files:**
- Modify: `frontend/src/renderer/components/XtermTerminal.tsx` — delete the private `loadRenderer` (lines 85-111), update the call site (line 480), add the status handler
- Test: `frontend/src/renderer/components/XtermTerminal.test.tsx`

**Interfaces:**
- Consumes: `loadRenderer(term, onStatus)` and `TerminalRendererStatus` from Task 1.
- Produces: telemetry event `ao.renderer.terminal_renderer` with properties `{ status: TerminalRendererStatus, visible: boolean, ageMs: number, detail?: string }`. `XtermTerminal` calls its existing `onError?: (error: unknown) => void` prop when status is `"none"`.

**Why these properties:** the leading hypothesis for what kills the context is that terminal retention re-parents hosts between an off-screen parking div (`TerminalPane.tsx:623`) and the pane slot on every session switch. `visible` (false while parked) and `ageMs` (how long the terminal lived before losing its renderer) are what confirm or refute that. Without them the event says a loss happened but not why.

- [ ] **Step 1: Write the failing test**

In `frontend/src/renderer/components/XtermTerminal.test.tsx`, replace the existing `@xterm/addon-webgl` and `@xterm/addon-canvas` mocks with versions that expose the context-loss listener, add the telemetry mock, and add the two tests below.

Add to the top of the file, after the existing imports:

```tsx
vi.mock("../lib/telemetry", () => ({
	captureRendererEvent: vi.fn(async () => undefined),
	captureRendererException: vi.fn(async () => undefined),
}));
```

Add these fields to the existing `state` object created by `vi.hoisted`:

```tsx
	canvasAddonLoads: 0,
	webglDisposeThrows: false,
	canvasConstructThrows: false,
	webglContextLoss: null as null | (() => void),
```

Replace the two addon mocks with:

```tsx
vi.mock("@xterm/addon-canvas", () => ({
	CanvasAddon: class FakeCanvasAddon {
		constructor() {
			if (state.canvasConstructThrows) throw new Error("canvas unavailable");
			state.canvasAddonLoads += 1;
		}
	},
}));

vi.mock("@xterm/addon-webgl", () => ({
	WebglAddon: class FakeWebglAddon {
		onContextLoss(listener: () => void) {
			state.webglContextLoss = listener;
		}
		dispose() {
			if (state.webglDisposeThrows) {
				throw new TypeError("Cannot read properties of undefined (reading '_isDisposed')");
			}
		}
	},
}));
```

Add to the existing `beforeEach` inside `describe("XtermTerminal", ...)`:

```tsx
		state.canvasAddonLoads = 0;
		state.webglDisposeThrows = false;
		state.canvasConstructThrows = false;
		state.webglContextLoss = null;
		vi.mocked(captureRendererEvent).mockClear();
```

Import the mocked function near the other imports:

```tsx
import { captureRendererEvent } from "../lib/telemetry";
```

Then add both tests immediately before `it("preserves the agent TUI palette without contrast remapping", ...)`:

```tsx
	it("reports a renderer downgrade to telemetry with the context that explains it", async () => {
		render(<XtermTerminal isVisible={false} theme="dark" />);
		// A healthy WebGL mount must not spend a rate-limit slot.
		expect(captureRendererEvent).not.toHaveBeenCalled();

		await act(async () => {
			state.webglContextLoss?.();
		});

		expect(captureRendererEvent).toHaveBeenCalledWith(
			"ao.renderer.terminal_renderer",
			expect.objectContaining({ status: "canvas", visible: false }),
		);
	});

	it("tells its owner when no renderer is left to draw the terminal", async () => {
		state.canvasConstructThrows = true;
		const onError = vi.fn();
		render(<XtermTerminal onError={onError} theme="dark" />);

		await act(async () => {
			state.webglContextLoss?.();
		});

		expect(onError).toHaveBeenCalledOnce();
		expect(captureRendererEvent).toHaveBeenCalledWith(
			"ao.renderer.terminal_renderer",
			expect.objectContaining({ status: "none" }),
		);
	});
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd frontend
env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_SESSION_ID -u AO_URL \
  npx vitest run src/renderer/components/XtermTerminal.test.tsx -t "renderer"
```

Expected: FAIL — `captureRendererEvent` never called (the component still uses its private `loadRenderer`, which reports nothing).

- [ ] **Step 3: Write the implementation**

In `frontend/src/renderer/components/XtermTerminal.tsx`:

1. Delete the private `loadRenderer` function and its comment block (lines 85-111, from `// Prefer the WebGL renderer` through the closing `}`).
2. Delete the now-unused imports of `CanvasAddon` (line 25) and `WebglAddon` (line 30).
3. Add these imports alongside the other `../lib/` imports:

```tsx
import { loadRenderer, type TerminalRendererStatus } from "../lib/terminal-renderer";
import { captureRendererEvent, captureRendererException } from "../lib/telemetry";
```

4. Replace the call site at line 480, `loadRenderer(term);`, with:

```tsx
		const rendererLoadedAt = Date.now();
		loadRenderer(term, (status: TerminalRendererStatus, detail?: unknown) => {
			// A healthy WebGL mount is the overwhelming majority and would burn the
			// per-name rate limit (5/min) for no signal. Only downgrades are news.
			if (status === "webgl") return;
			void captureRendererEvent("ao.renderer.terminal_renderer", {
				status,
				// False while the terminal is parked off-screen. Retention re-parents
				// hosts between the parking div and the pane slot on every session
				// switch, so this is what tells us whether that is the trigger.
				visible: callbacksRef.current.isVisible !== false,
				ageMs: Date.now() - rendererLoadedAt,
				detail: detail instanceof Error ? detail.message : typeof detail === "string" ? detail : undefined,
			});
			if (status !== "none") return;
			void captureRendererException(
				detail instanceof Error ? detail : new Error("terminal renderer unavailable"),
				{ source: "xterm-renderer", operation: "load_renderer" },
			);
			// Nothing is drawing this terminal. The owner surfaces it; without this
			// the pane just goes blank while everything else keeps working.
			callbacksRef.current.onError?.(detail);
		});
```

**Note for the implementer:** `callbacksRef` holds the entire props object (`callbacksRef.current = props`, line 326), so both `onError` and `isVisible` are already available on it — `callbacksRef.current.onError?.(error)` is used at line 448 today. Always read props through this ref inside the effect: it has an empty dependency array, so a directly captured prop would go stale.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd frontend
env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_SESSION_ID -u AO_URL \
  npx vitest run src/renderer/components/XtermTerminal.test.tsx
```

Expected: PASS — all tests in the file, including the two new ones.

- [ ] **Step 5: Remove the now-duplicated test**

Task 1 covers the fallback-on-throwing-dispose case at the unit level. Delete the test named `"loads the canvas fallback even when disposing the lost WebGL renderer throws"` from `XtermTerminal.test.tsx` (added in PR #124) — `terminal-renderer.test.ts` asserts the same behavior against the extracted module, and the component test now covers the reporting instead.

Re-run and confirm still green:

```bash
cd frontend
env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_SESSION_ID -u AO_URL \
  npx vitest run src/renderer/components/XtermTerminal.test.tsx
```

- [ ] **Step 6: Verify types and commit**

```bash
cd frontend
env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_SESSION_ID -u AO_URL \
  npx tsc --noEmit -p tsconfig.json 2>&1 | grep '^src/'
```

Expected: no output.

```bash
git add frontend/src/renderer/components/XtermTerminal.tsx frontend/src/renderer/components/XtermTerminal.test.tsx
git commit -m "feat(terminal): report renderer loss to telemetry with trigger context"
```

---

### Task 3: Show the user when a terminal has no renderer

**Files:**
- Modify: `frontend/src/renderer/components/TerminalPane.tsx:1048-1053` (the `initFailed` branch)
- Modify: `frontend/src/renderer/i18n/en.json` and the seven sibling catalogs
- Test: `frontend/src/renderer/components/TerminalPane.test.tsx`

**Interfaces:**
- Consumes: `XtermTerminal`'s `onError` firing on status `"none"` (Task 2).
- Produces: no new exports. `AttachedTerminal` distinguishes construction failure (`terminal.initFailed`) from mid-session renderer loss (`terminal.rendererLost`).

**Why reuse the existing branch:** `AttachedTerminal` already routes `onError` → `handleInitError` → `setInitFailed(true)` → a full-pane message. A terminal with no renderer is equally unusable, so it needs the same surface — only the copy differs, because "failed to initialize" is wrong for a terminal that ran fine for an hour. No new component, no new prop.

- [ ] **Step 1: Add the translation key to all eight catalogs**

Each catalog uses flat dotted keys. Insert `terminal.rendererLost` next to the existing `terminal.initFailed` entry in each file:

```
en.json     "terminal.rendererLost": "Terminal display stopped on this GPU/driver. Reopen the session or restart the app."
de.json     "terminal.rendererLost": "Die Terminalanzeige wurde auf dieser GPU/diesem Treiber beendet. Öffnen Sie die Sitzung erneut oder starten Sie die App neu."
es.json     "terminal.rendererLost": "La pantalla del terminal se detuvo en esta GPU/controlador. Vuelve a abrir la sesión o reinicia la aplicación."
fr.json     "terminal.rendererLost": "L'affichage du terminal s'est arrêté sur ce GPU/pilote. Rouvrez la session ou redémarrez l'application."
ja.json     "terminal.rendererLost": "この GPU/ドライバーでターミナルの表示が停止しました。セッションを開き直すか、アプリを再起動してください。"
ko.json     "terminal.rendererLost": "이 GPU/드라이버에서 터미널 표시가 중단되었습니다. 세션을 다시 열거나 앱을 다시 시작하세요."
pt-BR.json  "terminal.rendererLost": "A exibição do terminal parou nesta GPU/driver. Reabra a sessão ou reinicie o app."
zh-CN.json  "terminal.rendererLost": "终端显示在此 GPU/驱动上已停止。请重新打开会话或重启应用。"
```

- [ ] **Step 2: Write the failing test**

`TerminalPane.test.tsx` mocks `XtermTerminal`, so drive the failure through that mock. Add `onErrorRef` to the `vi.hoisted` state object:

```tsx
	onErrorRef: { current: undefined as undefined | ((error: unknown) => void) },
```

In the existing `vi.mock("./XtermTerminal", ...)` factory, capture the prop by adding to the component's props type and body:

```tsx
		onError?: (error: unknown) => void;
```
```tsx
		onErrorRef.current = props.onError;
```

Add this test inside `describe("TerminalPane replay cover", ...)`:

```tsx
	it("says the display stopped when the terminal loses its renderer", async () => {
		const view = renderPane({ ...worker, terminalHandleId: "term-1" });
		try {
			await waitFor(() => expect(onErrorRef.current).toBeDefined());

			await act(async () => {
				onErrorRef.current?.(new Error("terminal renderer unavailable"));
			});

			expect(
				screen.getByText("Terminal display stopped on this GPU/driver. Reopen the session or restart the app."),
			).toBeInTheDocument();
			expect(screen.queryByText(/failed to initialize/i)).not.toBeInTheDocument();
		} finally {
			view.restore();
		}
	});
```

- [ ] **Step 3: Run the test to verify it fails**

```bash
cd frontend
env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_SESSION_ID -u AO_URL \
  npx vitest run src/renderer/components/TerminalPane.test.tsx -t "display stopped"
```

Expected: FAIL — the pane renders `terminal.initFailed` copy ("Terminal failed to initialize…"), not the new string.

- [ ] **Step 4: Write the implementation**

In `frontend/src/renderer/components/TerminalPane.tsx`:

1. Replace the `initFailed` boolean state (line 946) with one that records which failure happened:

```tsx
	const [rendererFailure, setRendererFailure] = useState<null | "init" | "lost">(null);
```

2. Replace `handleInitError` (lines 999-1002) with:

```tsx
	const handleInitError = useCallback((err: unknown) => {
		console.error("xterm renderer unavailable", err);
		// Construction never produced a terminal; a lost renderer had one and
		// stopped drawing. Same dead pane, different remedy for the user.
		setRendererFailure((current) => current ?? (terminal ? "lost" : "init"));
	}, [terminal]);
```

3. Update the effect at lines 1003-1008:

```tsx
	useEffect(() => {
		if (rendererFailure) onFatal?.("renderer unavailable");
	}, [rendererFailure, onFatal]);
```

4. Replace the render branch at lines 1048-1053:

```tsx
	if (rendererFailure) {
		return (
			<div className="terminal-surface grid h-full place-items-center p-4 font-mono text-xs text-muted-foreground">
				{rendererFailure === "lost" ? t("terminal.rendererLost") : t("terminal.initFailed")}
			</div>
		);
	}
```

**Note for the implementer:** `terminal` is the `AttachableTerminal` state set by `handleReady` (line 993). It is non-null once xterm has constructed successfully, which is exactly what separates the two cases. Search the file for any other reference to `initFailed` and update it.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
cd frontend
env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_SESSION_ID -u AO_URL \
  npx vitest run src/renderer/components/TerminalPane.test.tsx \
                 src/renderer/i18n/instance.test.ts \
                 src/renderer/i18n/renderer-coverage.test.ts
```

Expected: PASS. `instance.test.ts` proves all eight catalogs carry the new key; `renderer-coverage.test.ts` proves no raw English literal was introduced.

- [ ] **Step 6: Verify types and commit**

```bash
cd frontend
env -u AO_DATA_DIR -u AO_RUN_FILE -u AO_SESSION_ID -u AO_URL \
  npx tsc --noEmit -p tsconfig.json 2>&1 | grep '^src/'
```

Expected: no output.

```bash
git add frontend/src/renderer/components/TerminalPane.tsx \
        frontend/src/renderer/components/TerminalPane.test.tsx \
        frontend/src/renderer/i18n/de.json frontend/src/renderer/i18n/en.json \
        frontend/src/renderer/i18n/es.json frontend/src/renderer/i18n/fr.json \
        frontend/src/renderer/i18n/ja.json frontend/src/renderer/i18n/ko.json \
        frontend/src/renderer/i18n/pt-BR.json frontend/src/renderer/i18n/zh-CN.json
git commit -m "feat(terminal): tell the user when the display stops instead of going blank"
```

---

### Task 4: Verify against a real GPU and land the repro recipe

Unit tests prove the fallback is *called*. They cannot prove xterm actually resumes drawing after a half-disposed WebGL addon is left registered in the `AddonManager` — the mocks never render pixels. That is an open risk in PR #124's fix and this task closes it.

**Files:**
- Create: `docs/debugging/terminal-renderer-loss.md`

**Interfaces:**
- Consumes: everything from Tasks 1-3.
- Produces: no code. A documented recipe and a recorded result.

- [ ] **Step 1: Write the repro recipe**

Create `docs/debugging/terminal-renderer-loss.md`:

````markdown
# Reproducing terminal renderer loss

A terminal that loses its GPU renderer is invisible rather than obviously
broken: the PTY stays connected, the buffer keeps filling, keystrokes still
reach the agent, and selecting the "empty" pane copies out the full text.
Only drawing stops. This recipe induces that state on demand.

## Why you cannot just use the desktop app's console

On macOS the app shell's DevTools could not be opened at all before PR #119:
`Cmd+Option+I` is routed to the Browser panel, and Electron's default
**View → Toggle Developer Tools** throws when no web contents holds focus —
which is precisely the state a dead pane leaves behind.

Use AO's web UI instead, served by the running daemon:

```
http://127.0.0.1:3011
```

Same sessions, same data, a normal browser tab, working DevTools. Verify you
are on the right page before trusting anything you measure — the Browser panel
can be displaying the AO web app too:

```js
!!window.ao   // true only in the Electron shell
```

## Kill the renderer

Open a session whose terminal is rendering, then run this in the Console:

```js
const host = [...document.querySelectorAll('[data-terminal-cache-key]')]
  .find(h => h.style.visibility !== 'hidden');
for (const c of host.querySelectorAll('canvas')) {
  const gl = c.getContext('webgl2') || c.getContext('webgl');
  gl?.getExtension('WEBGL_lose_context')?.loseContext();
}
```

xterm waits about three seconds for the context to come back, gives up, and
logs `webgl context not restored; firing onContextLoss`.

## What should happen

- The terminal keeps drawing, now on the 2D canvas renderer.
- Telemetry records `ao.renderer.terminal_renderer` with `status: "canvas"`,
  plus `visible` and `ageMs`.
- Nothing is shown to the user, because nothing is broken.

## What used to happen (fixed in #124)

`webgl.dispose()` threw out of `AddonManager._wrappedAddonDispose` reaching an
undefined `_isDisposed`. The throw escaped `onContextLoss`, the canvas fallback
never loaded, and the terminal was left with no renderer for the rest of its
life — including across session switches, because retention hands back the same
instance.

```
webgl context not restored; firing onContextLoss
Uncaught Error: Cannot read properties of undefined (reading '_isDisposed')
    at t.AddonManager._wrappedAddonDispose
```

## If no renderer survives

Force it by making the canvas fallback fail too (both renderers gone). The pane
must show "Terminal display stopped on this GPU/driver" rather than going
blank, and telemetry must record `status: "none"`.
````

- [ ] **Step 2: Run the recipe against a real browser**

Start the app or open `http://127.0.0.1:3011`, follow the recipe, and record:

1. Does the terminal **keep drawing** after the induced context loss? This is the open question — if it does not, the canvas addon is not taking over from a half-disposed WebGL addon, and PR #124's fix is incomplete.
2. Does `ao.renderer.terminal_renderer` appear with `status: "canvas"`?
3. Is `visible` `true` or `false`? Repeat the kill while the terminal is **parked** (switch to another session first, then run the snippet against the hidden host) — this is the datum that tests the re-parenting hypothesis.

- [ ] **Step 3: Record the outcome in the doc**

Append a `## Verified` section with the date, the app/browser used, and the answer to each of the three questions above. If the terminal does **not** resume drawing, stop and open an issue — the fallback needs to fully remove the dead addon (`term.loadAddon` on a fresh `Terminal`, or an explicit `AddonManager` cleanup) and that is a new plan, not a patch to this one.

- [ ] **Step 4: Commit**

```bash
git add docs/debugging/terminal-renderer-loss.md
git commit -m "docs: recipe for reproducing terminal renderer loss"
```

---

## After this plan

**The trigger is still unknown.** This plan ships the instrumentation that answers it; it does not answer it. Once `ao.renderer.terminal_renderer` has real data, query PostHog for the `visible` and `ageMs` distribution:

- Losses clustered at `visible: false` → retention's re-parenting between the parking div and the pane slot is the trigger, and the fix belongs in `TerminalPane`'s park/unpack path.
- Losses spread across both → the cause is environmental (driver, GPU pressure, context limits) and the fallback is the whole remedy.

Deliberately out of scope, each worth its own issue:

- **What kills the context.** Gated on the data above.
- **Recovering without a reopen.** Today a terminal with no renderer stays dead until the session is reopened; it could re-attempt `loadRenderer` on activation.
- **The Electron menu crash.** PR #119 makes the macOS shortcut work, but `View → Toggle Developer Tools` still throws when nothing holds focus.
