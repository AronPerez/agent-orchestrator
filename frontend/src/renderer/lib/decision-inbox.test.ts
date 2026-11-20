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
	dismissDecisionCall,
	fetchDecisionBoard,
	fetchDecisionDetail,
	fetchDecisionInboxCalls,
	fetchDecisionSearch,
	decisionOptionRequiresNote,
	mergeDecisionInboxSources,
	overruleDecisionCall,
	recordDecisionOutcome,
	retireDecisionCall,
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

	it("answers with an optional note through the machine that supplied the call", async () => {
		const answered = { ...call("dec_1", "2026-11-18T10:00:00Z"), state: "in_progress" as const, stateLabel: "In progress" };
		postMock.mockResolvedValue({ data: answered });

		await expect(answerDecisionInboxCall({
			sourceHostId: "remote-a",
			callId: "dec_1",
			option: 2,
			note: "Add a rollback step.",
		})).resolves.toEqual(answered);
		expect(postMock).toHaveBeenCalledWith("/api/v1/decisions/{id}/answer", {
			params: { path: { id: "dec_1" } },
			body: { choice: "option", option: 2, note: "Add a rollback step." },
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

describe("mergeDecisionInboxSources with this computer's states", () => {
	const remote = (calls: ReturnType<typeof call>[]): DecisionInboxSource[] => [
		{ hostId: "local", label: "This computer", calls: [] },
		{ hostId: "remote-a", label: "Laptop", calls },
	];

	it("drops remote copy closed locally", () => {
		const entries = mergeDecisionInboxSources(remote([call("dec_answered", "2026-11-18T09:00:00Z")]), new Map([["dec_answered", "in_progress"]]));
		expect(entries).toEqual([]);
	});

	it("keeps unknown remote call", () => {
		const entries = mergeDecisionInboxSources(remote([call("dec_elsewhere", "2026-11-18T09:00:00Z")]), new Map());
		expect(entries.map((entry) => entry.call.id)).toEqual(["dec_elsewhere"]);
		const stillOpen = mergeDecisionInboxSources(remote([call("dec_open", "2026-11-18T09:00:00Z")]), new Map([["dec_open", "needs_you"]]));
		expect(stillOpen.map((entry) => entry.call.id)).toEqual(["dec_open"]);
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

describe("decision actions API", () => {
	it("answers with Something else and a note through the same answer request", async () => {
		postMock.mockResolvedValue({ data: call("dec_1", "2026-11-18T10:00:00Z") });
		await answerDecisionInboxCall({ sourceHostId: "local", callId: "dec_1", choice: "other", note: "Use the vault" });
		expect(postMock).toHaveBeenCalledWith("/api/v1/decisions/{id}/answer", {
			params: { path: { id: "dec_1" } },
			body: { choice: "other", note: "Use the vault" },
		});
		await answerDecisionInboxCall({ sourceHostId: "local", callId: "dec_1", option: 2, note: "  " });
		expect(postMock).toHaveBeenLastCalledWith("/api/v1/decisions/{id}/answer", {
			params: { path: { id: "dec_1" } },
			body: { choice: "option", option: 2 },
		});
	});

	it("dismisses, records an outcome and retires on this computer", async () => {
		postMock.mockResolvedValue({ data: call("dec_1", "2026-11-18T10:00:00Z") });
		await dismissDecisionCall("dec_1", "Not needed");
		expect(postMock).toHaveBeenLastCalledWith("/api/v1/decisions/{id}/dismiss", { params: { path: { id: "dec_1" } }, body: { note: "Not needed" } });
		await dismissDecisionCall("dec_1", " ");
		expect(postMock).toHaveBeenLastCalledWith("/api/v1/decisions/{id}/dismiss", { params: { path: { id: "dec_1" } }, body: {} });
		await recordDecisionOutcome("dec_1", { forEntry: "dent_9", text: "Shipped", link: "https://example.com/pr/1" });
		expect(postMock).toHaveBeenLastCalledWith("/api/v1/decisions/{id}/outcome", {
			params: { path: { id: "dec_1" } },
			body: { forEntry: "dent_9", text: "Shipped", link: "https://example.com/pr/1" },
		});
		await retireDecisionCall("dec_1", false);
		expect(postMock).toHaveBeenLastCalledWith("/api/v1/decisions/{id}/retire", { params: { path: { id: "dec_1" } }, body: { retired: false } });
		expect(clientForSessionHostMock).toHaveBeenCalledWith("local");
	});

	it("surfaces the daemon's refusal", async () => {
		postMock.mockResolvedValue({ error: { code: "DECISION_WRONG_STATE", message: "Only a call in \"Needs you\" can be dismissed." } });
		await expect(dismissDecisionCall("dec_1", "")).rejects.toThrow("Only a call in \"Needs you\" can be dismissed.");
	});
});

describe("decisionOptionRequiresNote", () => {
	it("requires a note only for a plan-first call's Approve with changes", () => {
		const plan = { ...call("dec_1", "2026-11-18T10:00:00Z"), reference: "plan-first", options: [
			{ label: "Approve", consequence: "", recommended: true },
			{ label: "Approve with changes", consequence: "" },
		] };
		expect(decisionOptionRequiresNote(plan, 2)).toBe(true);
		expect(decisionOptionRequiresNote(plan, 1)).toBe(false);
		expect(decisionOptionRequiresNote({ ...plan, reference: undefined }, 2)).toBe(false);
	});
});

describe("overruleDecisionCall", () => {
	it("overrules on this computer with the trimmed note", async () => {
		postMock.mockResolvedValue({ data: call("dec_1", "2026-11-18T10:00:00Z") });
		await overruleDecisionCall("dec_1", "  Keep #12 open ");
		expect(clientForSessionHostMock).toHaveBeenCalledWith("local");
		expect(postMock).toHaveBeenCalledWith("/api/v1/decisions/{id}/overrule", { params: { path: { id: "dec_1" } }, body: { note: "Keep #12 open" } });
	});
});
