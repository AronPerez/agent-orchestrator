import createClient from "openapi-fetch";
import type { paths } from "../../api/schema";
import { getApiBaseUrl, hasTrustedApiBaseUrl } from "./api-client";
import { aoBridge } from "./bridge";
import { isLocal, type HostId } from "./hosts";

// One client per host. Local reads the live daemon base (which still moves when
// the daemon restarts on a new port); a remote reads the loopback proxy base its
// main-process proxy is listening on.
const remoteBases = new Map<HostId, string>();
const clients = new Map<HostId, ReturnType<typeof createClient<paths>>>();

export function registerHostBase(host: HostId, base: string): void {
	if (remoteBases.get(host) === base) return;
	remoteBases.set(host, base);
	// A changed base invalidates the cached client bound to the old one.
	clients.delete(host);
}

export function forgetHost(host: HostId): void {
	remoteBases.delete(host);
	clients.delete(host);
}

export function connectedHosts(): HostId[] {
	return [...remoteBases.keys()];
}

export function baseUrlFor(host: HostId): string | null {
	if (isLocal(host)) return hasTrustedApiBaseUrl() ? getApiBaseUrl() : null;
	return remoteBases.get(host) ?? null;
}

export function isHostReady(host: HostId): boolean {
	return baseUrlFor(host) !== null;
}

export function clientFor(host: HostId) {
	const base = baseUrlFor(host);
	if (base === null) throw new Error(`host ${host} is not connected`);
	// The local client is not cached: its base follows the daemon across restarts
	// and a stale cached client would keep talking to a dead port.
	if (isLocal(host)) return createClient<paths>({ baseUrl: base });
	const cached = clients.get(host);
	if (cached) return cached;
	const client = createClient<paths>({ baseUrl: base });
	clients.set(host, client);
	return client;
}

/** Start (or reuse) a proxy for a saved host and bind a client to its base. */
export async function connectHost(url: HostId): Promise<void> {
	const view = await aoBridge.remotes.connect(url);
	registerHostBase(view.url, view.base);
}

export async function disconnectHost(url: HostId): Promise<void> {
	forgetHost(url);
	await aoBridge.remotes.disconnect(url);
}

/** Re-bind clients to proxies main already has live (e.g. after a reload). */
export async function syncConnectedHosts(): Promise<void> {
	for (const view of await aoBridge.remotes.connected())
		registerHostBase(view.url, view.base);
}
