import { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { describe, expect, it } from "vitest";
import { routeTree } from "../routeTree.gen";

describe("host-qualified session route", () => {
	it("restores the session on the host encoded in the URL", async () => {
		const hostId = "http://192.0.2.1:3011";
		const sessionId = "same:id";
		const history = createMemoryHistory({
			initialEntries: [`/host/${encodeURIComponent(hostId)}/session/${encodeURIComponent(sessionId)}`],
		});
		const router = createRouter({
			history,
			routeTree,
			context: { queryClient: new QueryClient() },
		});

		await router.load();

		expect(router.state.matches.at(-1)?.params).toMatchObject({ hostId, sessionId });
	});

	it("restores the project on the host encoded in the URL", async () => {
		const hostId = "http://192.0.2.1:3011";
		const projectId = "same:id";
		const history = createMemoryHistory({
			initialEntries: [`/host/${encodeURIComponent(hostId)}/project/${encodeURIComponent(projectId)}`],
		});
		const router = createRouter({
			history,
			routeTree,
			context: { queryClient: new QueryClient() },
		});

		await router.load();

		expect(router.state.matches.at(-1)?.params).toMatchObject({ hostId, projectId });
	});
});
