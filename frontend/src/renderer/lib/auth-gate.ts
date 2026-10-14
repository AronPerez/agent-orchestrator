// Connection-password login for the daemon-served web build.
//
// The daemon's LAN listener answers every data route with 401 until the caller
// proves it knows the connection password (Settings → Connect Mobile shows it).
// The static UI is served without one so this prompt can exist at all; the first
// API call is what discovers the daemon wants a password.
//
// Exchanging the password for a Path=/ HttpOnly cookie is what makes the rest of
// the app work unchanged: REST, the SSE event stream and the terminal mux
// WebSocket all carry cookies on a same-origin request without any per-transport
// credential plumbing.

const listeners = new Set<() => void>();
let unauthorized = false;

export function isUnauthorized(): boolean {
	return unauthorized;
}

export function subscribeUnauthorized(listener: () => void): () => void {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}

/** Called by the API client on any 401. Idempotent — a 401 storm shows one prompt. */
export function reportUnauthorized(): void {
	if (unauthorized) return;
	unauthorized = true;
	listeners.forEach((listener) => listener());
}

export class LoginFailedError extends Error {
	constructor(readonly status: number) {
		super(`login failed with ${status}`);
	}
}

/**
 * Exchanges the connection password for the session cookie. The route is
 * addressed relatively on purpose — the UI and the API are one origin — so the
 * browser applies the Set-Cookie without any CORS credential dance.
 *
 * On success the caller reloads: every query, the SSE stream and the mux socket
 * were started before the cookie existed, and a reload re-establishes all three
 * with it — far less machinery than teaching each transport to retry.
 */
export async function login(password: string): Promise<void> {
	const response = await fetch("/api/v1/auth/login", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		credentials: "same-origin",
		body: JSON.stringify({ password }),
	});
	if (!response.ok) throw new LoginFailedError(response.status);
	unauthorized = false;
	listeners.forEach((listener) => listener());
}
