// A daemon that is not the daemon we expect. Every behaviour here was observed
// against a real host: an older build whose web-UI catch-all answers unknown
// routes with a 200 HTML page, a build whose JSON has a different shape, a
// wrong connection password, a build that is missing a route the app calls, a
// machine that is simply asleep, and one that accepts the connection and then
// never answers. Casting any of them to the expected type puts `undefined.map`
// on a render path.
export type Behaviour =
	| "healthy"
	| "html-catchall"
	| "wrong-shape"
	| "unauthorized"
	| "unreachable"
	| "route-missing"
	| "slow";

function json(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { "Content-Type": "application/json; charset=utf-8" },
	});
}

function notFound(): Response {
	return json({ error: "not_found", code: "NOT_FOUND", message: "route not found" }, 404);
}

function healthyResponse(input: RequestInfo | URL): Response {
	const url = new URL(input instanceof Request ? input.url : input.toString());
	switch (url.pathname) {
		case "/healthz":
			return json({ status: "ok", service: "agent-orchestrator-daemon", pid: 1 });
		case "/readyz":
			return json({ status: "ready", service: "agent-orchestrator-daemon", pid: 1 });
		case "/api/v1/projects":
			return json({ projects: [] });
		case "/api/v1/sessions":
			return json({ sessions: [] });
		case "/api/v1/fs/dirs":
			return json({ path: "/home/test", parent: "/home", entries: [], truncated: false });
		default:
			return notFound();
	}
}

// Accepts the request and answers only when the caller gives up. This is what
// makes "one sleeping host must not stall the others" falsifiable: without it a
// slow host is indistinguishable from a fast one in a test.
function neverRespond(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
	const signal = init?.signal ?? (input instanceof Request ? input.signal : null);
	return new Promise((_, reject) => {
		const abort = () => reject(signal?.reason ?? new DOMException("The operation was aborted", "AbortError"));
		if (signal?.aborted) abort();
		else signal?.addEventListener("abort", abort, { once: true });
	});
}

function pathnameOf(input: RequestInfo | URL): string {
	return new URL(input instanceof Request ? input.url : input.toString()).pathname;
}

export function fakeDaemon(behaviour: Behaviour): typeof fetch {
	return async (input, init) => {
		switch (behaviour) {
			case "healthy":
				return healthyResponse(input);
			case "html-catchall":
				return new Response("<!doctype html><html><body>AO</body></html>", {
					status: 200,
					headers: { "Content-Type": "text/html; charset=utf-8" },
				});
			case "wrong-shape":
				return json({ ok: true });
			case "unauthorized":
				return json(
					{
						error: "unauthorized",
						code: "BAD_PASSWORD",
						message: "missing or invalid connection password",
						requestId: "fake-request",
					},
					401,
				);
			case "unreachable":
				throw new TypeError("fetch failed");
			case "route-missing":
				return pathnameOf(input) === "/api/v1/fs/dirs" ? notFound() : healthyResponse(input);
			case "slow":
				return neverRespond(input, init);
		}
	};
}
