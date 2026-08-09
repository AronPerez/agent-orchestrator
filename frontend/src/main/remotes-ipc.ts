import type { RemoteEntry } from "./remotes-store";

// What the renderer is allowed to see. The password stays in the main process.
export type RemoteHostView = {
	label: string;
	url: string;
};

export function toHostViews(entries: RemoteEntry[]): RemoteHostView[] {
	return entries.map(({ label, url }) => ({ label, url }));
}
