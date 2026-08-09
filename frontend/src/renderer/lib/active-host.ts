import { aoBridge } from "./bridge";
import { setApiBaseUrl } from "./api-client";

// Which daemon the whole app is pointed at. Selection persists in localStorage
// and a switch reloads the window — every query, the SSE streams and the mux
// socket restart against the new base, the same simplification auth-gate.ts
// uses after login. Within one page lifetime the active host is therefore
// immutable, which is what makes activeHost() a plain accessor.
const STORAGE_KEY = "ao.active-host-url";

let current: { label: string; url: string } | null = null;

export function activeHost(): { label: string; url: string } | null {
	return current;
}

export async function initActiveHost(): Promise<void> {
	const stored = localStorage.getItem(STORAGE_KEY);
	if (!stored) {
		current = null;
		// A proxy left over from a previous page lifetime serves nobody.
		await aoBridge.remotes.deactivate().catch(() => undefined);
		return;
	}
	try {
		const view = await aoBridge.remotes.activate(stored);
		current = { label: view.label, url: view.url };
		setApiBaseUrl(view.base);
	} catch {
		// The saved host is gone or refused: clear it so the next boot is local
		// instead of wedged, and let this boot continue as local.
		localStorage.removeItem(STORAGE_KEY);
		current = null;
	}
}

export async function switchToHost(
	url: string | null,
	reload: () => void = () => window.location.reload(),
): Promise<void> {
	if (url === null) {
		localStorage.removeItem(STORAGE_KEY);
		await aoBridge.remotes.deactivate().catch(() => undefined);
	} else {
		localStorage.setItem(STORAGE_KEY, url);
	}
	reload();
}

/**
 * The one sanctioned path for daemon-status base updates. While a remote host
 * is active the local daemon's ready/port announcements must not repoint the
 * app — that would flip every view back to local data mid-session.
 */
export function applyDaemonBaseUrl(base: string | null): void {
	if (current !== null) return;
	setApiBaseUrl(base);
}
