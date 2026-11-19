import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";

type Identity = components["schemas"]["IdentityResponse"];

/**
 * This computer's stable daemon host ID. Session lineage from other machines
 * names local parents by this ID, while the renderer calls this machine `local`.
 * Undefined until loaded or when the daemon cannot answer.
 */
export function useLocalHostId(enabled = true): string | undefined {
	return useQuery<Identity>({
		queryKey: ["local-host-identity"],
		enabled,
		retry: false,
		staleTime: Infinity,
		queryFn: async ({ signal }) => {
			const { data, error } = await apiClient.GET("/api/v1/identity", { signal });
			if (error || !data) throw apiErrorException(error);
			return data;
		},
	}).data?.hostId;
}
