import type { BrowserRuntimeLinkHandle } from "./browser-runtime-link";
import type { ActiveProxy } from "./remote-proxy";
import type { RemoteEntry } from "./remotes-store";

/** What the renderer may know about a connected host. Password stays here. */
export type ConnectedHostView = {
	label: string;
	url: string;
	base: string;
};

type StartProxy = (entry: RemoteEntry) => Promise<ActiveProxy>;
type StartRuntimeLink = (entry: RemoteEntry, proxy: ActiveProxy) => BrowserRuntimeLinkHandle;

// N hosts live at once, one proxy each, keyed by url. Replaces ActiveRemote's
// single-slot model: the app no longer views one host, it talks to several.
export class RemoteRegistry {
	private readonly live = new Map<
		string,
		{ view: ConnectedHostView; proxy: ActiveProxy; runtime?: BrowserRuntimeLinkHandle }
	>();

	constructor(
		private readonly start: StartProxy,
		// Attaches this app as the host's browser runtime so `ao browser` inside
		// that host's workers reaches the panel here. Optional so tests that only
		// care about proxies stay unchanged.
		private readonly startRuntime?: StartRuntimeLink,
	) {}

	async connect(entry: RemoteEntry): Promise<ConnectedHostView> {
		const existing = this.live.get(entry.url);
		// Reuse rather than restart: a second connect would strand the first
		// proxy's port with the renderer still holding streams against it.
		if (existing) return existing.view;

		const proxy = await this.start(entry);
		const view = { label: entry.label, url: entry.url, base: proxy.base };
		const runtime = this.startRuntime?.(entry, proxy);
		this.live.set(entry.url, { view, proxy, runtime });
		return view;
	}

	async disconnect(url: string): Promise<void> {
		const entry = this.live.get(url);
		if (!entry) return;
		this.live.delete(url);
		entry.runtime?.dispose(); // stop reconnect attempts before the proxy dies
		await entry.proxy.close();
	}

	views(): ConnectedHostView[] {
		return [...this.live.values()].map(({ view }) => view);
	}

	async closeAll(): Promise<void> {
		const entries = [...this.live.values()];
		this.live.clear();
		for (const { runtime } of entries) runtime?.dispose();
		await Promise.all(entries.map(({ proxy }) => proxy.close()));
	}
}
