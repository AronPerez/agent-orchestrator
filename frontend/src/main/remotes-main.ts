import os from "node:os";
import path from "node:path";
import {
	IncompatibleRemoteVersionError,
	probeRemote,
	readRemoteIdentity,
	remoteRequest,
	type RemoteHealth,
	type RemoteRequestInit,
} from "./remote-request";
import { findRemote, removeSavedRemote, toHostViews, updateSavedRemote } from "./remotes-ipc";
import type { RemoteRegistry } from "./remote-registry";
import { addRemote, readRemotes, updateRemote, type RemoteChanges, type RemoteEntry } from "./remotes-store";

// The CLI resolves this file through config.StateDir(), which is ~/.ao
// unconditionally — it does NOT honour AO_DATA_DIR (that points at the daemon's
// data dir). Following AO_DATA_DIR here would make the app read a different
// file than `ao --url` writes, which defeats sharing one host list and one
// credential store.
export function remotesFilePath(): string {
	return path.join(os.homedir(), ".ao", "remotes.json");
}

// The slice of Electron's ipcMain these handlers need, so tests need no Electron.
type IpcMainLike = {
	// eslint-disable-next-line @typescript-eslint/no-explicit-any -- the listener
	// args must unify Electron's IpcMain (any[]) with a test fake (unknown[]),
	// and `never[]` rejects both; `any[]` here leaks nowhere past registration.
	handle(channel: string, listener: (event: unknown, ...args: any[]) => Promise<unknown>): void;
};

export type RemotesIpcDeps = {
	file: string;
	registry: RemoteRegistry;
	probe?: (entry: RemoteEntry) => Promise<RemoteHealth>;
	/** The daemon's own id from GET /api/v1/identity; injectable for tests. */
	identity?: (entry: Pick<RemoteEntry, "url">) => Promise<string>;
};

/**
 * Saved AO daemons, shared with the CLI's ~/.ao/remotes.json. Everything the
 * renderer receives back is password-free (see remotes-ipc.ts); the plaintext
 * password only ever travels renderer -> main, on `add` or `update`.
 */
export function registerRemotesIpc(
	ipcMain: IpcMainLike,
	{ file, registry, probe = probeRemote, identity = readRemoteIdentity }: RemotesIpcDeps,
): void {
	const disconnect = (url: string) => registry.disconnect(url);

	// Pairing without an id is right for a daemon too old to report one, and a
	// silent loss of the wrong-host check for anything else (a timeout, a redirect,
	// a non-JSON answer). The log is the one place that difference shows — address
	// and reason only, never the password.
	const warnUnbound = (url: string, error: unknown) =>
		console.warn(
			`[remotes] identity for ${url} unavailable; host stays unbound: ${error instanceof Error ? error.message : String(error)}`,
		);

	// An entry saved before host ids existed binds on its next connect — the same
	// trust-on-first-use its pairing had. Best-effort: a daemon too old for the
	// identity route stays unbound and keeps working exactly as it always did.
	const bindIdentity = async (entry: RemoteEntry): Promise<RemoteEntry> => {
		try {
			return await updateRemote(file, entry.url, { hostId: await identity(entry) });
		} catch (error) {
			warnUnbound(entry.url, error);
			return entry;
		}
	};

	ipcMain.handle("remotes:list", async () => toHostViews(await readRemotes(file)));
	ipcMain.handle("remotes:add", async (_event, input: RemoteEntry) => {
		// Probe before saving: a host that never answered is worse than no host,
		// because it looks configured. The probe keeps its own vocabulary
		// (unauthorized, not-a-daemon, offline); identity is read only once the
		// host has answered as a daemon.
		const health = await probe(input);
		if (health !== "online") return health;
		let hostId: string | undefined;
		try {
			hostId = await identity(input);
		} catch (error) {
			if (error instanceof IncompatibleRemoteVersionError) return "incompatible" satisfies RemoteHealth;
			// A daemon from before the identity route: pair it unbound, as before.
			warnUnbound(input.url, error);
		}
		await addRemote(file, { ...input, hostId });
		return health;
	});
	ipcMain.handle("remotes:probe", async (_event, url: string) => probe(await findRemote(file, url)));
	ipcMain.handle("remotes:request", async (_event, url: string, init: RemoteRequestInit) =>
		remoteRequest(await findRemote(file, url), init),
	);
	ipcMain.handle("remotes:update", async (_event, url: string, changes: RemoteChanges) =>
		updateSavedRemote(file, url, changes, disconnect, probe),
	);
	ipcMain.handle("remotes:remove", async (_event, url: string) => removeSavedRemote(file, url, disconnect));

	ipcMain.handle("remotes:connect", async (_event, url: string) => {
		const entry = await findRemote(file, url);
		// Probe before starting a proxy: a reachable port may serve something
		// other than an AO daemon, and exposing it as connected can wedge the
		// app at boot.
		const health = await probe(entry);
		if (health !== "online") throw new Error(`host ${url} is ${health}`);
		return registry.connect(entry.hostId ? entry : await bindIdentity(entry));
	});
	ipcMain.handle("remotes:disconnect", async (_event, url: string) => disconnect(url));
	ipcMain.handle("remotes:connected", async () => registry.views());
}
