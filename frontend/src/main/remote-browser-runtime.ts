import type { BrowserRuntimeState } from "./browser-view-host";
import { sessionUiKey } from "../renderer/lib/hosts";
import { connectBrowserRuntime, type BrowserRuntimeCommand, type BrowserRuntimeSessionState } from "./browser-runtime-link";
import { isRemoteLoopbackURL, type ActiveProxy } from "./remote-proxy";

type Execute = (command: BrowserRuntimeCommand, signal: AbortSignal) => Promise<unknown>;

/** Attach the desktop's native Browser panel to a paired daemon's broker. */
export function attachRemoteBrowserRuntime(hostId: string, proxy: ActiveProxy, execute: Execute, onStateChange?: (state: BrowserRuntimeState) => void, onSessionState?: (state: BrowserRuntimeSessionState) => void): ActiveProxy {
	const endpoint = new URL(`${proxy.base}/browser-runtime`);
	endpoint.protocol = "ws:"; // The existing loopback proxy owns upstream TLS and credentials.
	const sessions = new Set<string>();
	const link = connectBrowserRuntime(endpoint, {
		onStateChange,
		onSessionState,
		execute: async (command, signal) => {
			if (command.action.startsWith("__") && command.action !== "__destroy-session") {
				throw new Error("Remote browser does not support offscreen rendering");
			}
			const id = command.sessionId;
			const sessionId = sessionUiKey(id, hostId);
			const args = { ...command.args };
			if ((command.action === "open" || command.action === "tab-new") && typeof args.url === "string" && args.url.trim()) {
				const url = args.url.trim();
				// Public sites use normal desktop browsing; only host-local targets
				// need the preview proxy (whose origin is also used by existing tabs).
				const parsed = new URL(/^https?:\/\//i.test(url) ? url : `http://${url}`);
				const loopback = isRemoteLoopbackURL(parsed);
				if (loopback || parsed.hostname.replace(/\.+$/, "").endsWith(".localhost")) {
					if (loopback) {
						const response = await fetch(`${proxy.base}/api/v1/sessions/${encodeURIComponent(id)}/preview`, { signal });
						if (!response.ok) throw new Error("Remote preview selection is unavailable");
						const selection = await response.json() as { previewUrl?: string };
						const selected = selection.previewUrl ? new URL(selection.previewUrl) : null;
						if (parsed.protocol !== "http:" || selected?.origin !== parsed.origin) {
							throw Object.assign(new Error("Run ao preview start and open its selected URL"), { code: "PREVIEW_MANAGED_REQUIRED" });
						}
					}
					args.url = proxy.previewUrl(id, parsed.href);
				}
			}
			sessions.add(sessionId);
			const result = await execute({ ...command, sessionId, args }, signal);
			if (command.action === "__destroy-session") {
				sessions.delete(sessionId);
				proxy.previewUrl(id, "");
			}
			const resolved = sourceURLs(result, (url) => proxy.resolvePreviewUrl(id, url));
			if (command.action === "get" && args.property === "url" && resolved && typeof resolved === "object" && "value" in resolved && typeof resolved.value === "string") {
				resolved.value = proxy.resolvePreviewUrl(id, resolved.value);
			}
			return resolved;
		},
	});
	return {
		...proxy,
		browserRuntime: link,
		close: async () => {
			link.dispose();
			await proxy.close();
			await Promise.all([...sessions].map((sessionId) => execute({
				type: "command", requestId: "disconnect", sessionId, action: "__destroy-session",
			}, new AbortController().signal)));
		},
	};
}

// Rewrite structured URL fields, not page text, selectors, or user input.
function sourceURLs(value: unknown, resolve: (url: string) => string): unknown {
	if (Array.isArray(value)) return value.map((item) => sourceURLs(item, resolve));
	if (!value || typeof value !== "object") return value;
	return Object.fromEntries(Object.entries(value).map(([key, item]) => {
		if ((key === "url" || key === "origin") && typeof item === "string") {
			const url = resolve(item);
			return [key, key === "origin" && url && url !== item ? new URL(url).origin : url];
		}
		return [key, sourceURLs(item, resolve)];
	}));
}
