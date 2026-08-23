import { describe, expect, it, vi } from "vitest";
import { toggleDevToolsForFocusedSurface } from "./devtools-toggle";

const flush = () => new Promise((resolve) => setTimeout(resolve, 0));

describe("toggleDevToolsForFocusedSurface", () => {
	it("opens the shell when there is no browser host at all", () => {
		const shell = vi.fn();
		toggleDevToolsForFocusedSurface(null, shell);
		expect(shell).toHaveBeenCalledOnce();
	});

	it("opens the shell when no browser panel has been focused", async () => {
		const shell = vi.fn();
		toggleDevToolsForFocusedSurface(async () => null, shell);
		await flush();
		expect(shell).toHaveBeenCalledOnce();
	});

	it("leaves the shell alone when a focused panel took the toggle", async () => {
		const shell = vi.fn();
		toggleDevToolsForFocusedSurface(async () => ({ open: true }), shell);
		await flush();
		expect(shell).not.toHaveBeenCalled();
	});

	it("falls back to the shell when the panel toggle rejects", async () => {
		const shell = vi.fn();
		toggleDevToolsForFocusedSurface(async () => {
			throw new Error("view destroyed");
		}, shell);
		await flush();
		expect(shell).toHaveBeenCalledOnce();
	});

	it("falls back to the shell when the panel toggle throws synchronously", async () => {
		const shell = vi.fn();
		toggleDevToolsForFocusedSurface(() => {
			throw new Error("host torn down");
		}, shell);
		await flush();
		expect(shell).toHaveBeenCalledOnce();
	});
});
