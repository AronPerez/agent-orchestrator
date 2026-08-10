import { probeRemote, type RemoteHealth } from "./remote-request";
import {
	applyRemoteChanges,
	readRemotes,
	removeRemote,
	updateRemote,
	type RemoteChanges,
	type RemoteEntry,
} from "./remotes-store";
import type { ActiveHostView } from "./active-remote";

// What the renderer is allowed to see. The password stays in the main process.
export type RemoteHostView = {
	label: string;
	url: string;
};

export function toHostViews(entries: RemoteEntry[]): RemoteHostView[] {
	return entries.map(({ label, url }) => ({ label, url }));
}

// The proxy the app is currently talking through, as much of it as these need.
type ActiveProxyHandle = {
	view(): Promise<ActiveHostView | null>;
	deactivate(): Promise<void>;
};

async function findRemote(path: string, url: string): Promise<RemoteEntry> {
	const entry = (await readRemotes(path)).find((candidate) => candidate.url === url);
	if (!entry) throw new Error(`no saved host for ${url}`);
	return entry;
}

/**
 * Edit a saved host in place. Probes before saving exactly as adding does — an
 * edit is how a host gets fixed, and one that lands somewhere unreachable only
 * looks fixed — and drops the live proxy when it was this host's: that proxy
 * still holds the old address and the old password.
 */
export async function updateSavedRemote(
	path: string,
	url: string,
	changes: RemoteChanges,
	active: ActiveProxyHandle,
	probe: (entry: RemoteEntry) => Promise<RemoteHealth> = probeRemote,
): Promise<RemoteHealth> {
	const health = await probe(applyRemoteChanges(await findRemote(path, url), changes));
	if (health !== "online") return health;
	await updateRemote(path, url, changes);
	if ((await active.view())?.url === url) await active.deactivate();
	return health;
}

/** Forget a saved host. A proxy to a host that no longer exists is an open door with no doorman. */
export async function removeSavedRemote(path: string, url: string, active: ActiveProxyHandle): Promise<void> {
	await removeRemote(path, url);
	if ((await active.view())?.url === url) await active.deactivate();
}
