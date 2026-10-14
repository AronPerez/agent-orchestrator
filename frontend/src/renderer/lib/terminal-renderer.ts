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
