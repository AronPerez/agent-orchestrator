// @vitest-environment node
import { afterEach, expect, it, vi } from "vitest";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, existsSync } from "node:fs";
import path from "node:path";
import os from "node:os";
import { pinRendererResources } from "./renderer-resource-cache";

const temporary: string[] = [];
afterEach(() => { for (const dir of temporary.splice(0)) rmSync(dir, { recursive: true, force: true }); });

function fixture() {
	const dir = mkdtempSync(path.join(os.tmpdir(), "ao-renderer-cache-"));
	temporary.push(dir);
	const source = path.join(dir, "app", ".vite");
	const cache = path.join(dir, ".ao", "electron", "running-renderer");
	mkdirSync(path.join(source, "renderer", "main_window", "assets"), { recursive: true });
	mkdirSync(path.join(source, "build"));
	writeFileSync(path.join(source, "build", "preload.js"), "preload A");
	writeFileSync(path.join(source, "renderer", "main_window", "index.html"), '<script type="module" src="assets/settings.mjs"></script>');
	writeFileSync(path.join(source, "renderer", "main_window", "assets", "settings.mjs"), 'export default () => "Settings from A";');
	return { source, cache };
}

it("opens an unloaded lazy Settings view from the running build after the bundle is replaced", async () => {
	const { source, cache } = fixture();
	const pinned = await pinRendererResources(source, cache, async () => true);
	// No renderer chunk was loaded before the external installer replaced A.
	rmSync(source, { recursive: true });
	mkdirSync(path.join(source, "renderer", "main_window", "assets"), { recursive: true });
	writeFileSync(path.join(source, "renderer", "main_window", "assets", "settings.mjs"), 'export default () => "Settings from B";');
	const bytes = readFileSync(path.join(pinned.root, "renderer", "main_window", "assets", "settings.mjs"));
	const view = await import(`data:text/javascript;base64,${bytes.toString("base64")}`);
	expect(view.default()).toBe("Settings from A");
	expect(readFileSync(path.join(pinned.root, "build", "preload.js"), "utf8")).toBe("preload A");
	expect(readFileSync(path.join(pinned.root, "renderer", "main_window", "index.html"), "utf8")).toContain("settings.mjs");
	pinned.dispose();
	expect(existsSync(cache)).toBe(false);
});

it.each(["before", "during"])("refuses a cache when the source changes %s copying", async (when) => {
	const { source, cache } = fixture();
	const current = vi.fn().mockResolvedValueOnce(when === "during").mockResolvedValue(false);
	await expect(pinRendererResources(source, cache, current)).rejects.toThrow(/build.*changed/i);
	expect(existsSync(cache)).toBe(false);
});
