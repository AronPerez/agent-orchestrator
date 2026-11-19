import { useQuery } from "@tanstack/react-query";
import { apiClient } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForHost } from "../lib/host-clients";
import { decisionKindsFromResponse, type DecisionKindOption } from "../lib/project-autonomy";

export const decisionKindsQueryKey = (projectId: string, hostId?: string) =>
	hostId ? ["decision-kinds", hostId, projectId] as const : ["decision-kinds", projectId] as const;

/**
 * The decision kinds a project's daemon knows. A daemon that predates
 * autonomy levels fails this query, which also means it would refuse an
 * `autonomy` config block, so callers hide the setting until it succeeds. So
 * does one that serves kinds without their default reservations.
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
			const kinds = decisionKindsFromResponse(data?.kinds);
			// Failing the query hides the settings section, as for an older daemon.
			if (!kinds) throw new Error("decision kinds without default reservations");
			return kinds;
		},
	});
}
