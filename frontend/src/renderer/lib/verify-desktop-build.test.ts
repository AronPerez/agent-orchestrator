import { expect, it, vi } from "vitest";
import { verifyDesktopBuild } from "./verify-desktop-build";

it("checks the renderer's own baked build id before allowing the app to mount", async () => {
	const verify = vi.fn(async () => true);
	expect(await verifyDesktopBuild(verify, "0.14.8:commit")).toBe(true);
	expect(verify).toHaveBeenCalledWith("0.14.8:commit");
	expect(await verifyDesktopBuild(async () => false, "old")).toBe(false);
});

it("fails closed for an old or unresponsive main/preload instead of mounting mixed builds", async () => {
	expect(await verifyDesktopBuild(() => { throw new Error("missing IPC"); }, "new")).toBe(false);
	vi.useFakeTimers();
	try {
		const pending = verifyDesktopBuild(() => new Promise(() => undefined), "new");
		await vi.advanceTimersByTimeAsync(5_000);
		expect(await pending).toBe(false);
	} finally { vi.useRealTimers(); }
});
