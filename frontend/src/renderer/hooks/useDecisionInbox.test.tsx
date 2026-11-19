import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { PropsWithChildren } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { clients, connectedHostsMock } = vi.hoisted(() => ({
	clients: new Map<string, { GET: ReturnType<typeof vi.fn>; POST: ReturnType<typeof vi.fn> }>(),
	connectedHostsMock: vi.fn(),
}));

vi.mock("../lib/host-clients", () => ({
	clientForSessionHost: (hostId = "local") => clients.get(hostId),
	labelForHost: (hostId: string) => hostId === "remote-a" ? "Studio laptop" : hostId,
}));

vi.mock("./useHostConnection", () => ({ useConnectedHosts: () => connectedHostsMock() }));

import { decisionKeys } from "../lib/decision-inbox";
import { decisionInboxQueryKey, useDecisionBoard, useDecisionDetail, useDecisionInbox, useDecisionSearch } from "./useDecisionInbox";

const decisionCall = (id: string, raisedAt: string) => ({
	id,
	link: `ao://decisions/${id}`,
	state: "needs_you" as const,
	stateLabel: "Needs you",
	title: `Question ${id}`,
	context: "Choose one.",
	options: [
		{ label: "Ship", consequence: "Release today.", recommended: true },
		{ label: "Wait", consequence: "Release tomorrow." },
	],
	links: [],
	hostId: "h_origin",
	machineLabel: "Mac mini",
	ownerSessionId: "worker-1",
	ownerSessionName: "Checkout worker",
	raisedAt,
	retired: false,
});

function setupClient(hostId: string, calls: ReturnType<typeof decisionCall>[]) {
	let current = calls;
	const client = {
		GET: vi.fn().mockImplementation(() => Promise.resolve({ data: { calls: current } })),
		POST: vi.fn().mockImplementation((_path, request) => {
			const id = request.params.path.id;
			const answered = { ...current.find((call) => call.id === id)!, state: "in_progress", stateLabel: "In progress" };
			current = current.filter((call) => call.id !== id);
			return Promise.resolve({ data: answered });
		}),
	};
	clients.set(hostId, client);
	return client;
}

function wrapper(queryClient: QueryClient) {
	return function Wrapper({ children }: PropsWithChildren) {
		return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
	};
}

beforeEach(() => {
	clients.clear();
	connectedHostsMock.mockReset().mockReturnValue(["remote-a"]);
});

describe("useDecisionInbox", () => {
	it("loads and orders calls from this computer and connected machines while enabled", async () => {
		const local = setupClient("local", [decisionCall("dec_new", "2026-11-18T11:00:00Z")]);
		const remote = setupClient("remote-a", [decisionCall("dec_old", "2026-11-18T09:00:00Z")]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

		const { result } = renderHook(() => useDecisionInbox(true), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(result.current.entries).toHaveLength(2));

		expect(result.current.entries.map((entry) => [entry.sourceHostId, entry.call.id])).toEqual([
			["remote-a", "dec_old"],
			["local", "dec_new"],
		]);
		expect(local.GET).toHaveBeenCalledTimes(1);
		expect(remote.GET).toHaveBeenCalledTimes(1);
	});

	it("does not query any ledger until the bell opens", () => {
		const local = setupClient("local", []);
		const remote = setupClient("remote-a", []);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

		renderHook(() => useDecisionInbox(false), { wrapper: wrapper(queryClient) });

		expect(local.GET).not.toHaveBeenCalled();
		expect(remote.GET).not.toHaveBeenCalled();
	});

	it("answers on the source machine and removes the call while the list reconciles", async () => {
		setupClient("local", []);
		const remote = setupClient("remote-a", [decisionCall("dec_1", "2026-11-18T09:00:00Z")]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useDecisionInbox(true), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(result.current.entries).toHaveLength(1));

		act(() => {
			result.current.answer(result.current.entries[0], 2);
		});

		await waitFor(() => expect(remote.POST).toHaveBeenCalledWith("/api/v1/decisions/{id}/answer", {
			params: { path: { id: "dec_1" } },
			body: { choice: "option", option: 2 },
		}));
		await waitFor(() => expect(result.current.entries).toEqual([]));
		expect(remote.GET.mock.calls.length).toBeGreaterThan(1);
	});
});

/** A daemon that answers every decision route this page reads. */
function setupLedger(hostId: string, calls: ReturnType<typeof decisionCall>[]) {
	const client = {
		GET: vi.fn().mockImplementation((path: string, request?: { params?: { path?: { id?: string }; query?: { q?: string } } }) => {
			if (path === "/api/v1/decisions/board") {
				return Promise.resolve({ data: { calls, needsYouCount: calls.length, servingHostId: "h_local", doneSince: "2026-11-11T10:00:00Z" } });
			}
			if (path === "/api/v1/decisions/{id}") {
				const found = calls.find((call) => call.id === request?.params?.path?.id);
				return Promise.resolve(found
					? { data: { call: found, history: [] }, response: { status: 200 } }
					: { error: { error: { code: "DECISION_NOT_FOUND", message: "Unknown decision" } }, response: { status: 404 } });
			}
			const q = request?.params?.query?.q;
			return Promise.resolve({ data: { calls: q ? calls.filter((call) => call.title.includes(q)) : calls } });
		}),
		POST: vi.fn(),
	};
	clients.set(hostId, client);
	return client;
}

const pathCalls = (client: { GET: ReturnType<typeof vi.fn> }, path: string) =>
	client.GET.mock.calls.filter(([called]) => called === path).length;

describe("decision page hooks", () => {
	it("keeps the bell's needs-you key on the shared host branch", () => {
		expect(decisionInboxQueryKey("remote-a")).toEqual(decisionKeys.needsYou("remote-a"));
	});

	it("loads the board, one call's detail and search results from this computer", async () => {
		const local = setupLedger("local", [decisionCall("dec_1", "2026-11-18T09:00:00Z")]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => ({
			board: useDecisionBoard(),
			detail: useDecisionDetail("dec_1"),
			missing: useDecisionDetail("dec_gone"),
			search: useDecisionSearch("Question"),
		}), { wrapper: wrapper(queryClient) });

		await waitFor(() => expect(result.current.board.data?.calls).toHaveLength(1));
		await waitFor(() => expect(result.current.detail.data?.call.id).toBe("dec_1"));
		await waitFor(() => expect(result.current.missing.isSuccess).toBe(true));
		expect(result.current.missing.data).toBeNull();
		await waitFor(() => expect(result.current.search.data?.map((call) => call.id)).toEqual(["dec_1"]));
		expect(local.GET).toHaveBeenCalledWith("/api/v1/decisions", expect.objectContaining({ params: { query: { q: "Question", limit: 50 } } }));
	});

	it("does not fetch detail without a call or search without words", () => {
		const local = setupLedger("local", []);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		renderHook(() => ({ detail: useDecisionDetail(undefined), search: useDecisionSearch("   ") }), { wrapper: wrapper(queryClient) });
		expect(local.GET).not.toHaveBeenCalled();
	});

	it("refreshes this computer's bell list and board together, and a remote host alone", async () => {
		const local = setupLedger("local", [decisionCall("dec_1", "2026-11-18T09:00:00Z")]);
		const remote = setupLedger("remote-a", [decisionCall("dec_2", "2026-11-18T08:00:00Z")]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => ({ inbox: useDecisionInbox(true), board: useDecisionBoard() }), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(result.current.inbox.entries).toHaveLength(2));
		await waitFor(() => expect(result.current.board.data).toBeDefined());
		const localList = pathCalls(local, "/api/v1/decisions");
		const localBoard = pathCalls(local, "/api/v1/decisions/board");
		const remoteList = pathCalls(remote, "/api/v1/decisions");

		await act(() => queryClient.invalidateQueries({ queryKey: decisionKeys.host("remote-a") }));
		await waitFor(() => expect(pathCalls(remote, "/api/v1/decisions")).toBe(remoteList + 1));
		expect(pathCalls(local, "/api/v1/decisions/board")).toBe(localBoard);
		expect(pathCalls(local, "/api/v1/decisions")).toBe(localList);

		await act(() => queryClient.invalidateQueries({ queryKey: decisionKeys.host("local") }));
		await waitFor(() => expect(pathCalls(local, "/api/v1/decisions/board")).toBe(localBoard + 1));
		expect(pathCalls(local, "/api/v1/decisions")).toBe(localList + 1);
		expect(pathCalls(remote, "/api/v1/decisions")).toBe(remoteList + 1);
	});
});
