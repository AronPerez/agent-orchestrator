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
import { decisionInboxQueryKey, nextDoneExpiryDelay, useDecisionAction, useDecisionAnswer, useDecisionBoard, useDecisionDetail, useDecisionInbox, useDecisionSearch } from "./useDecisionInbox";

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
		expect(pathCalls(local, "/api/v1/decisions")).toBe(1);
		expect(remote.GET).toHaveBeenCalledTimes(1);
		// The remote-only call is looked up on this computer's ledger, once.
		await waitFor(() => expect(lookups(local)).toEqual(["dec_old"]));
		expect(lookups(remote)).toEqual([]);
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
			params: { path: { id: "dec_1" }, header: { "Idempotency-Key": expect.stringMatching(/^ans_/) } },
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

// Reads of path, not counting the bell's id lookup on this computer's ledger.
const pathCalls = (client: { GET: ReturnType<typeof vi.fn> }, path: string) =>
	client.GET.mock.calls.filter(([called, init]) => called === path && !isLookup(init)).length;
const isLookup = (init: unknown) => Boolean((init as { params?: { query?: { ids?: string } } } | undefined)?.params?.query?.ids);
const lookups = (client: { GET: ReturnType<typeof vi.fn> }) =>
	client.GET.mock.calls.filter(([, init]) => isLookup(init)).map(([, init]) => (init as { params: { query: { ids: string } } }).params.query.ids);

describe("shared lost-answer guard", () => {
	// The daemon can record an answer whose response is lost; if the re-read
	// fails too, every surface still shows the call as waiting.
	function lossyLocal() {
		let current = [decisionCall("dec_1", "2026-11-18T10:00:00Z")];
		let reads: "ok" | "fail" = "ok";
		const client = {
			GET: vi.fn().mockImplementation(() => reads === "fail"
				? Promise.reject(new TypeError("network down"))
				: Promise.resolve({ data: { calls: current } })),
			POST: vi.fn().mockImplementation(() => {
				current = [];
				reads = "fail";
				return Promise.reject(new TypeError("response lost"));
			}),
			recover: () => { reads = "ok"; },
		};
		clients.set("local", client);
		return client;
	}

	it("holds the bell after the panel's answer was lost, until a later read succeeds", async () => {
		connectedHostsMock.mockReturnValue([]);
		const client = lossyLocal();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const bell = renderHook(() => useDecisionInbox(true), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(bell.result.current.entries).toHaveLength(1));

		// The Decisions panel sends through the shared mutation and loses the response.
		const panel = renderHook(() => useDecisionAnswer(), { wrapper: wrapper(queryClient) });
		await act(async () => {
			await panel.result.current.mutateAsync({ sourceHostId: "local", callId: "dec_1", option: 1 }).catch(() => undefined);
		});
		panel.unmount();
		await waitFor(() => expect(bell.result.current.held.get("local:dec_1")).toBe("unconfirmed"));
		expect(bell.result.current.entries).toHaveLength(1);
		act(() => bell.result.current.answer(bell.result.current.entries[0], 1));
		expect(client.POST).toHaveBeenCalledTimes(1);

		client.recover();
		act(() => bell.result.current.retry());
		await waitFor(() => expect(bell.result.current.entries).toHaveLength(0));
		expect(bell.result.current.held.size).toBe(0);
		expect(client.POST).toHaveBeenCalledTimes(1);
	});

	it("refuses to send a held call through the shared mutation itself", async () => {
		connectedHostsMock.mockReturnValue([]);
		const client = lossyLocal();
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const answer = renderHook(() => useDecisionAnswer(), { wrapper: wrapper(queryClient) });
		await act(async () => {
			await answer.result.current.mutateAsync({ sourceHostId: "local", callId: "dec_1", option: 1 }).catch(() => undefined);
		});
		await act(async () => {
			await expect(answer.result.current.mutateAsync({ sourceHostId: "local", callId: "dec_1", option: 1 })).rejects.toThrow();
		});
		expect(client.POST).toHaveBeenCalledTimes(1);
	});
});

describe("lost answers across synced copies", () => {
	it("holds the local copy after an answer to the remote copy was lost, until the remote source settles it", async () => {
		connectedHostsMock.mockReturnValue(["remote-a"]);
		let localCalls: ReturnType<typeof decisionCall>[] = [];
		let remoteReads: "ok" | "fail" = "ok";
		let remoteCalls = [decisionCall("dec_1", "2026-11-18T10:00:00Z")];
		const local = {
			GET: vi.fn().mockImplementation(() => Promise.resolve({ data: { calls: localCalls } })),
			POST: vi.fn().mockResolvedValue({ data: decisionCall("dec_1", "2026-11-18T10:00:00Z") }),
		};
		const remote = {
			GET: vi.fn().mockImplementation(() => remoteReads === "fail"
				? Promise.reject(new TypeError("network down"))
				: Promise.resolve({ data: { calls: remoteCalls } })),
			POST: vi.fn().mockImplementation(() => {
				remoteCalls = [];
				remoteReads = "fail";
				return Promise.reject(new TypeError("response lost"));
			}),
		};
		clients.set("local", local);
		clients.set("remote-a", remote);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useDecisionInbox(true), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(result.current.entries.map((e) => e.sourceHostId)).toEqual(["remote-a"]));

		act(() => result.current.answer(result.current.entries[0], 1));
		await waitFor(() => expect(result.current.held.get("remote-a:dec_1")).toBe("unconfirmed"));

		// Sync brings the call to this computer; the bell now shows the local copy.
		localCalls = [decisionCall("dec_1", "2026-11-18T10:00:00Z")];
		act(() => result.current.retry());
		await waitFor(() => expect(result.current.entries.map((e) => e.sourceHostId)).toEqual(["local"]));
		expect(result.current.held.get("local:dec_1")).toBe("unconfirmed");
		act(() => result.current.answer(result.current.entries[0], 1));
		expect(local.POST).not.toHaveBeenCalled();

		// The remote source shows the answer recorded; the local copy is released once sync shows it too.
		remoteReads = "ok";
		act(() => result.current.retry());
		await waitFor(() => expect(remote.GET.mock.calls.length).toBeGreaterThan(2));
		expect(result.current.held.get("local:dec_1")).toBe("unconfirmed");
		localCalls = [];
		act(() => result.current.retry());
		await waitFor(() => expect(result.current.entries).toHaveLength(0));
		expect(local.POST).not.toHaveBeenCalled();
		expect(remote.POST).toHaveBeenCalledTimes(1);
	});
});

describe("idempotent answer retries", () => {
	// A proxy times out (504) while the daemon is still recording the answer; a
	// later read shows the call waiting, so the person sends again. The resend
	// must carry the first attempt's key so the daemon can return that answer.
	it("resends an uncertain answer with the same Idempotency-Key", async () => {
		connectedHostsMock.mockReturnValue([]);
		let gatewayTimeout = true;
		const client = {
			GET: vi.fn().mockImplementation(() => Promise.resolve({ data: { calls: [decisionCall("dec_1", "2026-11-18T10:00:00Z")] } })),
			POST: vi.fn().mockImplementation(() => gatewayTimeout
				? Promise.resolve({ error: { error: "gateway_timeout", code: "GATEWAY_TIMEOUT", message: "504" }, response: { status: 504 } })
				: Promise.resolve({ data: { ...decisionCall("dec_1", "2026-11-18T10:00:00Z"), state: "in_progress", stateLabel: "In progress" } })),
		};
		clients.set("local", client);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useDecisionInbox(true), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(result.current.entries).toHaveLength(1));

		act(() => result.current.answer(result.current.entries[0], 1));
		await waitFor(() => expect(client.POST).toHaveBeenCalledTimes(1));
		// The re-read after the failure still shows the call waiting, so the bell lets the person send again.
		await waitFor(() => expect(result.current.held.size).toBe(0));
		gatewayTimeout = false;
		act(() => result.current.answer(result.current.entries[0], 1));
		await waitFor(() => expect(client.POST).toHaveBeenCalledTimes(2));

		const keyOf = (call: unknown[]) => (call[1] as { params: { header?: Record<string, string> } }).params.header?.["Idempotency-Key"];
		const first = keyOf(client.POST.mock.calls[0]);
		expect(first).toEqual(expect.any(String));
		expect(keyOf(client.POST.mock.calls[1])).toBe(first);
	});
});

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

describe("decision page mutations", () => {
	it("shares the bell's answer mutation and refreshes the board after it", async () => {
		const local = setupLedger("local", [decisionCall("dec_1", "2026-11-18T09:00:00Z")]);
		local.POST.mockResolvedValue({ data: { ...decisionCall("dec_1", "2026-11-18T09:00:00Z"), state: "in_progress" } });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => ({ board: useDecisionBoard(), answer: useDecisionAnswer() }), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(result.current.board.data).toBeDefined());
		const before = pathCalls(local, "/api/v1/decisions/board");

		await act(() => result.current.answer.mutateAsync({ sourceHostId: "local", callId: "dec_1", choice: "other", note: "Vault" }));
		expect(local.POST).toHaveBeenCalledTimes(1);
		expect(local.POST).toHaveBeenCalledWith("/api/v1/decisions/{id}/answer", { params: { path: { id: "dec_1" }, header: { "Idempotency-Key": expect.stringMatching(/^ans_/) } }, body: { choice: "other", note: "Vault" } });
		await waitFor(() => expect(pathCalls(local, "/api/v1/decisions/board")).toBe(before + 1));
	});

	it("runs dismiss, outcome and retire against this computer and refreshes after success or failure", async () => {
		const local = setupLedger("local", [decisionCall("dec_1", "2026-11-18T09:00:00Z")]);
		local.POST.mockResolvedValue({ data: decisionCall("dec_1", "2026-11-18T09:00:00Z") });
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => ({ board: useDecisionBoard(), action: useDecisionAction() }), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(result.current.board.data).toBeDefined());

		await act(() => result.current.action.mutateAsync({ kind: "dismiss", callId: "dec_1", note: "No" }));
		await act(() => result.current.action.mutateAsync({ kind: "outcome", callId: "dec_1", forEntry: "dent_2", text: "Done", link: "" }));
		await act(() => result.current.action.mutateAsync({ kind: "retire", callId: "dec_1", retired: true }));
		expect(local.POST.mock.calls.map(([path]) => path)).toEqual([
			"/api/v1/decisions/{id}/dismiss",
			"/api/v1/decisions/{id}/outcome",
			"/api/v1/decisions/{id}/retire",
		]);
		const before = pathCalls(local, "/api/v1/decisions/board");
		local.POST.mockResolvedValue({ error: { code: "DECISION_WRONG_STATE", message: "Closed." } });
		await act(async () => {
			await expect(result.current.action.mutateAsync({ kind: "retire", callId: "dec_1", retired: true })).rejects.toThrow("Closed.");
		});
		await waitFor(() => expect(pathCalls(local, "/api/v1/decisions/board")).toBeGreaterThan(before));
	});

describe("board freshness", () => {
	it("refetches the board when its oldest Done card leaves the seven-day window, with no event", async () => {
		const outcomeAt = new Date(Date.now() - 7 * 24 * 60 * 60 * 1000 + 1_200).toISOString();
		const local = setupLedger("local", [{ ...decisionCall("dec_done", "2026-11-01T09:00:00Z"), state: "done", stateLabel: "Done", outcomeAt } as unknown as ReturnType<typeof decisionCall>]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		renderHook(() => useDecisionBoard(), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(pathCalls(local, "/api/v1/decisions/board")).toBe(1));
		await waitFor(() => expect(pathCalls(local, "/api/v1/decisions/board")).toBe(2), { timeout: 4_000 });
	});
});

	it("times the refresh from the oldest non-retired Done card only", () => {
		const now = Date.parse("2026-11-18T12:00:00Z");
		const week = 7 * 24 * 60 * 60 * 1000;
		const done = (outcomeAt: string, retired = false) => ({ ...decisionCall("d", "2026-11-01T00:00:00Z"), state: "done" as const, outcomeAt, retired });
		const boardOf = (calls: unknown[]) => ({ calls, needsYouCount: 0, servingHostId: "h", doneSince: "" }) as never;
		expect(nextDoneExpiryDelay(undefined, now)).toBe(false);
		expect(nextDoneExpiryDelay(boardOf([decisionCall("n", "2026-11-18T00:00:00Z")]), now)).toBe(false);
		expect(nextDoneExpiryDelay(boardOf([done(new Date(now - week + 60_000).toISOString(), true)]), now)).toBe(false);
		expect(nextDoneExpiryDelay(boardOf([done(new Date(now - week + 60_000).toISOString()), done(new Date(now - week + 120_000).toISOString())]), now)).toBe(60_250);
		expect(nextDoneExpiryDelay(boardOf([done(new Date(now - week - 1).toISOString())]), now)).toBe(60_000);
		expect(nextDoneExpiryDelay(boardOf([done(new Date(now).toISOString())]), now)).toBe(24 * 60 * 60 * 1000);
	});
});

describe("answer reconciliation", () => {
	it("stays pending after a failed answer until this computer's calls are re-read", async () => {
		const local = setupLedger("local", [decisionCall("dec_1", "2026-11-18T09:00:00Z")]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => ({ board: useDecisionBoard(), answer: useDecisionAnswer() }), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(result.current.board.data).toBeDefined());
		let finishRead!: () => void;
		const plain = local.GET.getMockImplementation()!;
		local.GET.mockImplementation((path: string, request?: unknown) => path === "/api/v1/decisions/board"
			? new Promise((done) => { finishRead = () => done(plain(path, request)); })
			: plain(path, request));
		// The daemon may have committed the answer even though its response was lost.
		local.POST.mockRejectedValue(new Error("connection reset"));
		act(() => { result.current.answer.mutate({ sourceHostId: "local", callId: "dec_1", option: 1 }); });
		await waitFor(() => expect(pathCalls(local, "/api/v1/decisions/board")).toBe(2));
		expect(result.current.answer.isPending).toBe(true);
		await act(async () => { finishRead(); });
		await waitFor(() => expect(result.current.answer.isError).toBe(true));
	});

	it("takes a confirmed dismissal off the board and its count when the re-read fails", async () => {
		const local = setupLedger("local", [decisionCall("dec_1", "2026-11-18T09:00:00Z"), decisionCall("dec_2", "2026-11-18T09:30:00Z")]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => ({ board: useDecisionBoard(), action: useDecisionAction() }), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(result.current.board.data?.needsYouCount).toBe(2));
		local.POST.mockResolvedValue({ data: { ...decisionCall("dec_1", "2026-11-18T09:00:00Z"), state: "dismissed", stateLabel: "Dismissed" } });
		local.GET.mockResolvedValue({ error: { code: "DECISION_BOARD_FAILED", message: "Unable to load decisions" }, response: { status: 500 } });
		await act(() => result.current.action.mutateAsync({ kind: "dismiss", callId: "dec_1" }));
		await waitFor(() => expect(result.current.board.data?.calls.map((call) => call.id)).toEqual(["dec_2"]));
		expect(result.current.board.data?.needsYouCount).toBe(1);
	});

	it("keeps the confirmed call when the re-read after an answer fails", async () => {
		const local = setupLedger("local", [decisionCall("dec_1", "2026-11-18T09:00:00Z")]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => ({ detail: useDecisionDetail("dec_1"), answer: useDecisionAnswer(), action: useDecisionAction() }), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(result.current.detail.data?.call.state).toBe("needs_you"));
		local.POST.mockResolvedValue({ data: { ...decisionCall("dec_1", "2026-11-18T09:00:00Z"), state: "in_progress", stateLabel: "In progress" } });
		local.GET.mockResolvedValue({ error: { code: "DECISION_GET_FAILED", message: "Unable to get decision" }, response: { status: 500 } });
		await act(() => result.current.answer.mutateAsync({ sourceHostId: "local", callId: "dec_1", option: 1 }));
		await waitFor(() => expect(result.current.detail.data?.call.state).toBe("in_progress"));

		local.POST.mockResolvedValue({ data: { ...decisionCall("dec_1", "2026-11-18T09:00:00Z"), state: "done", stateLabel: "Done" } });
		await act(() => result.current.action.mutateAsync({ kind: "outcome", callId: "dec_1", text: "Shipped" }));
		await waitFor(() => expect(result.current.detail.data?.call.state).toBe("done"));
	});
});
