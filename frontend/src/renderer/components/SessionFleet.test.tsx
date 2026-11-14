import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";
import { SessionFleet } from "./SessionFleet";
import type { WorkspaceSession } from "../types/workspace";

const { get, remoteGet, connected, resolveHost, preview } = vi.hoisted(() => ({
	get: vi.fn(), remoteGet: vi.fn(), connected: { hosts: ["box-a", "box-b"] }, resolveHost: vi.fn(), preview: { enabled: false },
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: get },
	apiErrorCode: (error: { code?: string }) => error?.code,
	apiErrorMessage: (error: { message?: string }, fallback = "Request failed") => error?.message ?? fallback,
}));
vi.mock("../hooks/useHostConnection", () => ({ useConnectedHosts: () => connected.hosts }));
vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: (hostId?: string) => {
		resolveHost(hostId);
		if (!hostId) return { GET: get };
		if (!connected.hosts.includes(hostId)) throw new Error("Host not connected");
		return { GET: (...args: unknown[]) => remoteGet(hostId, ...args) };
	},
}));
vi.mock("../lib/preview-mode", () => ({ get usesPreviewWorkspaceData() { return preview.enabled; } }));

const sessions: WorkspaceSession[] = [
	{
		id: "orch",
		workspaceId: "p",
		updatedAt: "2026-11-03T00:00:00Z",
		prs: [],
		workspaceName: "P",
		title: "Orchestrator",
		kind: "orchestrator",
		provider: "codex",
		status: "working",
	},
	{
		id: "manager",
		parentSessionId: "orch",
		workspaceId: "p",
		updatedAt: "2026-11-03T00:00:00Z",
		prs: [],
		workspaceName: "P",
		title: "Release coordination",
		kind: "worker",
		provider: "codex",
		status: "idle",
	},
	{
		id: "worker",
		parentSessionId: "manager",
		workspaceId: "p",
		updatedAt: "2026-11-03T00:00:00Z",
		prs: [],
		workspaceName: "P",
		title: "Implement login",
		kind: "worker",
		provider: "codex",
		status: "working",
		issueId: "TASK-1",
	},
];
const clients: QueryClient[] = [];
function renderFleet(hostId?: string, items = sessions, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
	const open = vi.fn();
	clients.push(client);
	const view = (host?: string) => <QueryClientProvider client={client}>
		<SessionFleet key={host ?? "local"} sessions={items.map((session) => ({ ...session, hostId: host }))} hostId={host} onOpen={open} />
	</QueryClientProvider>;
	const rendered = render(view(hostId));
	return { ...rendered, open, client, showHost: (host?: string) => rendered.rerender(view(host)) };
}
const show = (items = sessions) => renderFleet(undefined, items).open;
function history(summary: string) {
	return { data: { events: [{ id: "event-1", kind: "message", sourceSessionId: "orch", targetSessionId: "worker", summary, state: "recorded", createdAt: "2026-11-03T00:00:00Z" }], hasMore: false } };
}
beforeEach(() => {
	get.mockReset();
	get.mockResolvedValue({ data: { events: [], hasMore: false } });
	remoteGet.mockReset();
	remoteGet.mockResolvedValue({ data: { events: [], hasMore: false } });
	resolveHost.mockClear();
	connected.hosts = ["box-a", "box-b"];
	preview.enabled = false;
});
afterEach(async () => {
	cleanup();
	for (const client of clients.splice(0)) client.clear();
	await new Promise((resolve) => setTimeout(resolve, 0));
});

describe("SessionFleet", () => {
	it("localizes fleet chrome and accessibility labels", async () => {
		await appI18n.changeLanguage("es");
		try {
			show([]);
			expect(screen.getByRole("region", { name: "Flota de sesiones" })).toBeInTheDocument();
			expect(screen.getByText("No hay sesiones para mostrar.")).toBeInTheDocument();
		} finally {
			await appI18n.changeLanguage("en");
		}
	});
	it("selects a worker and opens the existing session", async () => {
		const open = show();
		fireEvent.click(screen.getByRole("button", { name: /Implement login.*Worker/ }));
		expect(screen.getByText("TASK-1")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Open session" }));
		expect(open).toHaveBeenCalledWith(sessions[2]);
		await waitFor(() =>
			expect(get).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/coordination", {
				params: { path: { sessionId: "worker" } }, signal: expect.any(AbortSignal),
			}),
		);
		fireEvent.click(screen.getByRole("button", { name: "Collapse Release coordination" }));
		expect(screen.queryByRole("button", { name: /Implement login.*Worker/ })).not.toBeInTheDocument();
	});
	it("shows source, target, recorded evidence and truncation without claiming action", async () => {
		get.mockResolvedValue({
			data: {
				hasMore: true,
				events: [
					{
						id: "m",
						kind: "message",
						sourceSessionId: "manager",
						targetSessionId: "worker",
						state: "recorded",
						summary: "Check CI",
						createdAt: "2026-11-03T01:00:00Z",
					},
				],
			},
		});
		show();
		expect(await screen.findByText("Check CI")).toBeInTheDocument();
		expect(screen.getByText("Release coordination → Implement login")).toBeInTheDocument();
		expect(screen.getByText("Latest 50 recorded events")).toBeInTheDocument();
		expect(screen.getByText(/does not mean acted on/)).toBeInTheDocument();
	});
	it("shows request failure and supports retry", async () => {
		get.mockResolvedValueOnce({ error: { code: "failed" } });
		show();
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not load communication");
		fireEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(await screen.findByText("No recorded communication in this history.")).toBeInTheDocument();
	});
	it("shows loading and an empty fleet without inventing events", () => {
		get.mockReturnValue(new Promise(() => {}));
		show();
		expect(screen.getByRole("status")).toHaveTextContent("Loading communication");
	});
	it("renders the empty fleet", () => {
		show([]);
		expect(screen.getByText("No sessions to display.")).toBeInTheDocument();
		expect(within(screen.getByRole("region")).queryByRole("complementary")).toBeNull();
		expect(get).not.toHaveBeenCalled();
	});
	it("isolates identical project/session/event IDs on local, A and B", async () => {
		get.mockResolvedValue(history("Local communication"));
		remoteGet.mockImplementation((hostId) => Promise.resolve(history(`${hostId} communication`)));
		const { showHost, client } = renderFleet();
		expect(await screen.findByText("Local communication")).toBeVisible();
		showHost("box-a");
		expect(screen.queryByText("Local communication")).toBeNull();
		expect(await screen.findByText("box-a communication")).toBeVisible();
		showHost("box-b");
		expect(screen.queryByText("box-a communication")).toBeNull();
		expect(await screen.findByText("box-b communication")).toBeVisible();
		for (const [host, summary] of [["", "Local communication"], ["box-a", "box-a communication"], ["box-b", "box-b communication"]]) {
			expect(client.getQueryData(["session-coordination", host, "orch"])).toEqual(history(summary).data);
		}
		expect(get).toHaveBeenCalledTimes(1);
		expect(remoteGet).toHaveBeenCalledWith("box-b", "/api/v1/sessions/{sessionId}/coordination", {
			params: { path: { sessionId: "orch" } }, signal: expect.any(AbortSignal),
		});
	});
	it("aborts an old host request and ignores its late response after switching hosts", async () => {
		let finish!: (value: ReturnType<typeof history>) => void;
		let signal!: AbortSignal;
		remoteGet.mockImplementation((host, _path, options) => host === "box-a" ? new Promise((resolve) => {
			finish = resolve; signal = options.signal;
		}) : Promise.resolve(history("B history")));
		const { showHost } = renderFleet("box-a");
		await waitFor(() => expect(remoteGet).toHaveBeenCalledTimes(1));
		showHost("box-b");
		expect(signal.aborted).toBe(true);
		expect(await screen.findByText("B history")).toBeVisible();
		await act(async () => finish(history("Late A history")));
		expect(screen.queryByText("Late A history")).toBeNull();
		expect(get).not.toHaveBeenCalled();
	});
	it("cancels on disconnect, keeps cached history marked offline, and resolves the client again on reconnect", async () => {
		const { client, showHost } = renderFleet("box-a");
		await screen.findByText("No recorded communication in this history.");
		let finish!: (value: ReturnType<typeof history>) => void;
		let signal!: AbortSignal;
		remoteGet.mockImplementationOnce((_host, _path, options) => new Promise((resolve) => {
			finish = resolve; signal = options.signal;
		}));
		void client.invalidateQueries({ queryKey: ["session-coordination", "box-a", "orch"] });
		await waitFor(() => expect(remoteGet).toHaveBeenCalledTimes(2));
		connected.hosts = [];
		showHost("box-a");
		expect(await screen.findByRole("status")).toHaveTextContent("Offline");
		await waitFor(() => expect(signal.aborted).toBe(true));
		await act(async () => finish(history("Late disconnected history")));
		expect(screen.queryByText("Late disconnected history")).toBeNull();
		connected.hosts = ["box-a"];
		showHost("box-a");
		await waitFor(() => expect(remoteGet).toHaveBeenCalledTimes(3));
		expect(resolveHost.mock.calls.filter(([host]) => host === "box-a")).toHaveLength(3);
		expect(get).not.toHaveBeenCalled();
	});
	it("does not fetch or invent empty communication for an offline host", () => {
		connected.hosts = [];
		renderFleet("box-a");
		expect(screen.getByRole("status")).toHaveTextContent("Offline");
		expect(screen.queryByText("No recorded communication in this history.")).toBeNull();
		expect(remoteGet).not.toHaveBeenCalled();
		expect(get).not.toHaveBeenCalled();
	});
	it.each([
		[404, "NOT_FOUND", "Communication history was not found on this host."],
		[501, "NOT_IMPLEMENTED", "Communication history is not supported by this host."],
		[501, "COORDINATION_UNAVAILABLE", "Communication history is not supported by this host."],
		[401, "DEVICE_REVOKED", "Could not load communication."],
		[502, "UPSTREAM_UNAVAILABLE", "Offline"],
	])("shows host response %s/%s without local fallback", async (status, code, message) => {
		remoteGet.mockResolvedValue({ error: { code }, response: { status } });
		renderFleet("box-a");
		expect(await screen.findByText(message)).toBeVisible();
		expect(screen.queryByText("No recorded communication in this history.")).toBeNull();
		expect(get).not.toHaveBeenCalled();
	});
	it("keeps old apiVersion=1 host sessions without parent fields usable when coordination is missing", async () => {
		remoteGet.mockResolvedValue({ error: { code: "NOT_FOUND" }, response: { status: 404 } });
		const { open } = renderFleet("box-a", [{ ...sessions[2], parentSessionId: undefined }]);
		expect(await screen.findByText("Communication history was not found on this host.")).toBeVisible();
		expect(screen.getByText(/Older sessions may predate lineage tracking/)).toBeVisible();
		fireEvent.click(screen.getByRole("button", { name: "Open session" }));
		expect(open).toHaveBeenCalledWith(expect.objectContaining({ id: "worker", hostId: "box-a" }));
	});
	it("does not replace a remote failure with local preview communication", async () => {
		preview.enabled = true;
		remoteGet.mockResolvedValue({ error: { code: "NOT_IMPLEMENTED" }, response: { status: 501 } });
		renderFleet("box-a");
		expect(await screen.findByText("Communication history is not supported by this host.")).toBeVisible();
		expect(screen.queryByText(appI18n.t("fleet.preview"))).toBeNull();
		expect(get).not.toHaveBeenCalled();
	});
	it.each([undefined, "box-a", "box-b"])("preserves archive, collapse, status, PR and artifact details on host %s", async (host) => {
		const items: WorkspaceSession[] = [
			{ ...sessions[0], status: "terminated", isTerminated: true },
			sessions[1],
			{ ...sessions[2], displayStatus: "Waiting for review", prs: [{ url: "https://example.test/pr/44", number: 44, state: "open", ci: "passing", review: "approved", mergeability: "clean", reviewComments: false, updatedAt: sessions[2].updatedAt }], artifactFiles: [{ kind: "file", name: "evidence.txt", path: "evidence.txt", size: 12, updatedAt: sessions[2].updatedAt }] },
			{ ...sessions[2], id: "old", title: "Unrelated archive", parentSessionId: undefined, status: "terminated", isTerminated: true },
		];
		renderFleet(host, items);
		expect(screen.getByRole("button", { name: /Orchestrator.*Archived/ })).toBeVisible();
		expect(screen.queryByRole("button", { name: /Unrelated archive/ })).toBeNull();
		fireEvent.click(screen.getByRole("button", { name: /Implement login.*Waiting for review/ }));
		expect(screen.getByRole("link", { name: "PR #44" })).toHaveAttribute("href", "https://example.test/pr/44");
		expect(screen.getByText("CI: passing · Review: approved")).toBeVisible();
		expect(screen.getByText("Artifacts: 1 · Open session to inspect")).toBeVisible();
		fireEvent.click(screen.getByRole("button", { name: "Collapse Release coordination" }));
		expect(screen.queryByRole("button", { name: /Implement login.*Waiting for review/ })).toBeNull();
		fireEvent.click(screen.getByRole("button", { name: "Show archived" }));
		expect(screen.getByRole("button", { name: /Unrelated archive.*Archived/ })).toBeVisible();
		await screen.findByText("No recorded communication in this history.");
	});
});
