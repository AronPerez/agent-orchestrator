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
	fetchDecisionInboxCalls,
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
