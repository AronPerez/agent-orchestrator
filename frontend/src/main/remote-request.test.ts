import { describe, expect, it, vi } from "vitest";
import { probeRemote, remoteRequest } from "./remote-request";

const entry = { label: "workbox", url: "http://192.0.2.1:3011", password: "pw" };

// Typed as fetch so `mock.calls` carries fetch's argument tuple — an untyped
// `vi.fn(async () => …)` records a zero-length tuple and indexing it is a type error.
function fakeFetch(status: number, body: unknown = {}) {
	return vi.fn<typeof fetch>(async () => new Response(JSON.stringify(body), { status }));
}

describe("remoteRequest", () => {
	it("sends the connection password as a Bearer token", async () => {
		const doFetch = fakeFetch(201, { id: "p1" });
		await remoteRequest(entry, { method: "POST", path: "/api/v1/projects", body: { path: "/srv/repo" } }, doFetch);

		const [url, init] = doFetch.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("http://192.0.2.1:3011/api/v1/projects");
		expect(new Headers(init.headers).get("Authorization")).toBe("Bearer pw");
		expect(init.body).toBe('{"path":"/srv/repo"}');
	});

	it("returns the status and parsed body rather than throwing on 4xx", async () => {
		const doFetch = fakeFetch(400, { error: "path must be absolute" });
		await expect(remoteRequest(entry, { method: "POST", path: "/api/v1/projects" }, doFetch)).resolves.toEqual({
			status: 400,
			body: { error: "path must be absolute" },
		});
	});

	it("joins paths without doubling the slash on a trailing-slash url", async () => {
		const doFetch = fakeFetch(200);
		await remoteRequest({ ...entry, url: "http://192.0.2.1:3011/" }, { method: "GET", path: "/healthz" }, doFetch);
		expect(doFetch.mock.calls[0][0]).toBe("http://192.0.2.1:3011/healthz");
	});
});

describe("probeRemote", () => {
	it("reports online on 200", async () => {
		await expect(probeRemote(entry, fakeFetch(200, { status: "ok" }))).resolves.toBe("online");
	});

	it("distinguishes a bad password from an unreachable host", async () => {
		await expect(probeRemote(entry, fakeFetch(401))).resolves.toBe("unauthorized");
		const refused = vi.fn(async () => {
			throw new TypeError("fetch failed");
		});
		await expect(probeRemote(entry, refused)).resolves.toBe("offline");
	});
});
