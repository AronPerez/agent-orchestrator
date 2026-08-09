import type { RemoteEntry } from "./remotes-store";
import type { ActiveProxy } from "./remote-proxy";

// What the renderer may know about the active remote. The password stays here.
export type ActiveHostView = {
	label: string;
	url: string;
	base: string;
};

type StartProxy = (entry: RemoteEntry) => Promise<ActiveProxy>;

// One active remote at a time: the app views one host, and a dangling proxy
// for a host nobody is viewing is an open door with no doorman.
export class ActiveRemote {
	private current: { view: ActiveHostView; proxy: ActiveProxy } | null = null;

	constructor(private readonly start: StartProxy) {}

	async activate(entry: RemoteEntry): Promise<ActiveHostView> {
		await this.deactivate();
		const proxy = await this.start(entry);
		const view = { label: entry.label, url: entry.url, base: proxy.base };
		this.current = { view, proxy };
		return view;
	}

	async deactivate(): Promise<void> {
		const previous = this.current;
		this.current = null;
		await previous?.proxy.close();
	}

	async view(): Promise<ActiveHostView | null> {
		return this.current?.view ?? null;
	}
}
