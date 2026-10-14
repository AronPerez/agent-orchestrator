import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import {
	agentModelsQueryOptions,
	refreshAgentModels,
	revalidateAgentModels,
} from "./useAgentModelsQuery";

const { get, post, clientFor } = vi.hoisted(() => {
	const get = vi.fn().mockResolvedValue({ data: { models: [] } });
	const post = vi.fn().mockResolvedValue({ data: { models: [] } });
	return { get, post, clientFor: vi.fn(() => ({ GET: get, POST: post })) };
});
vi.mock("../lib/host-clients", () => ({ clientFor }));

describe("standalone model catalogs", () => {
	it("keeps the remote host and cache key without sending a synthetic project ID", async () => {
		const project = { host: "remote", id: "@standalone" };
		const options = agentModelsQueryOptions("codex", project);
		expect(options.queryKey).not.toEqual(
			agentModelsQueryOptions("codex", { ...project, host: "local" }).queryKey,
		);
		await new QueryClient().fetchQuery(options);
		await refreshAgentModels("codex", project);
		await revalidateAgentModels("codex", project);
		expect(clientFor.mock.calls).toEqual([["remote"], ["remote"], ["remote"]]);
		expect(get).toHaveBeenCalledWith("/api/v1/agents/{agent}/models", {
			params: { path: { agent: "codex" }, query: { projectId: undefined } },
		});
		expect(post.mock.calls).toEqual(
			[undefined, true].map((revalidate) => [
				"/api/v1/agents/{agent}/models/refresh",
				{
					params: {
						path: { agent: "codex" },
						query: { projectId: undefined, revalidate },
					},
				},
			]),
		);
	});
});
