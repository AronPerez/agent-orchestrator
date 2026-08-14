import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { clientFor } from "../lib/host-clients";
import { LOCAL_HOST, refKey, type Ref } from "../lib/hosts";

export type SessionUsageSummary = components["schemas"]["CompactSessionUsageResponse"];

export const sessionUsageQueryRoot = ["session-usage"] as const;
export const sessionUsageQueryKey = (project?: Ref) =>
	[...sessionUsageQueryRoot, project ? refKey(project) : "all"] as const;

export async function fetchSessionUsageSummaries(project?: Ref): Promise<SessionUsageSummary[]> {
	const { data, error } = await clientFor(project?.host ?? LOCAL_HOST).GET("/api/v1/usage/sessions", {
		params: { query: project ? { projectId: project.id } : {} },
	});
	if (error) throw error;
	return data?.sessions ?? [];
}

export function sessionUsageQueryOptions(project?: Ref) {
	return {
		queryKey: sessionUsageQueryKey(project),
		queryFn: () => fetchSessionUsageSummaries(project),
		retry: 1,
		select: (items: SessionUsageSummary[]) =>
			new Map(items.map((item) => [refKey({ host: project?.host ?? LOCAL_HOST, id: item.sessionId }), item] as const)),
	};
}

export function useSessionUsageSummaries(project?: Ref) {
	return useQuery(sessionUsageQueryOptions(project));
}
