import { describe, expect, it, vi } from "vitest";
import { chmod, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { registerRemotesIpc } from "./remotes-main";
import { RemoteRegistry } from "./remote-registry";
import { IncompatibleRemoteVersionError } from "./remote-request";

type Handler = (event: unknown, ...args: unknown[]) => Promise<unknown>;

// ipcMain stand-in: records what was registered and lets a test invoke it.
function fakeIpc() {
	const handlers = new Map<string, Handler>();
	return {
		ipcMain: { handle: (channel: string, handler: Handler) => void handlers.set(channel, handler) },
		invoke: (channel: string, ...args: unknown[]) => {
			const handler = handlers.get(channel);
			if (!handler) throw new Error(`no handler for ${channel}`);
			return handler({}, ...args);
		},
		channels: () => [...handlers.keys()].sort(),
	};
}

async function tempFile(): Promise<string> {
	const dir = await mkdtemp(join(tmpdir(), "ao-remotes-main-"));
	const path = join(dir, "remotes.json");
	await writeFile(path, '{"remotes":[{"label":"workbox","url":"http://192.0.2.1:1","password":"old"}]}', "utf8");
	await chmod(path, 0o600);
	return path;
}

function registryOf(closed: string[] = []) {
	return new RemoteRegistry(async (entry) => ({
		base: "http://127.0.0.1:9999/tok",
		url: entry.url,
		close: async () => {
			closed.push(entry.url);
		},
	}));
}

describe("registerRemotesIpc", () => {
	it("registers the saved-host and connection surface", async () => {
		const ipc = fakeIpc();
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), registry: registryOf() });
		expect(ipc.channels()).toEqual([
			"remotes:add",
			"remotes:connect",
			"remotes:connected",
			"remotes:disconnect",
			"remotes:list",
			"remotes:probe",
			"remotes:remove",
			"remotes:request",
			"remotes:update",
		]);
	});

	it("lists hosts without their passwords", async () => {
		const ipc = fakeIpc();
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), registry: registryOf() });
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([{ label: "workbox", url: "http://192.0.2.1:1" }]);
	});

	it("saves a new host only after it answers as a daemon", async () => {
		const ipc = fakeIpc();
		const file = await tempFile();
		const probe = vi.fn().mockResolvedValueOnce("offline" as const).mockResolvedValueOnce("online" as const);
		registerRemotesIpc(ipc.ipcMain, { file, registry: registryOf(), probe });
		const mini = { label: "mini", url: "http://192.0.2.9:9", password: "m" };

		await expect(ipc.invoke("remotes:add", mini)).resolves.toBe("offline");
		expect(JSON.parse(await readFile(file, "utf8")).remotes).toHaveLength(1);

		await expect(ipc.invoke("remotes:add", mini)).resolves.toBe("online");
		expect(JSON.parse(await readFile(file, "utf8")).remotes).toHaveLength(2);
	});

	it("connects a saved host only after it answers as a daemon, and hands back a password-free view", async () => {
		const ipc = fakeIpc();
		const probe = vi.fn().mockResolvedValueOnce("offline" as const).mockResolvedValueOnce("online" as const);
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), registry: registryOf(), probe });

		await expect(ipc.invoke("remotes:connect", "http://192.0.2.1:1")).rejects.toThrow(/is offline/);
		await expect(ipc.invoke("remotes:connected")).resolves.toEqual([]);

		const view = await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
		expect(view).toEqual({ label: "workbox", url: "http://192.0.2.1:1", base: "http://127.0.0.1:9999/tok" });
		expect(JSON.stringify(await ipc.invoke("remotes:connected"))).not.toContain("old");
	});

	it("removing a connected host closes its proxy", async () => {
		const ipc = fakeIpc();
		const closed: string[] = [];
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), registry: registryOf(closed), probe: async () => "online" });
		await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
		await ipc.invoke("remotes:remove", "http://192.0.2.1:1");
		expect(closed).toEqual(["http://192.0.2.1:1"]);
		await expect(ipc.invoke("remotes:connected")).resolves.toEqual([]);
	});

	it("disconnect closes the proxy and forgets the view", async () => {
		const ipc = fakeIpc();
		const closed: string[] = [];
		registerRemotesIpc(ipc.ipcMain, { file: await tempFile(), registry: registryOf(closed), probe: async () => "online" });
		await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
		await ipc.invoke("remotes:disconnect", "http://192.0.2.1:1");
		expect(closed).toEqual(["http://192.0.2.1:1"]);
		await expect(ipc.invoke("remotes:connected")).resolves.toEqual([]);
	});
});

async function savedRemotes(file: string): Promise<Array<Record<string, unknown>>> {
	return JSON.parse(await readFile(file, "utf8")).remotes;
}

// Pairing binds a saved entry to the daemon's own id so a later request can
// name the machine it expects; the daemon refuses (421) when that is not it.
describe("host identity", () => {
	const mini = { label: "mini", url: "http://192.0.2.9:9", password: "m" };

	it("add records the host id the daemon reports", async () => {
		const ipc = fakeIpc();
		const file = await tempFile();
		const identity = vi.fn(async () => "h_mini");
		registerRemotesIpc(ipc.ipcMain, { file, registry: registryOf(), probe: async () => "online", identity });

		await expect(ipc.invoke("remotes:add", mini)).resolves.toBe("online");
		expect(await savedRemotes(file)).toContainEqual({ ...mini, hostId: "h_mini" });
	});

	// A daemon from before the identity route still pairs; it just stays unbound,
	// exactly as every host did until now.
	it("add still saves a daemon that has no identity route, without a host id", async () => {
		const ipc = fakeIpc();
		const file = await tempFile();
		const identity = vi.fn(async () => {
			throw new Error("remote identity probe returned 404");
		});
		registerRemotesIpc(ipc.ipcMain, { file, registry: registryOf(), probe: async () => "online", identity });

		await expect(ipc.invoke("remotes:add", mini)).resolves.toBe("online");
		const saved = (await savedRemotes(file)).find((entry) => entry.url === mini.url);
		expect(saved).toEqual(mini);
		expect(saved).not.toHaveProperty("hostId");
	});

	it("add refuses a daemon that speaks a different API version and saves nothing", async () => {
		const ipc = fakeIpc();
		const file = await tempFile();
		const identity = vi.fn(async () => {
			throw new IncompatibleRemoteVersionError();
		});
		registerRemotesIpc(ipc.ipcMain, { file, registry: registryOf(), probe: async () => "online", identity });

		await expect(ipc.invoke("remotes:add", mini)).resolves.toBe("incompatible");
		expect(await savedRemotes(file)).toHaveLength(1);
	});

	// Entries saved before host ids existed bind on their next connect — the same
	// trust-on-first-use the original pairing had.
	it("connect backfills the host id of an entry saved without one", async () => {
		const ipc = fakeIpc();
		const file = await tempFile();
		const identity = vi.fn(async () => "h_work");
		registerRemotesIpc(ipc.ipcMain, { file, registry: registryOf(), probe: async () => "online", identity });

		await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
		expect(await savedRemotes(file)).toEqual([
			{ label: "workbox", url: "http://192.0.2.1:1", password: "old", hostId: "h_work" },
		]);
		expect(identity).toHaveBeenCalledTimes(1);
	});

	it("update probes a re-pointed address with the saved host id and keeps the file when it is a stranger", async () => {
		const ipc = fakeIpc();
		const file = await tempFile();
		// The first probe is the connect that binds the entry; every later one is
		// the re-pointed address answering as a stranger.
		const probe = vi.fn<(entry: unknown) => Promise<"online" | "wrong-host">>()
			.mockResolvedValueOnce("online")
			.mockResolvedValue("wrong-host");
		registerRemotesIpc(ipc.ipcMain, { file, registry: registryOf(), probe, identity: async () => "h_work" });
		await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
		probe.mockClear();

		await expect(ipc.invoke("remotes:update", "http://192.0.2.1:1", { url: "http://192.0.2.5:5" })).resolves.toBe(
			"wrong-host",
		);
		expect(probe).toHaveBeenCalledWith(expect.objectContaining({ url: "http://192.0.2.5:5", hostId: "h_work" }));
		expect((await savedRemotes(file))[0].url).toBe("http://192.0.2.1:1");
	});
});

it("registers the extracted IPC surface once and keeps SSH editor metadata password-free", async () => {
  const source = await readFile(join(process.cwd(), "src/main.ts"), "utf8");
  expect(source.match(/registerRemotesIpc\(ipcMain,/g)).toHaveLength(1);
  expect(source).not.toMatch(/ipcMain\.handle\(\s*["']remotes:/);
  const ipc = fakeIpc();
  const file = await tempFile();
  registerRemotesIpc(ipc.ipcMain, { file, registry: new RemoteRegistry(async () => { throw new Error("unused"); }), probe: async () => "online" });
  await ipc.invoke("remotes:add", {
    label: "SSH", url: "http://192.0.2.5:5", password: "saved-password", sshDestination: "user@box",
  });
  const views = await ipc.invoke("remotes:list");
  expect(views).toContainEqual({ label: "SSH", url: "http://192.0.2.5:5", sshDestination: "user@box" });
  expect(JSON.stringify(views)).not.toContain("saved-password");
});
