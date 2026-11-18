import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DecisionInboxSource } from "./decision-inbox";

const { clientForSessionHostMock, getMock, postMock } = vi.hoisted(() => ({
	clientForSessionHostMock: vi.fn(),
	getMock: vi.fn(),
	postMock: vi.fn(),
}));

vi.mock("./host-clients", () => ({ clientForSessionHost: clientForSessionHostMock }));

import {
	answerDecisionInboxCall,
	decisionKeys,
	fetchDecisionBoard,
	fetchDecisionDetail,
	fetchDecisionInboxCalls,
	fetchDecisionSearch,
	mergeDecisionInboxSources,
} from "./decision-inbox";

const call = (id: string, raisedAt: string) => ({
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

beforeEach(() => {
	getMock.mockReset();
	postMock.mockReset();
	clientForSessionHostMock.mockReset().mockReturnValue({ GET: getMock, POST: postMock });
});

describe("decision inbox API", () => {
	it("loads only calls that need the person from the selected machine", async () => {
		const calls = [call("dec_1", "2026-11-18T10:00:00Z")];
		getMock.mockResolvedValue({ data: { calls } });
		const signal = new AbortController().signal;

		await expect(fetchDecisionInboxCalls("remote-a", signal)).resolves.toEqual(calls);
		expect(clientForSessionHostMock).toHaveBeenCalledWith("remote-a");
		expect(getMock).toHaveBeenCalledWith("/api/v1/decisions", {
			params: { query: { scope: "needs_you" } },
			signal,
		});
	});

	it("answers through the machine that supplied the call", async () => {
		const answered = { ...call("dec_1", "2026-11-18T10:00:00Z"), state: "in_progress" as const, stateLabel: "In progress" };
		postMock.mockResolvedValue({ data: answered });

		await expect(answerDecisionInboxCall({ sourceHostId: "remote-a", callId: "dec_1", option: 2 })).resolves.toEqual(answered);
		expect(postMock).toHaveBeenCalledWith("/api/v1/decisions/{id}/answer", {
			params: { path: { id: "dec_1" } },
			body: { choice: "option", option: 2 },
		});
	});
});

describe("mergeDecisionInboxSources", () => {
	it("orders the oldest asks first and collapses replicated calls to the local copy", () => {
		const duplicate = call("dec_shared", "2026-11-18T11:00:00Z");
		const sources: DecisionInboxSource[] = [
			{ hostId: "local", label: "This computer", calls: [duplicate] },
			{ hostId: "remote-a", label: "Laptop", calls: [call("dec_old", "2026-11-18T09:00:00Z"), duplicate] },
		];

		expect(mergeDecisionInboxSources(sources).map(({ sourceHostId, call: item }) => [sourceHostId, item.id])).toEqual([
			["remote-a", "dec_old"],
			["local", "dec_shared"],
		]);
	});
});

describe("decision query keys", () => {
	it("keeps one root partitioned by host, with page views under this computer", () => {
		expect(decisionKeys.all).toEqual(["decision-inbox"]);
		// The bell's needs-you list keeps its original key.
		expect(decisionKeys.needsYou("remote-a")).toEqual(["decision-inbox", "remote-a"]);
		expect(decisionKeys.host("remote-a")).toEqual(["decision-inbox", "remote-a"]);
		expect(decisionKeys.board()).toEqual(["decision-inbox", "local", "board"]);
		expect(decisionKeys.detail("dec_1")).toEqual(["decision-inbox", "local", "detail", "dec_1"]);
		expect(decisionKeys.search("auth", 50)).toEqual(["decision-inbox", "local", "search", "auth", 50]);
		for (const key of [decisionKeys.board(), decisionKeys.detail("dec_1"), decisionKeys.search("auth", 50)]) {
			expect(key.slice(0, 2)).toEqual(decisionKeys.host("local"));
		}
	});
});

describe("decision page API", () => {
	it("loads the board snapshot from this computer", async () => {
		const board = { calls: [call("dec_1", "2026-11-18T10:00:00Z")], needsYouCount: 1, servingHostId: "h_local", doneSince: "2026-11-11T10:00:00Z" };
		getMock.mockResolvedValue({ data: board });
		const signal = new AbortController().signal;

		await expect(fetchDecisionBoard(signal)).resolves.toEqual(board);
		expect(clientForSessionHostMock).toHaveBeenCalledWith("local");
		expect(getMock).toHaveBeenCalledWith("/api/v1/decisions/board", { signal });
	});

	it("keeps an empty board as an empty list", async () => {
		getMock.mockResolvedValue({ data: { calls: [], needsYouCount: 0, servingHostId: "h_local", doneSince: "2026-11-11T10:00:00Z" } });
		await expect(fetchDecisionBoard()).resolves.toMatchObject({ calls: [], needsYouCount: 0 });
	});

	it("loads one call with its history, and reports a missing call as null", async () => {
		const detail = { call: call("dec_1", "2026-11-18T10:00:00Z"), history: [] };
		getMock.mockResolvedValueOnce({ data: detail, response: { status: 200 } });
		await expect(fetchDecisionDetail("dec_1")).resolves.toEqual(detail);
		expect(getMock).toHaveBeenCalledWith("/api/v1/decisions/{id}", { params: { path: { id: "dec_1" } }, signal: undefined });

		getMock.mockResolvedValueOnce({ error: { error: { code: "DECISION_NOT_FOUND", message: "Unknown decision" } }, response: { status: 404 } });
		await expect(fetchDecisionDetail("dec_gone")).resolves.toBeNull();

		getMock.mockResolvedValueOnce({ error: { error: { code: "DECISION_GET_FAILED", message: "Unable to get decision" } }, response: { status: 500 } });
		await expect(fetchDecisionDetail("dec_1")).rejects.toThrow();
	});

	it("searches every call with a bounded page", async () => {
		const calls = [call("dec_1", "2026-11-18T10:00:00Z")];
		getMock.mockResolvedValue({ data: { calls } });
		const signal = new AbortController().signal;

		await expect(fetchDecisionSearch("auth scheme", 50, signal)).resolves.toEqual(calls);
		expect(getMock).toHaveBeenCalledWith("/api/v1/decisions", { params: { query: { q: "auth scheme", limit: 50 } }, signal });
	});
});
