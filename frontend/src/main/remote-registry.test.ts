import { describe, expect, it, vi } from "vitest";
import { RemoteRegistry } from "./remote-registry";

const a = { label: "workbox", url: "http://192.0.2.1:3011", password: "pw" };
const b = { label: "mini", url: "http://192.0.2.9:3011", password: "pw2" };

function rig() {
	const closes: string[] = [];
	const start = vi.fn(async (e: typeof a) => ({
		base: `http://127.0.0.1:9/${e.label}`,
		url: e.url,
		close: async () => {
			closes.push(e.url);
		},
	}));
	return { start, closes };
}

describe("RemoteRegistry", () => {
	it("keeps several hosts connected at once", async () => {
		const { start } = rig();
		const registry = new RemoteRegistry(start);
		await registry.connect(a);
		await registry.connect(b);
		expect(registry.views().map((v) => v.label).sort()).toEqual(["mini", "workbox"]);
		expect(start).toHaveBeenCalledTimes(2);
	});

	it("connecting the same url twice reuses the live proxy", async () => {
		const { start } = rig();
		const registry = new RemoteRegistry(start);
		const first = await registry.connect(a);
		const second = await registry.connect(a);
		expect(second.base).toBe(first.base);
		expect(start).toHaveBeenCalledTimes(1);
		expect(registry.views()).toHaveLength(1);
	});

	it("disconnect closes only that host's proxy", async () => {
		const { start, closes } = rig();
		const registry = new RemoteRegistry(start);
		await registry.connect(a);
		await registry.connect(b);
		await registry.disconnect(a.url);
		expect(closes).toEqual([a.url]);
		expect(registry.views().map((v) => v.label)).toEqual(["mini"]);
	});

	it("never exposes the password", async () => {
		const { start } = rig();
		const registry = new RemoteRegistry(start);
		await registry.connect(a);
		expect(JSON.stringify(registry.views())).not.toContain("pw");
	});

	it("a host that fails to start does not join the registry", async () => {
		const start = vi.fn(async () => {
			throw new Error("not a daemon");
		});
		const registry = new RemoteRegistry(start);
		await expect(registry.connect(a)).rejects.toThrow(/not a daemon/);
		expect(registry.views()).toEqual([]);
	});

	it("starts one runtime link per connected host and disposes it on disconnect", async () => {
		const { start } = rig();
		const disposed: string[] = [];
		const started: string[] = [];
		const registry = new RemoteRegistry(start, (entry, proxy) => {
			started.push(`${entry.url}|${proxy.base}`);
			return { connected: true, dispose: () => disposed.push(entry.url) };
		});
		await registry.connect(a);
		await registry.connect(a); // reuse — no second link
		expect(started).toEqual([`${a.url}|http://127.0.0.1:9/workbox`]);

		await registry.disconnect(a.url);
		expect(disposed).toEqual([a.url]);
	});

	it("disposes every runtime link on closeAll", async () => {
		const { start } = rig();
		const disposed: string[] = [];
		const registry = new RemoteRegistry(start, (entry) => ({
			connected: true,
			dispose: () => disposed.push(entry.url),
		}));
		await registry.connect(a);
		await registry.connect(b);
		await registry.closeAll();
		expect(disposed.sort()).toEqual([a.url, b.url].sort());
	});

	it("closeAll tears every proxy down", async () => {
		const { start, closes } = rig();
		const registry = new RemoteRegistry(start);
		await registry.connect(a);
		await registry.connect(b);
		await registry.closeAll();
		expect(closes.sort()).toEqual([a.url, b.url].sort());
		expect(registry.views()).toEqual([]);
	});
});
