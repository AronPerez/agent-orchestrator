import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { workspaceQueryKey } from "./useWorkspaceQuery";
import { forgetRemoteHost, syncRemoteCache } from "../lib/remote-chat-cache";

const { patch, del, get } = vi.hoisted(() => ({ patch: vi.fn(), del: vi.fn(), get: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { PATCH: patch, DELETE: del, GET: get },
	apiErrorMessage: (error: { message: string }) => error.message,
}));
vi.mock("../lib/host-clients", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../lib/host-clients")>();
	return { ...actual, clientForSessionHost: (hostId?: string) => hostId
		? { PATCH: patch, DELETE: del, GET: get }
		: actual.clientForSessionHost(hostId) };
});
import { useRememberProjectPermissions } from "./useRememberProjectPermissions";

function setup(sourceHarness?: string, hostId?: string) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	const invalidate = vi.spyOn(client, "invalidateQueries");
	const wrapper = ({ children }: { children: ReactNode }) =>
		<QueryClientProvider client={client}>{children}</QueryClientProvider>;
	return { ...renderHook(() => useRememberProjectPermissions("project-one", sourceHarness, hostId), { wrapper }), invalidate, client };
}

// The daemon's copy of the project: GET reads what the last successful write left.
let stored: string | undefined;
beforeEach(() => {
	stored = undefined;
	patch.mockReset();
	del.mockReset();
	get.mockReset();
	get.mockImplementation(async () => ({ data: { status: "ok", project: { config: stored ? { agentConfig: { permissions: stored } } : {} } } }));
});

describe("project permission persistence", () => {
	it("writes only the requested permission to the correct project and refreshes project data", async () => {
		patch.mockImplementation(async (_path, { body }) => { stored = body.permissions; return { data: { project: {} } }; });
		const { result, invalidate } = setup();
		expect(patch).not.toHaveBeenCalled();
		await act(async () => { await result.current.remember("bypass-permissions"); });
		expect(patch).toHaveBeenCalledWith("/api/v1/projects/{id}/permissions", {
			params: { path: { id: "project-one" } }, body: { permissions: "bypass-permissions" },
		});
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["project", "project-one"] });
		expect(invalidate).toHaveBeenCalledWith({ queryKey: workspaceQueryKey });
		await waitFor(() => expect(result.current.savedMode).toBe("bypass-permissions"));
	});

	it("reads the project's remembered mode and forgets it", async () => {
		stored = "auto";
		del.mockImplementation(async () => { stored = undefined; return { data: { project: {} } }; });
		const { result, invalidate } = setup();
		await waitFor(() => expect(result.current.savedMode).toBe("auto"));
		await act(async () => { await result.current.remember(null); });
		expect(del).toHaveBeenCalledWith("/api/v1/projects/{id}/permissions", { params: { path: { id: "project-one" } } });
		expect(patch).not.toHaveBeenCalled();
		expect(invalidate).toHaveBeenCalledWith({ queryKey: ["project", "project-one"] });
		await waitFor(() => expect(result.current.savedMode).toBeUndefined());
	});

	it("reports errors without claiming success and allows an explicit retry", async () => {
		patch.mockResolvedValueOnce({ error: { message: "Project not found" } }).mockImplementationOnce(async () => { stored = "auto"; return {}; });
		const { result, invalidate } = setup();
		await act(async () => { await expect(result.current.remember("auto")).rejects.toEqual({ message: "Project not found" }); });
		await waitFor(() => expect(result.current.error).toBe("Project not found"));
		expect(result.current.savedMode).toBeUndefined();
		expect(invalidate).not.toHaveBeenCalled();
		await act(async () => { await result.current.remember("auto"); });
		await waitFor(() => expect(result.current.savedMode).toBe("auto"));
		expect(result.current.error).toBeUndefined();
	});
});


it("sends source harness context so the daemon can preserve Full access across providers", async () => {
	patch.mockResolvedValue({});
	const { result } = setup("codex");
	await act(async () => { await result.current.remember("default"); });
	expect(patch).toHaveBeenCalledWith("/api/v1/projects/{id}/permissions", {
		params: { path: { id: "project-one" } }, body: { permissions: "default", sourceHarness: "codex" },
	});
});

it("reads a Codex Full access write as the bypass mode the project stores", async () => {
	patch.mockImplementation(async () => { stored = "bypass-permissions"; return {}; });
	const { result } = setup("codex");
	await act(async () => { await result.current.remember("default"); });
	await waitFor(() => expect(result.current.savedMode).toBe("bypass-permissions"));
});

it("shows a mode changed elsewhere once the project refetches", async () => {
	patch.mockImplementation(async (_path, { body }) => { stored = body.permissions; return {}; });
	const { result, client } = setup();
	await act(async () => { await result.current.remember("auto"); });
	await waitFor(() => expect(result.current.savedMode).toBe("auto"));
	stored = "bypass-permissions";
	await act(async () => { await client.invalidateQueries({ queryKey: ["project", "project-one"] }); });
	await waitFor(() => expect(result.current.savedMode).toBe("bypass-permissions"));
});

it("does not read a project for a Scratchpad session", async () => {
	const client = new QueryClient();
	const wrapper = ({ children }: { children: ReactNode }) =>
		<QueryClientProvider client={client}>{children}</QueryClientProvider>;
	const { result } = renderHook(() => useRememberProjectPermissions("__standalone__"), { wrapper });
	await act(async () => {});
	expect(get).not.toHaveBeenCalled();
	expect(result.current.savedMode).toBeUndefined();
});

it("preserves remembered permissions for a retained offline host when the saved hosts change", async () => {
	stored = "auto";
	const { result, client } = setup(undefined, "box-a");
	await waitFor(() => expect(result.current.savedMode).toBe("auto"));
	get.mockRejectedValue(new Error("Host is offline"));
	client.setQueryData(["project", "box-b", "project-one"], { config: {} });

	await act(async () => { await syncRemoteCache(client, ["box-a", "box-c"]); });

	expect(client.getQueryData(["project", "box-a", "project-one"])).toMatchObject({ config: { agentConfig: { permissions: "auto" } } });
	expect(client.getQueryData(["project", "box-b", "project-one"])).toBeUndefined();
	expect(result.current.savedMode).toBe("auto");
	expect(get).toHaveBeenCalledTimes(1);
});

it("clears a revoked host's remembered permissions from a mounted observer while preserving other hosts and local projects", async () => {
	stored = "auto";
	const { result, client } = setup(undefined, "box-a");
	await waitFor(() => expect(result.current.savedMode).toBe("auto"));
	get.mockRejectedValue(new Error("Access revoked"));
	client.setQueryData(["project", "box-b", "project-one"], { config: { agentConfig: { permissions: "default" } } });
	client.setQueryData(["project", "project-one"], { config: { agentConfig: { permissions: "bypass-permissions" } } });

	await act(async () => { await forgetRemoteHost(client, "box-a"); });

	await waitFor(() => expect(result.current.savedMode).toBeUndefined());
	expect(client.getQueryData(["project", "box-a", "project-one"])).toBeUndefined();
	expect(client.getQueryData(["project", "box-b", "project-one"])).toMatchObject({ config: { agentConfig: { permissions: "default" } } });
	expect(client.getQueryData(["project", "project-one"])).toMatchObject({ config: { agentConfig: { permissions: "bypass-permissions" } } });
});

it("invalidates the host-first project key after remembering remote permissions", async () => {
	stored = "auto";
	patch.mockImplementation(async (_path, { body }) => { stored = body.permissions; return {}; });
	const { result, invalidate, client } = setup(undefined, "box-a");
	await waitFor(() => expect(result.current.savedMode).toBe("auto"));

	await act(async () => { await result.current.remember("bypass-permissions"); });

	expect(invalidate).toHaveBeenCalledWith({ queryKey: ["project", "box-a", "project-one"] });
	expect(invalidate).toHaveBeenCalledWith({ queryKey: ["remote-workspaces", "box-a"] });
	await waitFor(() => expect(result.current.savedMode).toBe("bypass-permissions"));
	expect(client.getQueryData(["project", "project-one", "box-a"])).toBeUndefined();
});
