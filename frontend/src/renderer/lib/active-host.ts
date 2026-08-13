import { setApiBaseUrl } from "./api-client";
import { aoBridge } from "./bridge";
import { connectHost } from "./host-clients";
import { LOCAL_HOST, type HostId } from "./hosts";

/** Connect every saved host without making any one host own the window. */
export async function initHosts(): Promise<void> {
	const saved = await aoBridge.remotes.list().catch(() => []);
	await Promise.allSettled(saved.map(({ url }) => connectHost(url)));
}

/** Local daemon lifecycle signals may update only the local host's base. */
export function applyDaemonBaseUrl(host: HostId, base: string | null): void {
	if (host !== LOCAL_HOST) return;
	setApiBaseUrl(base);
}
