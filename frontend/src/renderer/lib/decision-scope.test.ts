import { describe, expect, it } from "vitest";
import { buildDecisionBoard, NO_PROJECT_BAND, PROJECT_WIDE_ROW } from "./decision-board";
import type { DecisionCall } from "./decision-inbox";
import { decisionScopeTree, resolveDecisionScope, scopeDecisionBoard } from "./decision-scope";

function call(overrides: Partial<DecisionCall> & { id: string }): DecisionCall {
	return {
		link: `ao://decisions/${overrides.id}`,
		state: "needs_you",
		stateLabel: "Needs you",
		title: `Question ${overrides.id}`,
		context: "",
		options: [],
		links: [],
		hostId: "h_a",
		machineLabel: "mac-mini",
		ownerSessionId: "w-1",
		ownerSessionName: "worker 1",
		raisedAt: "2026-11-18T09:00:00Z",
		retired: false,
		...overrides,
	};
}
const cloud = { projectId: "cloud", projectName: "skyvern-cloud" };
const nango = { ...cloud, streamSessionId: "nango", streamName: "Nango" };
const board = buildDecisionBoard([
	call({ id: "wide", ...cloud }),
	call({ id: "need", ...nango }),
	call({ id: "sent", ...nango, state: "in_progress", chosenLabel: "Ship", delivery: "sent", answeredAt: "2026-11-18T10:00:00Z" }),
	call({ id: "fin", ...nango, state: "done", outcome: "Shipped", outcomeAt: "2026-11-18T11:00:00Z" }),
	call({ id: "auto", ...cloud, streamSessionId: "billing", streamName: "Billing", state: "decided", decided: { undo: "Reopen", carriedOut: true } }),
	call({ id: "shop", projectId: "shop", projectName: "Shop", state: "done", outcome: "Shipped", outcomeAt: "2026-11-18T11:30:00Z" }),
	call({ id: "loose" }),
]);
const ids = (model: ReturnType<typeof buildDecisionBoard>) => [...model.cardById.keys()].sort();

describe("resolveDecisionScope", () => {
	it("keeps a project, or a project and one of its streams, that are on the board", () => {
		expect(resolveDecisionScope(board, { project: "cloud" })).toEqual({ project: "cloud" });
		expect(resolveDecisionScope(board, { project: "cloud", stream: "h_a:nango" })).toEqual({ project: "cloud", stream: "h_a:nango" });
		expect(resolveDecisionScope(board, { project: NO_PROJECT_BAND })).toEqual({ project: NO_PROJECT_BAND });
	});

	it("falls back to the project when the stream is gone, and to everything when the project is gone", () => {
		expect(resolveDecisionScope(board, { project: "cloud", stream: "h_a:gone" })).toEqual({ project: "cloud" });
		expect(resolveDecisionScope(board, { project: "gone", stream: "h_a:nango" })).toEqual({});
		expect(resolveDecisionScope(board, { stream: "h_a:nango" })).toEqual({});
		expect(resolveDecisionScope(board, {})).toEqual({});
	});
});

describe("scopeDecisionBoard", () => {
	it("returns the board itself when nothing is chosen or the choice is not on it", () => {
		expect(scopeDecisionBoard(board, {})).toBe(board);
		expect(scopeDecisionBoard(board, { project: "gone" })).toBe(board);
	});

	it("narrows to one project with its own counts and lanes", () => {
		const scoped = scopeDecisionBoard(board, { project: "cloud" });
		expect(scoped.bands.map((band) => band.key)).toEqual(["cloud"]);
		expect(ids(scoped)).toEqual(["auto", "fin", "need", "sent", "wide"]);
		expect(scoped.totals).toEqual({ needs_you: 2, decided: 1, in_progress: 1, done: 1 });
		expect(scoped.lanes).toEqual(["needs_you", "decided", "in_progress", "done"]);
		expect(scoped.topology.every((row) => row.bandKey === "cloud")).toBe(true);
	});

	it("narrows to one stream, and drops the Decided for you lane when that stream has none", () => {
		const scoped = scopeDecisionBoard(board, { project: "cloud", stream: "h_a:nango" });
		expect(scoped.bands[0].rows.map((row) => row.key)).toEqual(["h_a:nango"]);
		expect(ids(scoped)).toEqual(["fin", "need", "sent"]);
		expect(scoped.totals).toEqual({ needs_you: 1, decided: 0, in_progress: 1, done: 1 });
		expect(scoped.bands[0].counts).toEqual(scoped.totals);
		expect(scoped.bands[0].needsYouCount).toBe(1);
		expect(scoped.lanes).toEqual(["needs_you", "in_progress", "done"]);
		expect(scoped.topology).toHaveLength(1);
	});

	it("narrows to the project's own row", () => {
		const scoped = scopeDecisionBoard(board, { project: "cloud", stream: PROJECT_WIDE_ROW });
		expect(ids(scoped)).toEqual(["wide"]);
	});
});

describe("decisionScopeTree", () => {
	it("lists every project and stream in board order with what needs the person and what is open", () => {
		const tree = decisionScopeTree(board);
		expect({ needsYou: tree.needsYou, open: tree.open }).toEqual({ needsYou: 3, open: 5 });
		expect(tree.projects.map((project) => [project.key, project.name, project.needsYou, project.open])).toEqual([
			["cloud", "skyvern-cloud", 2, 4],
			[NO_PROJECT_BAND, undefined, 1, 1],
			["shop", "Shop", 0, 0],
		]);
		expect(tree.projects[0].streams.map((stream) => [stream.key, stream.name, stream.projectWide, stream.needsYou, stream.open])).toEqual([
			[PROJECT_WIDE_ROW, undefined, true, 1, 1],
			["h_a:nango", "Nango", false, 1, 2],
			["h_a:billing", "Billing", false, 0, 1],
		]);
	});
});
