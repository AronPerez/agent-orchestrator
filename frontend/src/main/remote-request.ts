import { parseDaemonProbe } from "../shared/daemon-attach";
import type { RemoteEntry } from "./remotes-store";

// Remote HTTP lives in the main process for two reasons: the renderer's origin
// is app://renderer and a remote daemon has no reason to allow it through CORS,
// and the connection password must never enter renderer memory.
export type RemoteRequestInit = {
	method: "GET" | "POST" | "DELETE";
	path: string;
	body?: unknown;
};

export type RemoteResponse = {
	status: number;
	body: unknown;
};

// "not-a-daemon" is its own answer because the honest sentence differs: the
// address replied, so telling someone it is unreachable sends them to debug a
// network that is working. "wrong-host" likewise: network and password both
// worked, but the daemon at this address is not the one that was saved (421).
// "incompatible" is a daemon whose identity route speaks another API version.
export type RemoteHealth = "online" | "unauthorized" | "offline" | "not-a-daemon" | "incompatible" | "wrong-host";

export class IncompatibleRemoteVersionError extends Error {
	constructor() {
		super("AO versions are incompatible. Update AO on this computer and the remote host.");
		this.name = "IncompatibleRemoteVersionError";
	}
}

type FetchImpl = typeof fetch;

// Accepts what the address field accepts: a bare host:port means http.
function identityUrl(url: string): URL {
	const base = (url.includes("://") ? url : `http://${url}`).replace(/\/+$/, "");
	const target = new URL(`${base}/api/v1/identity`);
	if (!["http:", "https:"].includes(target.protocol) || target.username || target.password)
		throw new Error("remote host must use an HTTP(S) URL without embedded credentials");
	return target;
}

/**
 * The daemon's id from its unauthenticated identity route — the one probe made
 * before any password is sent, so a saved entry can be bound to the machine it
 * was paired with (ADR 0003; the mobile app does the same in connectRuntime.ts).
 * A daemon from before that route answers 404 and lands in the plain Error
 * branch: the caller decides what an unknown identity means.
 */
export async function readRemoteIdentity(
	entry: Pick<RemoteEntry, "url">,
	fetchImpl: FetchImpl = fetch,
	signal: AbortSignal = AbortSignal.timeout(5_000),
): Promise<string> {
	// redirect: "error" — a redirect could hand the follow-up to another origin.
	const response = await fetchImpl(identityUrl(entry.url).href, { method: "GET", redirect: "error", signal });
	if (!response.ok) throw new Error(`remote identity probe returned ${response.status}`);
	const body = (await response.json()) as { hostId?: unknown; apiVersion?: unknown };
	if (typeof body.hostId !== "string" || body.hostId === "") throw new Error("remote identity probe returned no host ID");
	if (body.hostId === "local") throw new Error("remote identity probe returned the reserved local host ID");
	if (body.apiVersion !== 1) throw new IncompatibleRemoteVersionError();
	return body.hostId;
}

export async function remoteRequest(
	entry: RemoteEntry,
	init: RemoteRequestInit,
	fetchImpl: FetchImpl = fetch,
	signal?: AbortSignal,
): Promise<RemoteResponse> {
	const base = entry.url.replace(/\/+$/, "");
	// The path is concatenated, and a concatenated path can leave the host: a
	// path starting with "@" turns the base into userinfo ("http://box:3011" +
	// "@evil.com/" is a request to evil.com) and would hand this host's
	// connection password to whoever answers there. The renderer is the only
	// caller today, but it is the process that renders agent output, so the
	// origin is checked here rather than trusted upstream.
	const target = new URL(`${base}${init.path}`);
	if (target.origin !== new URL(base).origin)
		throw new Error(`refusing to send ${entry.url} credentials to ${target.origin}`);
	const response = await fetchImpl(target.href, {
		method: init.method,
		headers: {
			"Content-Type": "application/json",
			// Same credential presentation as the CLI (cli/remote.go:374).
			Authorization: `Bearer ${entry.password}`,
			// The daemon answers 421 when this is not its own id (see RemoteEntry.hostId).
			...(entry.hostId ? { "X-AO-Expected-Host-ID": entry.hostId } : {}),
		},
		body: init.body === undefined ? undefined : JSON.stringify(init.body),
		signal,
	});

	const text = await response.text();
	let body: unknown = null;
	try {
		body = text ? JSON.parse(text) : null;
	} catch {
		body = text;
	}
	return { status: response.status, body };
}

export async function probeRemote(
	entry: RemoteEntry,
	fetchImpl: FetchImpl = fetch,
	timeoutMs = 5_000,
): Promise<RemoteHealth> {
	try {
		const { status, body } = await remoteRequest(
			entry,
			{ method: "GET", path: "/healthz" },
			fetchImpl,
			AbortSignal.timeout(timeoutMs),
		);
		if (status === 401 || status === 403) return "unauthorized";
		if (status === 421) return "wrong-host";
		if (status < 200 || status >= 300) return "offline";
		// A status code proves something replied, not that it is a daemon. An SPA
		// catch-all (an Expo dev server on a mistyped port, say) answers every path
		// with 200 and an HTML page; accepting that as the api base hands the whole
		// renderer bodies it will read fields off and crash on.
		return parseDaemonProbe("healthz", body) === null ? "not-a-daemon" : "online";
	} catch {
		// A transport failure is indistinguishable from a wrong port here, and
		// both mean the same thing to the user: it is not reachable.
		return "offline";
	}
}
