import { parseDaemonProbe } from "../shared/daemon-attach";
import { COORDINATION_TOKEN_PREFIX, DEVICE_TOKEN_PREFIX, type RemoteEntry } from "./remotes-store";

// Remote HTTP lives in the main process for two reasons: the renderer's origin
// is app://renderer and a remote daemon has no reason to allow it through CORS,
// and saved connection passwords must not be sent back to renderer memory.
// The Add Host form necessarily holds a newly typed password until IPC saves it.
// "not-a-daemon" is its own answer because the honest sentence differs: the
// address replied, so telling someone it is unreachable sends them to debug a
// network that is working.
export type RemoteHealth = "online" | "unauthorized" | "offline" | "not-a-daemon" | "incompatible";

export class IncompatibleRemoteVersionError extends Error {
	constructor() {
		super("Orchestrator versions are incompatible. Update Orchestrator on this computer and the remote host.");
		this.name = "IncompatibleRemoteVersionError";
	}
}

type FetchImpl = typeof fetch;

/** Cache only the identity of the daemon attachment confirmed by the supervisor. */
export function createLocalHostIdentity(
	attachment: () => { port: number; pid?: number; generation: number } | null,
	identity = readRemoteIdentity,
): () => Promise<string | null> {
	let cached: { key: string; hostId: string } | undefined;
	const key = (value: NonNullable<ReturnType<typeof attachment>>) => `${value.generation}:${value.port}:${value.pid ?? ""}`;
	return async () => {
		const current = attachment();
		if (!current) { cached = undefined; return null; }
		const currentKey = key(current);
		if (cached?.key === currentKey) return cached.hostId;
		cached = undefined;
		try {
			const hostId = await identity({ url: `http://127.0.0.1:${current.port}` });
			const confirmed = attachment();
			if (!confirmed || key(confirmed) !== currentKey) return null;
			cached = { key: currentKey, hostId };
			return hostId;
		} catch { return null; }
	};
}

/** This is the only remote probe allowed before a saved password is sent. */
export async function readRemoteIdentity(
	entry: Pick<RemoteEntry, "url">,
	fetchImpl: FetchImpl = fetch,
	signal: AbortSignal = AbortSignal.timeout(5_000),
): Promise<string> {
	const base = (entry.url.includes("://") ? entry.url : `http://${entry.url}`).replace(/\/+$/, "");
	const url = new URL(`${base}/api/v1/identity`);
	if (!["http:", "https:"].includes(url.protocol) || url.username || url.password)
		throw new Error("remote host must use an HTTP(S) URL without embedded credentials");
	const response = await fetchImpl(url.href, { method: "GET", redirect: "error", signal });
	if (!response.ok) throw new Error(`remote identity probe returned ${response.status}`);
	const body = (await response.json()) as { hostId?: unknown; apiVersion?: unknown };
	if (typeof body.hostId !== "string" || body.hostId === "") throw new Error("remote identity probe returned no host ID");
	if (body.hostId === "local") throw new Error("remote identity probe returned reserved local host ID");
	if (body.apiVersion !== 1) throw new IncompatibleRemoteVersionError();
	return body.hostId;
}

export async function probeRemote(
	entry: RemoteEntry,
	fetchImpl: FetchImpl = fetch,
	timeoutMs = 5_000,
): Promise<RemoteHealth> {
	try {
		const base = (entry.url.includes("://") ? entry.url : `http://${entry.url}`).replace(/\/+$/, "");
		const url = new URL(`${base}/healthz`);
		if (!["http:", "https:"].includes(url.protocol) || url.username || url.password)
			throw new Error("remote host must use an HTTP(S) URL without embedded credentials");
		const response = await fetchImpl(url.href, {
			method: "GET",
			redirect: "error",
			headers: {
				Authorization: `Bearer ${entry.password}`,
				...(entry.hostId ? { "X-AO-Expected-Host-ID": entry.hostId } : {}),
			},
			signal: AbortSignal.timeout(timeoutMs),
		});
		if (response.status === 401 || response.status === 403) return "unauthorized";
		if (!response.ok) return "offline";
		// A status code proves something replied, not that it is a daemon. An SPA
		// catch-all (an Expo dev server on a mistyped port, say) answers every path
		// with 200 and an HTML page; accepting that as the api base hands the whole
		// renderer bodies it will read fields off and crash on.
		const text = await response.text();
		let body: unknown;
		try { body = JSON.parse(text); }
		catch { return "not-a-daemon"; }
		return parseDaemonProbe("healthz", body) === null ? "not-a-daemon" : "online";
	} catch {
		// A transport failure is indistinguishable from a wrong port here, and
		// both mean the same thing to the user: it is not reachable.
		return "offline";
	}
}

function remoteUrl(entry: Pick<RemoteEntry, "url">, path: string): URL {
	const base = (entry.url.includes("://") ? entry.url : `http://${entry.url}`).replace(/\/+$/, "");
	const url = new URL(`${base}${path}`);
	if (!["http:", "https:"].includes(url.protocol) || url.username || url.password)
		throw new Error("remote host must use an HTTP(S) URL without embedded credentials");
	return url;
}

export type PairDeviceResult =
	| { kind: "paired"; token: string; credential: "pairing-code" | "password" }
	| { kind: "unsupported" }
	| { kind: "unauthorized" }
	| { kind: "offline" };

export type CoordinationTokenResult =
	| { kind: "issued"; token: string; deviceId: string }
	| { kind: "unsupported" }
	| { kind: "unauthorized" }
	| { kind: "offline" };

export type CoordinationTokenHealth = "valid" | "invalid" | "revoked" | "offline";
export type CoordinationTokenRevokeResult = "retired" | "unsupported" | "unauthorized" | "offline";

/** Exchanges a pairing code or password for this device's own token. Confirm the
 * host's identity first: this sends the secret. */
export async function pairDevice(
	entry: Pick<RemoteEntry, "url" | "hostId">,
	secret: string,
	device: { name: string; platform: string },
	fetchImpl: FetchImpl = fetch,
	timeoutMs = 8_000,
): Promise<PairDeviceResult> {
	try {
		const response = await fetchImpl(remoteUrl(entry, "/api/v1/remote-host/devices").href, {
			method: "POST",
			redirect: "error",
			headers: {
				Authorization: `Bearer ${secret}`,
				"Content-Type": "application/json",
				...(entry.hostId ? { "X-AO-Expected-Host-ID": entry.hostId } : {}),
			},
			body: JSON.stringify(device),
			signal: AbortSignal.timeout(timeoutMs),
		});
		if (response.status === 401) return { kind: "unauthorized" };
		// 404 and 405: a release without device tokens. 403 and 409: this
		// credential cannot pair, or the host's device list is full.
		if ([403, 404, 405, 409, 501].includes(response.status)) return { kind: "unsupported" };
		if (!response.ok) return { kind: "offline" };
		const body = (await response.json()) as { hostId?: unknown; token?: unknown; credential?: unknown };
		if (typeof body.token !== "string" || !body.token.startsWith(DEVICE_TOKEN_PREFIX) || (entry.hostId && body.hostId !== entry.hostId)) return { kind: "offline" };
		return { kind: "paired", token: body.token, credential: body.credential === "pairing-code" ? "pairing-code" : "password" };
	} catch {
		return { kind: "offline" };
	}
}

/** Mint a route-filtered credential after the caller has verified this host's
 * unauthenticated identity. The token carries full transitive host authority
 * through agents and must remain owner-only. A missing endpoint is an older
 * compatible release, not a reason to break the desktop connection. */
export async function issueCoordinationToken(
	entry: Pick<RemoteEntry, "url" | "hostId" | "password">,
	device: { name: string; platform: string },
	fetchImpl: FetchImpl = fetch,
	timeoutMs = 8_000,
): Promise<CoordinationTokenResult> {
	try {
		const response = await fetchImpl(remoteUrl(entry, "/api/v1/remote-host/coordination-token").href, {
			method: "POST",
			redirect: "error",
			headers: {
				Authorization: `Bearer ${entry.password}`,
				"Content-Type": "application/json",
				...(entry.hostId ? { "X-AO-Expected-Host-ID": entry.hostId } : {}),
			},
			body: JSON.stringify(device),
			signal: AbortSignal.timeout(timeoutMs),
		});
		if (response.status === 401 || response.status === 403) return { kind: "unauthorized" };
		if ([404, 405, 501].includes(response.status)) return { kind: "unsupported" };
		if (!response.ok) return { kind: "offline" };
		const body = (await response.json()) as { hostId?: unknown; deviceId?: unknown; token?: unknown; scope?: unknown };
		if (typeof body.deviceId !== "string" || body.deviceId === ""
			|| typeof body.token !== "string" || !body.token.startsWith(COORDINATION_TOKEN_PREFIX)
			|| body.scope !== "agent-coordination" || (entry.hostId && body.hostId !== entry.hostId)) {
			return { kind: "offline" };
		}
		return { kind: "issued", token: body.token, deviceId: body.deviceId };
	} catch {
		return { kind: "offline" };
	}
}

/** Check a saved coordination credential only against a route its direct filter allows. */
export async function probeCoordinationToken(
	entry: Pick<RemoteEntry, "url" | "hostId" | "coordinationToken">,
	fetchImpl: FetchImpl = fetch,
	timeoutMs = 5_000,
): Promise<CoordinationTokenHealth> {
	if (!entry.coordinationToken) return "invalid";
	try {
		const response = await fetchImpl(remoteUrl(entry, "/api/v1/sessions?active=true").href, {
			method: "GET",
			redirect: "error",
			headers: {
				Authorization: `Bearer ${entry.coordinationToken}`,
				...(entry.hostId ? { "X-AO-Expected-Host-ID": entry.hostId } : {}),
			},
			signal: AbortSignal.timeout(timeoutMs),
		});
		if (response.status === 401 || response.status === 403) {
			try {
				const body = (await response.json()) as { code?: unknown };
				if (body.code === "DEVICE_REVOKED") return "revoked";
			} catch {
				// A non-JSON auth response is still an invalid credential.
			}
			return "invalid";
		}
		return response.ok ? "valid" : "offline";
	} catch {
		return "offline";
	}
}

/** Retire one saved coordination child with the current full credential. A
 * missing device is already retired, so 404 is an idempotent success. */
export async function revokeCoordinationToken(
	entry: Pick<RemoteEntry, "url" | "hostId" | "password">,
	deviceId: string,
	fetchImpl: FetchImpl = fetch,
	timeoutMs = 8_000,
): Promise<CoordinationTokenRevokeResult> {
	if (!deviceId) return "offline";
	try {
		const response = await fetchImpl(remoteUrl(entry, `/api/v1/remote-host/coordination-token/${encodeURIComponent(deviceId)}`).href, {
			method: "DELETE",
			redirect: "error",
			headers: {
				Authorization: `Bearer ${entry.password}`,
				...(entry.hostId ? { "X-AO-Expected-Host-ID": entry.hostId } : {}),
			},
			signal: AbortSignal.timeout(timeoutMs),
		});
		if (response.status === 204 || response.status === 404) return "retired";
		if (response.status === 401 || response.status === 403) return "unauthorized";
		if (response.status === 405 || response.status === 501) return "unsupported";
		return "offline";
	} catch {
		return "offline";
	}
}

export async function readAuthErrorCode(entry: RemoteEntry, fetchImpl: FetchImpl = fetch, timeoutMs = 5_000): Promise<string | undefined> {
	try {
		const response = await fetchImpl(remoteUrl(entry, "/healthz").href, {
			method: "GET",
			redirect: "error",
			headers: { Authorization: `Bearer ${entry.password}`, ...(entry.hostId ? { "X-AO-Expected-Host-ID": entry.hostId } : {}) },
			signal: AbortSignal.timeout(timeoutMs),
		});
		if (response.status !== 401) return undefined;
		const body = (await response.json()) as { code?: unknown };
		return typeof body.code === "string" ? body.code : undefined;
	} catch {
		return undefined;
	}
}
