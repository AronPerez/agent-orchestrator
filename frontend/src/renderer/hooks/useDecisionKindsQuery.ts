import { useQuery } from "@tanstack/react-query";
import { apiClient } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForHost } from "../lib/host-clients";
import type { DecisionKindOption } from "../lib/project-autonomy";

export const decisionKindsQueryKey = (projectId: string, hostId?: string) =>
	hostId ? ["decision-kinds", hostId, projectId] as const : ["decision-kinds", projectId] as const;

/**
 * The decision kinds a project's daemon knows. A daemon that predates
 * autonomy levels fails this query, which also means it would refuse an
 * `autonomy` config block, so callers hide the setting until it succeeds.
 */
export function useDecisionKindsQuery(projectId: string, hostId?: string, enabled = true) {
	return useQuery({
		queryKey: decisionKindsQueryKey(projectId, hostId),
		enabled,
		retry: false,
		staleTime: Infinity,
		queryFn: async (): Promise<DecisionKindOption[]> => {
			const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/decisions/kinds", {
				params: { query: { project: projectId } },
			});
			if (error) throw apiErrorException(error);
			return (data?.kinds ?? []).map((kind) => ({ id: kind.id, label: kind.label, reservedByDefault: kind.reservedByDefault }));
		},
	});
}
