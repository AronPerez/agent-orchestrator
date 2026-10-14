import { useQuery } from "@tanstack/react-query";
import { useSyncExternalStore } from "react";
import type { components } from "../../api/schema";
import { subscribeApiBaseUrl } from "../lib/api-client";
import { baseUrlFor, clientFor } from "../lib/host-clients";
import { LOCAL_HOST } from "../lib/hosts";
import { isLoopbackHostname } from "../lib/loopback";

export type CodexAccount = components["schemas"]["CodexAccountResponse"];
const localBase = () => baseUrlFor(LOCAL_HOST);

export function useCodexAccountsQuery(active: boolean) {
	const base = useSyncExternalStore(subscribeApiBaseUrl, localBase);
	// LOCAL_HOST can also represent a daemon-served LAN UI. Never probe it.
	const url = base === "" ? window.location.origin : base;
	const local = url !== null && URL.canParse(url) && isLoopbackHostname(new URL(url).hostname);
	const query = useQuery<components["schemas"]["CodexAccountsResponse"]>({
		queryKey: ["codex-accounts", LOCAL_HOST, base],
		enabled: active && local,
		retry: false,
		refetchOnWindowFocus: true,
		refetchInterval: (current) => active && local && !(["unauthorized", "localOnly"].includes(current.state.error?.message ?? "")) ? 15_000 : false,
		queryFn: async ({ signal }) => {
			if (!active || !local) throw new Error("unavailable");
			const { data, error, response } = await clientFor(LOCAL_HOST).GET("/api/v1/agents/codex/accounts", { signal });
			// Raw diagnostics may contain credential paths; only display fixed copy.
			if (response.status === 401 || response.status === 403) throw new Error("unauthorized");
			if (error?.code === "ROUTE_LOOPBACK_ONLY") throw new Error("localOnly");
			if (error || !data) throw new Error("request");
			return data;
		},
	});
	return { ...query, available: local, disconnected: base === null };
}
