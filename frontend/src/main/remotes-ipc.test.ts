import { describe, expect, it, vi } from "vitest";
import { chmod, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ActiveRemote } from "./active-remote";
import { removeSavedRemote, toHostViews, updateSavedRemote } from "./remotes-ipc";
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

// A real ActiveRemote over a fake proxy: the close() call is the assertion, and
// only the real class knows when it fires.
function activeOn(url: string, label = "workbox") {
	const close = vi.fn().mockResolvedValue(undefined);
	const active = new ActiveRemote(async () => ({
		base: "http://127.0.0.1:9999/tok",
		url,
		close,
	}));
	return {
		active,
		close,
		activated: active.activate({ label, url, password: "old" }),
	};
}

// No proxy has been started, so nothing is active.
const idleProxy = () => new ActiveRemote(async () => ({ base: "", url: "", close: async () => {} }));

const online = async () => "online" as const;

describe("toHostViews", () => {
	it("strips the password before anything crosses to the renderer", () => {
		const views = toHostViews([
			{
				label: "workbox",
				url: "http://192.0.2.1:3011",
				password: "supersecret",
			},
		]);
		expect(views).toEqual([{ label: "workbox", url: "http://192.0.2.1:3011" }]);
		expect(JSON.stringify(views)).not.toContain("supersecret");
	});
});

describe("updateSavedRemote", () => {
	it("probes the merged entry before it writes anything", async () => {
		const path = await tempFile();
		const probed: RemoteEntry[] = [];
		const health = await updateSavedRemote(
			path,
			"http://192.0.2.1:1",
			{ password: "rotated" },
			idleProxy(),
			async (entry) => {
				probed.push(entry);
				return "online";
			},
		);
		expect(health).toBe("online");
		// Probed with the new password against the saved address, not with either half.
		expect(probed).toEqual([{ label: "workbox", url: "http://192.0.2.1:1", password: "rotated" }]);
	});

	it("saves nothing when the edited host does not answer", async () => {
		const path = await tempFile();
		const health = await updateSavedRemote(
			path,
			"http://192.0.2.1:1",
			{ password: "wrong" },
			idleProxy(),
			async () => "unauthorized",
		);
		expect(health).toBe("unauthorized");
		expect(await readFile(path, "utf8")).toBe(TWO_HOSTS);
	});

	// The live proxy holds the address and password that were saved when it
	// started; after an edit both may be stale, so it does not get to keep serving.
	it("tears down the proxy when the edited host is the active one", async () => {
		const path = await tempFile();
		const { active, close, activated } = activeOn("http://192.0.2.1:1");
		await activated;
		await updateSavedRemote(path, "http://192.0.2.1:1", { url: "http://192.0.2.5:5" }, active, online);
		expect(close).toHaveBeenCalled();
		await expect(active.view()).resolves.toBeNull();
	});

	it("leaves another host's proxy alone", async () => {
		const path = await tempFile();
		const { active, close, activated } = activeOn("http://192.0.2.9:9", "mini");
		await activated;
		await updateSavedRemote(path, "http://192.0.2.1:1", { password: "rotated" }, active, online);
		expect(close).not.toHaveBeenCalled();
		await expect(active.view()).resolves.not.toBeNull();
	});
});

describe("removeSavedRemote", () => {
	it("forgets the host", async () => {
		const path = await tempFile();
		await removeSavedRemote(path, "http://192.0.2.1:1", idleProxy());
		expect(JSON.parse(await readFile(path, "utf8")).remotes).toEqual([
			{ label: "mini", url: "http://192.0.2.9:9", password: "m" },
		]);
	});

	it("tears down the proxy when the removed host is the active one", async () => {
		const path = await tempFile();
		const { active, close, activated } = activeOn("http://192.0.2.1:1");
		await activated;
		await removeSavedRemote(path, "http://192.0.2.1:1", active);
		expect(close).toHaveBeenCalled();
		// Nothing left pointing at a host that no longer exists.
		await expect(active.view()).resolves.toBeNull();
	});

	it("leaves another host's proxy alone", async () => {
		const path = await tempFile();
		const { active, close, activated } = activeOn("http://192.0.2.9:9", "mini");
		await activated;
		await removeSavedRemote(path, "http://192.0.2.1:1", active);
		expect(close).not.toHaveBeenCalled();
	});

	it("refuses to touch a file others can read", async () => {
		const path = await tempFile(TWO_HOSTS, 0o644);
		await expect(removeSavedRemote(path, "http://192.0.2.1:1", idleProxy())).rejects.toThrow(/chmod 600/);
	});
});
