import { beforeEach, describe, expect, it } from "vitest";
import { buildDecisionBoard, PROJECT_WIDE_ROW } from "./decision-board";
import {
	DECISION_DONE_SHOWN,
	DECISION_FOLD_STORAGE_KEY,
	decisionBandFoldKey,
	decisionRowFoldKey,
	foldAllDecisions,
	keepDecisionCardOpen,
	readDecisionFolds,
	rememberDecisionFold,
	resolveDecisionFolds,
	revealDecisionCard,
	visibleDecisionTopology,
	writeDecisionFolds,
} from "./decision-folds";
import type { DecisionCall } from "./decision-inbox";

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

const done = (id: string, minute: number, extra: Partial<DecisionCall> = {}) => call({
	id, state: "done", stateLabel: "Done", outcome: "Shipped", outcomeAt: `2026-11-18T11:${String(minute).padStart(2, "0")}:00Z`, ...extra,
});

const board = buildDecisionBoard([
	call({ id: "need", projectId: "cloud", projectName: "skyvern-cloud", streamSessionId: "nango", streamName: "Nango" }),
	done("old", 1, { projectId: "cloud", projectName: "skyvern-cloud", streamSessionId: "aide", streamName: "Billing epic" }),
	done("shop", 2, { projectId: "shop", projectName: "Shop" }),
]);
const cloud = decisionBandFoldKey("cloud");
const shop = decisionBandFoldKey("shop");
const nango = decisionRowFoldKey("cloud", "h_a:nango");
const aide = decisionRowFoldKey("cloud", "h_a:aide");
const shopWide = decisionRowFoldKey("shop", PROJECT_WIDE_ROW);

describe("resolveDecisionFolds", () => {
	it("starts a project or stream folded only when nothing in it is open", () => {
		expect([...resolveDecisionFolds(board, {})].sort()).toEqual([aide, shop, shopWide].sort());
	});

	it("lets a saved choice win in both directions", () => {
		const folded = resolveDecisionFolds(board, { [cloud]: true, [shop]: false, [aide]: false });
		expect(folded.has(cloud)).toBe(true);
		expect(folded.has(shop)).toBe(false);
		expect(folded.has(aide)).toBe(false);
		expect(folded.has(nango)).toBe(false);
	});

	it("never folds the selected call's project or stream by default, but respects a fold the person made", () => {
		expect([...resolveDecisionFolds(board, {}, "old")].sort()).toEqual([shop, shopWide].sort());
		expect([...resolveDecisionFolds(board, {}, "shop")].sort()).toEqual([aide]);
		expect(resolveDecisionFolds(board, { [aide]: true }, "old").has(aide)).toBe(true);
		expect(resolveDecisionFolds(board, { [cloud]: true }, "old").has(cloud)).toBe(true);
	});

	it("ignores saved choices for groups that are no longer on the board", () => {
		expect(resolveDecisionFolds(board, { [decisionBandFoldKey("gone")]: true }).has(decisionBandFoldKey("gone"))).toBe(false);
	});
});

describe("fold choices", () => {
	it("folds or opens every project and stream at once", () => {
		const folded = resolveDecisionFolds(board, foldAllDecisions(board, {}, true));
		expect([...folded].sort()).toEqual([aide, cloud, nango, shop, shopWide].sort());
		expect(resolveDecisionFolds(board, foldAllDecisions(board, {}, false)).size).toBe(0);
	});

	it("opens the project and stream of a call, and leaves others alone", () => {
		const start = { [cloud]: true };
		const next = revealDecisionCard(board, start, "old");
		const folded = resolveDecisionFolds(board, next);
		expect(folded.has(cloud)).toBe(false);
		expect(folded.has(aide)).toBe(false);
		expect(folded.has(shop)).toBe(true);
	});

	it("saves a default-folded group as open for a call, without touching a fold the person made", () => {
		expect(keepDecisionCardOpen(board, {}, "shop")).toEqual({ [shop]: false, [shopWide]: false });
		const explicit = { [shop]: true, [shopWide]: true };
		expect(keepDecisionCardOpen(board, explicit, "shop")).toBe(explicit);
		expect(keepDecisionCardOpen(board, { [shop]: true }, "shop")).toEqual({ [shop]: true, [shopWide]: false });
		// Nothing to save when the group is open by default, or the call is unknown.
		const none = {};
		expect(keepDecisionCardOpen(board, none, "need")).toBe(none);
		expect(keepDecisionCardOpen(board, none, "missing")).toBe(none);
	});

	it("returns the same choices when the call is already on screen or unknown", () => {
		const start = {};
		expect(revealDecisionCard(board, start, "need")).toBe(start);
		expect(revealDecisionCard(board, start, "missing")).toBe(start);
	});
});

describe("saved folds", () => {
	beforeEach(() => window.localStorage.clear());

	it("round-trips through local storage", () => {
		writeDecisionFolds(rememberDecisionFold({}, cloud, true));
		expect(JSON.parse(window.localStorage.getItem(DECISION_FOLD_STORAGE_KEY) ?? "{}")).toEqual({ [cloud]: true });
		expect(readDecisionFolds()).toEqual({ [cloud]: true });
	});

	it("reads damaged or missing storage as no choices", () => {
		expect(readDecisionFolds()).toEqual({});
		window.localStorage.setItem(DECISION_FOLD_STORAGE_KEY, "{not json");
		expect(readDecisionFolds()).toEqual({});
	});
});

describe("visibleDecisionTopology", () => {
	const many = buildDecisionBoard([
		call({ id: "open", projectId: "cloud", projectName: "skyvern-cloud" }),
		...[1, 2, 3, 4, 5].map((minute) => done(`d${minute}`, minute, { projectId: "cloud", projectName: "skyvern-cloud" })),
		call({ id: "other", projectId: "shop", projectName: "Shop" }),
	]);
	const wide = decisionRowFoldKey("cloud", PROJECT_WIDE_ROW);

	it("drops folded projects and streams", () => {
		expect(visibleDecisionTopology(many, new Set([shop])).map((row) => row.bandKey)).toEqual(["cloud"]);
		expect(visibleDecisionTopology(many, new Set([wide])).map((row) => row.bandKey)).toEqual(["shop"]);
	});

	it("keeps only the newest Done cards of a row, and every card of the other lanes", () => {
		expect(DECISION_DONE_SHOWN).toBe(3);
		const capped = visibleDecisionTopology(many, new Set()).find((row) => row.bandKey === "cloud")!;
		expect(capped.lanes.done).toEqual(["d5", "d4", "d3"]);
		expect(capped.lanes.needs_you).toEqual(["open"]);
	});

	it("always keeps the selected Done card, in its place, even past the cap", () => {
		const row = (selectedId: string) => visibleDecisionTopology(many, new Set(), selectedId).find((item) => item.bandKey === "cloud")!;
		expect(row("d1").lanes.done).toEqual(["d5", "d4", "d3", "d1"]);
		expect(row("d2").lanes.done).toEqual(["d5", "d4", "d3", "d2"]);
		expect(row("d4").lanes.done).toEqual(["d5", "d4", "d3"]);
	});
});
