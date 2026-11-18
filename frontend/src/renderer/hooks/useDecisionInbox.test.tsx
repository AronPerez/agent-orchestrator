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

import { useDecisionInbox } from "./useDecisionInbox";

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
			body: { choice: "option", option: 2, ifState: "needs_you" },
		}));
		await waitFor(() => expect(result.current.entries).toEqual([]));
		expect(remote.GET.mock.calls.length).toBeGreaterThan(1);
	});

	it("releases answer controls without waiting for an unrelated host refresh", async () => {
		connectedHostsMock.mockReturnValue(["remote-a", "remote-b"]);
		setupClient("local", []);
		const remoteA = setupClient("remote-a", [
			decisionCall("dec_1", "2026-11-18T09:00:00Z"),
			decisionCall("dec_2", "2026-11-18T10:00:00Z"),
		]);
		const remoteB = setupClient("remote-b", []);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useDecisionInbox(true), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(result.current.entries).toHaveLength(2));

		remoteB.GET.mockImplementation(() => new Promise(() => undefined));
		void queryClient.invalidateQueries({ queryKey: ["decision-inbox", "remote-b"], exact: true });
		await waitFor(() => expect(remoteB.GET).toHaveBeenCalledTimes(2));

		act(() => result.current.answer(result.current.entries[0], 1));
		await waitFor(() => expect(remoteA.POST).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(result.current.pending).toBeUndefined());

		const remaining = result.current.entries.find((entry) => entry.call.id === "dec_2");
		expect(remaining).toBeDefined();
		act(() => result.current.answer(remaining!, 2));
		await waitFor(() => expect(remoteA.POST).toHaveBeenCalledTimes(2));
		expect(remoteB.GET).toHaveBeenCalledTimes(2);
	});

	it("keeps a failed answer visible after reconciliation removes its call", async () => {
		setupClient("local", []);
		const remote = setupClient("remote-a", [decisionCall("dec_1", "2026-11-18T09:00:00Z")]);
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const { result } = renderHook(() => useDecisionInbox(true), { wrapper: wrapper(queryClient) });
		await waitFor(() => expect(result.current.entries).toHaveLength(1));

		remote.POST.mockRejectedValueOnce(new Error("The call changed."));
		remote.GET.mockResolvedValue({ data: { calls: [] } });
		act(() => result.current.answer(result.current.entries[0], 1));

		await waitFor(() => expect(result.current.entries).toEqual([]));
		expect(result.current.answerError?.message).toBe("The call changed.");

		act(() => result.current.clearAnswerError());
		await waitFor(() => expect(result.current.answerError).toBeUndefined());
	});
});
