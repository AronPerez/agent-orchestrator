import { useEffect, useMemo } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientFor } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";

export type AgentReadiness = components["schemas"]["AgentReadinessResponse"];
export type AgentReadinessSnapshot = components["schemas"]["AgentReadinessSnapshot"];
export type AgentReadinessPurpose = components["schemas"]["EnsureAgentReadinessRequest"]["purpose"];

export const agentReadinessQueryKey = ["agent-readiness"] as const;
export const agentReadinessQueryKeyFor = (host: HostId) =>
	host === LOCAL_HOST ? agentReadinessQueryKey : ([...agentReadinessQueryKey, host] as const);

async function fetchAgentReadiness(host: HostId): Promise<AgentReadiness> {
	const { data, error } = await clientFor(host).GET("/api/v1/agents/readiness");
	if (error) throw new Error(apiErrorMessage(error));
	return data as AgentReadiness;
}

export async function ensureAgentReadiness(
	agentIds: string[] = [],
	purpose: AgentReadinessPurpose = "display",
	host: HostId = LOCAL_HOST,
): Promise<AgentReadiness> {
	const { data, error } = await clientFor(host).POST("/api/v1/agents/readiness/ensure", {
		body: { agentIds, purpose },
	});
	if (error) throw new Error(apiErrorMessage(error));
	return data as AgentReadiness;
}

export function mergeAgentReadiness(
	current: AgentReadiness | undefined,
	next: AgentReadiness,
): AgentReadiness {
	if (!current || next.agents.length === 0) return next;
	const byID = new Map(current.agents.map((agent) => [agent.id, agent]));
	for (const agent of next.agents) byID.set(agent.id, agent);
	return { agents: [...byID.values()].sort((a, b) => a.id.localeCompare(b.id)) };
}

export function cacheAgentReadiness(
	queryClient: QueryClient,
	next: AgentReadiness,
	host: HostId = LOCAL_HOST,
): void {
	queryClient.setQueryData<AgentReadiness>(agentReadinessQueryKeyFor(host), (current) =>
		mergeAgentReadiness(current, next),
	);
}

export const agentReadinessQueryOptionsFor = (host: HostId) => ({
	queryKey: agentReadinessQueryKeyFor(host),
	queryFn: () => fetchAgentReadiness(host),
	retry: 1,
	// Freshness belongs to the daemon coordinator. React Query only retains the
	// latest display copy and must never decide whether native work is required.
	staleTime: Number.POSITIVE_INFINITY,
});

export const agentReadinessQueryOptions = agentReadinessQueryOptionsFor(LOCAL_HOST);

export function useAgentReadinessQuery(enabled = true, host: HostId = LOCAL_HOST) {
	return useQuery({ ...agentReadinessQueryOptionsFor(host), enabled });
}

export function useEnsureAgentReadiness({
	agentIds = [],
	enabled = true,
	purpose = "display",
	host = LOCAL_HOST,
}: {
	agentIds?: string[];
	enabled?: boolean;
	purpose?: AgentReadinessPurpose;
	host?: HostId;
} = {}): void {
	const queryClient = useQueryClient();
	const agentIDsKey = [...new Set(agentIds.filter(Boolean))].sort().join(" ");
	const normalizedIDs = useMemo(
		() => (agentIDsKey === "" ? [] : agentIDsKey.split(" ")),
		[agentIDsKey],
	);

	useEffect(() => {
		if (!enabled) return;
		let active = true;
		void ensureAgentReadiness(normalizedIDs, purpose, host)
			.then((next) => {
				if (active) cacheAgentReadiness(queryClient, next, host);
			})
			.catch(() => {
				// Opportunistic: cached readiness remains useful and native launch is
				// still the authoritative validation path.
			});
		return () => {
			active = false;
		};
	}, [enabled, normalizedIDs, purpose, queryClient, host]);
}
