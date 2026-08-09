import { describe, expect, it, vi } from "vitest";
import { ActiveRemote } from "./active-remote";

const entry = { label: "workbox", url: "http://192.0.2.1:3011", password: "secret" };

function fakeStart() {
	const close = vi.fn(async () => {});
	const start = vi.fn(async (e: typeof entry) => ({ base: "http://127.0.0.1:9999/tok", url: e.url, close }));
	return { start, close };
}

describe("ActiveRemote", () => {
	it("activates and reports a view with no password", async () => {
		const { start } = fakeStart();
		const active = new ActiveRemote(start);
		const view = await active.activate(entry);
		expect(view).toEqual({ label: "workbox", url: entry.url, base: "http://127.0.0.1:9999/tok" });
		expect(JSON.stringify(view)).not.toContain("secret");
		expect(await active.view()).toEqual(view);
	});

	it("activating a second host closes the first proxy", async () => {
		const { start, close } = fakeStart();
		const active = new ActiveRemote(start);
		await active.activate(entry);
		await active.activate({ ...entry, label: "mini", url: "http://192.0.2.9:3011" });
		expect(close).toHaveBeenCalledTimes(1);
		expect((await active.view())?.label).toBe("mini");
	});

	it("deactivate closes and clears", async () => {
		const { start, close } = fakeStart();
		const active = new ActiveRemote(start);
		await active.activate(entry);
		await active.deactivate();
		expect(close).toHaveBeenCalled();
		expect(await active.view()).toBeNull();
	});

	it("deactivate on a fresh instance is a no-op", async () => {
		const { start, close } = fakeStart();
		const active = new ActiveRemote(start);
		await active.deactivate();
		expect(close).not.toHaveBeenCalled();
		expect(start).not.toHaveBeenCalled();
	});

	it("leaves nothing active when starting the proxy fails", async () => {
		const active = new ActiveRemote(async () => {
			throw new Error("refused");
		});
		await expect(active.activate(entry)).rejects.toThrow("refused");
		expect(await active.view()).toBeNull();
	});
});
