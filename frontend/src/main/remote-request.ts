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

export type RemoteHealth = "online" | "unauthorized" | "offline";

type FetchImpl = typeof fetch;

export async function remoteRequest(
	entry: RemoteEntry,
	init: RemoteRequestInit,
	fetchImpl: FetchImpl = fetch,
): Promise<RemoteResponse> {
	const base = entry.url.replace(/\/+$/, "");
	const response = await fetchImpl(`${base}${init.path}`, {
		method: init.method,
		headers: {
			"Content-Type": "application/json",
			// Same credential presentation as the CLI (cli/remote.go:374).
			Authorization: `Bearer ${entry.password}`,
		},
		body: init.body === undefined ? undefined : JSON.stringify(init.body),
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

export async function probeRemote(entry: RemoteEntry, fetchImpl: FetchImpl = fetch): Promise<RemoteHealth> {
	try {
		const { status } = await remoteRequest(entry, { method: "GET", path: "/healthz" }, fetchImpl);
		if (status === 401 || status === 403) return "unauthorized";
		return status >= 200 && status < 300 ? "online" : "offline";
	} catch {
		// A transport failure is indistinguishable from a wrong port here, and
		// both mean the same thing to the user: it is not reachable.
		return "offline";
	}
}
