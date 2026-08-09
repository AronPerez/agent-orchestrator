import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../lib/bridge";

export const LOCAL_HOST_ID = "local";

export type HostStatus = "local" | "online" | "unauthorized" | "offline" | "checking";

/** What the main process is allowed to hand the renderer — never the password. */
export type RemoteHostView = { label: string; url: string };

export type RemoteHealth = "online" | "unauthorized" | "offline";

export type Host = {
	id: string;
	label: string;
	/** null for the local daemon — the app already knows how to reach it. */
	url: string | null;
	status: HostStatus;
};

/** The preload bridge's saved-host surface: list, add, probe, request. */
export function remotesBridge() {
	return aoBridge.remotes;
}

export function useRemoteHosts(): { hosts: Host[]; refresh: () => Promise<void> } {
	const { t } = useTranslation();
	const localHost: Host = { id: LOCAL_HOST_ID, label: t("hosts.local"), url: null, status: "local" };
	const [remotes, setRemotes] = useState<Host[]>([]);

	const refresh = useCallback(async () => {
		const saved = await remotesBridge().list();
		// Show every saved host immediately as "checking" — a host that is slow to
		// answer must not look like a host that does not exist.
		setRemotes(saved.map((host) => ({ id: host.url, label: host.label, url: host.url, status: "checking" })));
		await Promise.all(
			saved.map(async (host) => {
				const status = await remotesBridge().probe(host.url);
				setRemotes((current) => current.map((row) => (row.id === host.url ? { ...row, status } : row)));
			}),
		);
	}, []);

	useEffect(() => {
		void refresh();
	}, [refresh]);

	return { hosts: [localHost, ...remotes], refresh };
}
