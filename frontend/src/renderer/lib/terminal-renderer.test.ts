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
