import { queryOptions } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientFor } from "../lib/host-clients";
import { LOCAL_HOST, refKey, type Ref } from "../lib/hosts";

export type AgentModelCatalog = components["schemas"]["AgentModelsResponse"];

const MODEL_CATALOG_VALIDATION_INTERVAL_MS = 10 * 60 * 1_000;

export const agentModelsQueryKey = (agentId: string, project?: Ref) =>
	["agent-models", agentId, project ? refKey(project) : ""] as const;

async function requestAgentModels(
	agentId: string,
	project: Ref | undefined,
	mode: "cached" | "refresh" | "revalidate",
): Promise<AgentModelCatalog> {
	const path = { agent: agentId };
	const client = clientFor(project?.host ?? LOCAL_HOST);
	const projectId = project?.id;
	const result =
		mode === "cached"
			? await client.GET("/api/v1/agents/{agent}/models", {
					params: { path, query: { projectId } },
				})
			: await client.POST("/api/v1/agents/{agent}/models/refresh", {
					params: {
						path,
						query: { projectId, revalidate: mode === "revalidate" || undefined },
					},
				});
	if (result.error) throw new Error(apiErrorMessage(result.error));
	return result.data as AgentModelCatalog;
}

export function agentModelsQueryOptions(agentId: string, project?: Ref) {
	return queryOptions({
		queryKey: agentModelsQueryKey(agentId, project),
		queryFn: () => requestAgentModels(agentId, project, "cached"),
		enabled: agentId !== "",
		staleTime: MODEL_CATALOG_VALIDATION_INTERVAL_MS,
	});
}

export function refreshAgentModels(agentId: string, project?: Ref) {
	return requestAgentModels(agentId, project, "refresh");
}

export function revalidateAgentModels(agentId: string, project?: Ref) {
	return requestAgentModels(agentId, project, "revalidate");
}
