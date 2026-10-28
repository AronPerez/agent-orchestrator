import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import { useConnectedHosts } from "./useHostConnection";

export const projectQueryKey = (id: string, hostId?: string) => hostId ? ["project", hostId, id] as const : ["project", id] as const;

export function useProjectSettingsQuery(projectId: string, hostId?: string) {
	const { t } = useTranslation();
	const connected = useConnectedHosts();
	const hostConnected = !hostId || connected.includes(hostId);
	const query = useQuery({
		queryKey: projectQueryKey(projectId, hostId),
		enabled: hostConnected,
		queryFn: async () => {
			const { data, error } = await (hostId ? clientForHost(hostId) : apiClient).GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			if (data?.status !== "ok") throw new Error(t("settings.project.degraded"));
			return data.project as components["schemas"]["Project"];
		},
	});
	return { query, hostConnected };
}
