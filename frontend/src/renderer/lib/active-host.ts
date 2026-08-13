import { setApiBaseUrl } from "./api-client";
import { aoBridge } from "./bridge";
import { connectHost } from "./host-clients";
import { LOCAL_HOST, type HostId } from "./hosts";

/** Connect every saved host without making any one host own the window. */
export async function initHosts(): Promise<void> {
	const saved = await aoBridge.remotes.list();
	await Promise.allSettled(saved.map(({ url }) => connectHost(url)));
}

// Compatibility for the pre-unified-tree controls. It only ensures the host is
// connected; it no longer persists global selection or reloads the window.
export async function switchToHost(url: string | null): Promise<void> {
	if (url) await connectHost(url);
}

/** There is no global active host after federation. */
export function activeHost(): { label: string; url: string } | null {
	return null;
}

/** Local daemon lifecycle signals may update only the local host's base. */
export function applyDaemonBaseUrl(host: HostId, base: string | null): void {
	if (host !== LOCAL_HOST) return;
	setApiBaseUrl(base);
}
