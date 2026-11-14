import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { SessionsBoard } from "./SessionsBoard";
import { TooltipProvider } from "./ui/tooltip";
import { useUiStore } from "../stores/ui-store";
import { connectedHosts } from "../lib/host-clients";
import { remoteWorkspaceQueryKey } from "../hooks/useWorkspaceQuery";

const { localGet, navigate, fetchMock } = vi.hoisted(() => ({ localGet: vi.fn(), navigate: vi.fn(), fetchMock: vi.fn() }));
vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...await importOriginal<typeof import("@tanstack/react-router")>(), useNavigate: () => navigate,
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: localGet }, hasTrustedApiBaseUrl: () => true, reportingFetch: (input: Request) => fetch(input),
	apiErrorCode: (error: { code?: string }) => error?.code,
	apiErrorMessage: (error: { message?: string }, fallback: string) => error?.message ?? fallback,
}));
vi.mock("../lib/telemetry", () => ({ captureRendererEvent: vi.fn() }));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ ready: false, baseUrl: "", client: {} }) }));
vi.mock("../hooks/useCloudOrg", () => ({ useCloudOrg: () => ({ org: undefined, ready: false }) }));
vi.mock("../hooks/useWorkspaceQuery", async (importOriginal) => ({
	// Keep useRemoteProjectQuery and its real cache/host-client path intact.
	...await importOriginal<typeof import("../hooks/useWorkspaceQuery")>(),
	useWorkspaceQuery: () => ({ data: [{ id: "shared", name: "Local", sessions: [] }], isSuccess: true, isError: false }),
	useCloudProjectsQuery: () => ({ data: undefined }),
}));
vi.mock("../hooks/useBoardPresentation", () => ({
	useBoardPresentation: () => ({ showStartup: false, showWelcome: false, showProjectEmpty: false, workspaceStartupState: "ready" }),
}));
vi.mock("../hooks/useProjectOrchestratorAction", () => ({
	useProjectOrchestratorAction: () => ({ isProjectRestarting: false, isProvisioning: false, openNewTask: vi.fn(), openOrchestrator: vi.fn() }),
}));
vi.mock("../lib/shell-context", () => ({
	useShellMaybe: () => ({ remoteHosts: [{ hostId: "box-a", label: "Saved A", status: "offline" }], retryRemoteHosts: vi.fn() }),
}));
vi.mock("./NotificationCenter", () => ({ NotificationCenter: () => null }));

const clients: QueryClient[] = [];
beforeEach(() => {
	useUiStore.setState({ developerMode: false, diagnostics: false, remoteHosts: true });
	localGet.mockReset(); navigate.mockReset(); fetchMock.mockReset();
	fetchMock.mockRejectedValue(new Error("Offline fixture must not use the network"));
	vi.stubGlobal("fetch", fetchMock);
});
afterEach(async () => {
	cleanup();
	for (const client of clients.splice(0)) client.clear();
	vi.unstubAllGlobals();
	await new Promise((resolve) => setTimeout(resolve, 0));
});

it.each(["restart", "combined-query eviction"])("hydrates offline Fleet from separate saved lists after %s", (scenario) => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	clients.push(client);
	const updatedAt = Date.now() - 12 * 60_000;
	for (const host of ["box-a", "box-b"]) {
		client.setQueryData(["remote-workspaces", host, "projects"], [{ id: "shared", name: host, path: "/project", orchestratorAgent: "codex" }], { updatedAt });
		client.setQueryData(["remote-workspaces", host, "sessions"], [
			{ id: "orch", projectId: "shared", kind: "orchestrator", displayName: `${host} orchestrator`, harness: "codex", status: "terminated", isTerminated: true, prs: [] },
			{ id: "manager", projectId: "shared", kind: "worker", parentSessionId: "orch", displayName: `${host} manager`, harness: "codex", status: "idle", prs: [] },
			{ id: "worker", projectId: "shared", kind: "worker", parentSessionId: "manager", displayName: `${host} worker`, harness: "codex", status: "working", prs: [] },
		].map((session) => ({ ...session, updatedAt: new Date(updatedAt).toISOString() })), { updatedAt });
	}
	if (scenario === "combined-query eviction") {
		client.setQueryData(remoteWorkspaceQueryKey("box-a"), []);
		client.removeQueries({ queryKey: remoteWorkspaceQueryKey("box-a"), exact: true });
	}
	expect(client.getQueryData(remoteWorkspaceQueryKey("box-a"))).toBeUndefined();
	expect(connectedHosts()).toEqual([]);
	render(<QueryClientProvider client={client}><TooltipProvider><SessionsBoard hostId="box-a" projectId="shared" /></TooltipProvider></QueryClientProvider>);
	// This exercises the real project query, rather than giving the board a mocked workspace.
	fireEvent.click(screen.getByRole("button", { name: "Fleet" }));
	expect(screen.getByRole("region", { name: "Session fleet" })).toHaveAttribute("data-host-offline");
	expect(screen.getByRole("button", { name: /box-a orchestrator.*Archived/ })).toBeVisible();
	expect(screen.getByRole("button", { name: /box-a manager.*Manager/ })).toBeVisible();
	expect(screen.getByRole("button", { name: /box-a worker.*Worker/ })).toBeVisible();
	expect(screen.queryByText("box-b worker")).toBeNull();
	fireEvent.click(screen.getByRole("button", { name: "Collapse box-a manager" }));
	expect(screen.queryByRole("button", { name: /box-a worker.*Worker/ })).toBeNull();
	expect(client.getQueryState(remoteWorkspaceQueryKey("box-a"))?.dataUpdatedAt).toBe(updatedAt);
	expect(localGet).not.toHaveBeenCalled();
	expect(fetchMock).not.toHaveBeenCalled();
});
