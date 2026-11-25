import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appI18n } from "../i18n";
import { FleetSessionDetails } from "./FleetSessionDetails";
import type { WorkspaceSession } from "../types/workspace";
import { forgetRemoteHost, syncRemoteCache } from "../lib/remote-chat-cache";
import { LOCAL_HOST } from "../lib/hosts";

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

const orchestrator: WorkspaceSession = {
	id: "orch",
	workspaceId: "p",
	updatedAt: "2026-11-03T00:00:00Z",
	prs: [],
	workspaceName: "P",
	title: "Orchestrator",
	kind: "orchestrator",
	provider: "codex",
	status: "working",
};
const worker: WorkspaceSession = {
	...orchestrator,
	id: "worker",
	parentSessionId: "manager",
	title: "Implement login",
	kind: "worker",
	issueId: "TASK-1",
};
const names = new Map([["orch", "Orchestrator"], ["manager", "Release coordination"], ["worker", "Implement login"]]);
const clients: QueryClient[] = [];

function renderDetails(
	hostId?: string,
	session = orchestrator,
	client = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
	parentName?: string,
) {
	const open = vi.fn();
	const close = vi.fn();
	clients.push(client);
	const view = (host?: string) => <QueryClientProvider client={client}>
		<FleetSessionDetails
			key={host ?? "local"}
			hostId={host}
			onClose={close}
			onOpen={open}
			parentName={parentName}
			roleLabel="Worker"
			session={session}
			sessionNames={names}
			statusLabel="Working"
		/>
	</QueryClientProvider>;
	const rendered = render(view(hostId));
	return { ...rendered, open, close, client, showHost: (host?: string) => rendered.rerender(view(host)) };
}
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

describe("FleetSessionDetails", () => {
	it("localizes its labels", async () => {
		await appI18n.changeLanguage("es");
		try {
			renderDetails();
			expect(screen.getByRole("group", { name: appI18n.t("fleet.selected") })).toBeInTheDocument();
			expect(screen.getByText(appI18n.t("fleet.work"))).toBeInTheDocument();
		} finally {
			await appI18n.changeLanguage("en");
		}
	});
	it("shows the session's work and parent, opens it and closes on Escape", async () => {
		const { open, close } = renderDetails(undefined, worker, undefined, "Release coordination");
		expect(screen.getByText("TASK-1")).toBeInTheDocument();
		expect(screen.getByText("Worker · Working")).toBeInTheDocument();
		expect(screen.getByText("Spawned by Release coordination")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Open session" }));
		expect(open).toHaveBeenCalledTimes(1);
		await waitFor(() =>
			expect(get).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/coordination", {
				params: { path: { sessionId: "worker" } }, signal: expect.any(AbortSignal),
			}),
		);
		fireEvent.keyDown(screen.getByRole("button", { name: "Open session" }), { key: "Escape" });
		expect(close).toHaveBeenCalledTimes(1);
	});
	it("shows source, target, recorded evidence and truncation without claiming action", async () => {
		get.mockResolvedValue({
			data: {
				hasMore: true,
				events: [{ id: "m", kind: "message", sourceSessionId: "manager", targetSessionId: "worker", state: "recorded", summary: "Check CI", createdAt: "2026-11-03T01:00:00Z" }],
			},
		});
		renderDetails();
		expect(await screen.findByText("Check CI")).toBeInTheDocument();
		expect(screen.getByText("Release coordination → Implement login")).toBeInTheDocument();
		expect(screen.getByText("Latest 50 recorded events")).toBeInTheDocument();
		expect(screen.getByText(/does not mean acted on/)).toBeInTheDocument();
	});
	it("shows request failure and supports retry", async () => {
		let calls = 0;
		get.mockImplementation(() => Promise.resolve(++calls === 1 ? { error: { code: "failed" } } : { data: { events: [], hasMore: false } }));
		renderDetails();
		expect(await screen.findByRole("alert")).toHaveTextContent("Could not load communication");
		fireEvent.click(screen.getByRole("button", { name: "Retry" }));
		expect(await screen.findByText("No recorded communication in this history.")).toBeInTheDocument();
	});
	it("shows loading without inventing events", () => {
		get.mockReturnValue(new Promise(() => {}));
		renderDetails();
		expect(screen.getByRole("status")).toHaveTextContent("Loading communication");
		expect(screen.queryByText("No recorded communication in this history.")).toBeNull();
	});
	it("isolates identical session and event IDs on local, A and B", async () => {
		get.mockResolvedValue(history("Local communication"));
		remoteGet.mockImplementation((hostId) => Promise.resolve(history(`${hostId} communication`)));
		const { showHost, client } = renderDetails();
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
	it("reads this computer's history for the local machine key", async () => {
		renderDetails(LOCAL_HOST);
		await screen.findByText("No recorded communication in this history.");
		expect(resolveHost).toHaveBeenCalledWith(undefined);
		expect(remoteGet).not.toHaveBeenCalled();
	});
	it("aborts an old host request and ignores its late response after switching hosts", async () => {
		let finish!: (value: ReturnType<typeof history>) => void;
		let signal!: AbortSignal;
		remoteGet.mockImplementation((host, _path, options) => host === "box-a" ? new Promise((resolve) => {
			finish = resolve; signal = options.signal;
		}) : Promise.resolve(history("B history")));
		const { showHost } = renderDetails("box-a");
		await waitFor(() => expect(remoteGet).toHaveBeenCalledTimes(1));
		showHost("box-b");
		expect(signal.aborted).toBe(true);
		expect(await screen.findByText("B history")).toBeVisible();
		await act(async () => finish(history("Late A history")));
		expect(screen.queryByText("Late A history")).toBeNull();
		expect(get).not.toHaveBeenCalled();
	});
	it("cancels on disconnect, keeps cached history marked offline, and resolves the client again on reconnect", async () => {
		const { client, showHost } = renderDetails("box-a");
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
		renderDetails("box-a");
		expect(screen.getByRole("status")).toHaveTextContent("Offline");
		expect(screen.queryByText("No recorded communication in this history.")).toBeNull();
		expect(remoteGet).not.toHaveBeenCalled();
		expect(get).not.toHaveBeenCalled();
	});
	it.each(["revoked pairing", "removed account host"])("clears mounted offline history after %s cleanup", async (reason) => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		client.setQueryData(["session-coordination", "box-b", "orch"], history("Other host history").data);
		client.setQueryData(["session-coordination", "", "orch"], history("Local history").data);
		remoteGet.mockResolvedValue(history("Paired-host private communication"));
		const { showHost } = renderDetails("box-a", orchestrator, client);
		expect(await screen.findByText("Paired-host private communication")).toBeVisible();
		connected.hosts = ["box-b"];
		showHost("box-a"); // Same mounted observer; ordinary disconnect keeps its saved copy.
		expect(screen.getByRole("status")).toHaveTextContent("Offline");
		expect(screen.getByText("Paired-host private communication")).toBeVisible();
		expect(client.getQueryState(["session-coordination", "box-a", "orch"])?.fetchStatus).toBe("idle");
		await act(async () => {
			if (reason === "revoked pairing") await forgetRemoteHost(client, "box-a");
			else await syncRemoteCache(client, ["box-b"]);
		});
		expect(client.getQueryData(["session-coordination", "box-a", "orch"])).toBeUndefined();
		await waitFor(() => expect(screen.queryByText("Paired-host private communication")).toBeNull());
		expect(screen.getByRole("status")).not.toHaveTextContent("Showing the last saved copy");
		expect(screen.queryByText("No recorded communication in this history.")).toBeNull();
		expect(client.getQueryData(["session-coordination", "box-b", "orch"])).toEqual(history("Other host history").data);
		expect(client.getQueryData(["session-coordination", "", "orch"])).toEqual(history("Local history").data);
		expect(remoteGet).toHaveBeenCalledTimes(1);
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
		renderDetails("box-a");
		expect(await screen.findByText(message)).toBeVisible();
		expect(screen.queryByText("No recorded communication in this history.")).toBeNull();
		expect(get).not.toHaveBeenCalled();
	});
	it.each([404, 501])("recovers from HTTP %s using Retry without reconnecting the host", async (status) => {
		let finish!: (value: ReturnType<typeof history>) => void;
		remoteGet.mockResolvedValueOnce({ error: { code: status === 404 ? "NOT_FOUND" : "NOT_IMPLEMENTED" }, response: { status } });
		remoteGet.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
		const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } });
		const hosts = [...connected.hosts];
		renderDetails("box-a", orchestrator, client);
		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent(status === 404 ? "Communication history was not found on this host." : "Communication history is not supported by this host.");
		fireEvent.click(within(alert).getByRole("button", { name: "Retry" }));
		expect(await screen.findByRole("status")).toHaveTextContent("Loading communication");
		expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
		await act(async () => finish(history("History available after host upgrade")));
		expect(await screen.findByText("History available after host upgrade")).toBeVisible();
		expect(screen.queryByRole("alert")).toBeNull();
		expect(client.getQueryData(["session-coordination", "box-a", "orch"])).toEqual(history("History available after host upgrade").data);
		expect(connected.hosts).toEqual(hosts);
		expect(resolveHost.mock.calls).toEqual([["box-a"], ["box-a"]]);
		expect(remoteGet).toHaveBeenCalledTimes(2);
		expect(get).not.toHaveBeenCalled();
	});
	it("keeps old host sessions without parent fields usable when coordination is missing", async () => {
		remoteGet.mockResolvedValue({ error: { code: "NOT_FOUND" }, response: { status: 404 } });
		const { open } = renderDetails("box-a", { ...worker, parentSessionId: undefined });
		expect(await screen.findByText("Communication history was not found on this host.")).toBeVisible();
		expect(screen.getByText(/Older sessions may predate lineage tracking/)).toBeVisible();
		fireEvent.click(screen.getByRole("button", { name: "Open session" }));
		expect(open).toHaveBeenCalledTimes(1);
	});
	it("does not replace a remote failure with local preview communication", async () => {
		preview.enabled = true;
		remoteGet.mockResolvedValue({ error: { code: "NOT_IMPLEMENTED" }, response: { status: 501 } });
		renderDetails("box-a");
		expect(await screen.findByText("Communication history is not supported by this host.")).toBeVisible();
		expect(get).not.toHaveBeenCalled();
	});
	it.each([undefined, "box-a"])("shows PR, checks and artifact details on host %s", async (host) => {
		renderDetails(host, {
			...worker,
			prs: [{ url: "https://example.test/pr/44", number: 44, state: "open", ci: "passing", review: "approved", mergeability: "clean", reviewComments: false, updatedAt: worker.updatedAt }],
			artifactFiles: [{ kind: "file", name: "evidence.txt", path: "evidence.txt", size: 12, updatedAt: worker.updatedAt }],
		});
		expect(screen.getByRole("link", { name: "PR #44" })).toHaveAttribute("href", "https://example.test/pr/44");
		expect(screen.getByText("CI: passing · Review: approved")).toBeVisible();
		expect(screen.getByText("Artifacts: 1 · Open session to inspect")).toBeVisible();
		await screen.findByText("No recorded communication in this history.");
	});
});
