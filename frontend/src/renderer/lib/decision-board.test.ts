import { describe, expect, it } from "vitest";
import type { DecisionCall } from "./decision-inbox";
import { buildDecisionBoard, initialDecisionFocusId, moveDecisionFocus, replacementDecisionFocusId, type DecisionTopologyRow } from "./decision-board";

let serial = 0;
function call(overrides: Partial<DecisionCall> & { id: string }): DecisionCall {
	serial += 1;
	return {
		link: `ao://decisions/${overrides.id}`,
		state: "needs_you",
		stateLabel: "Needs you",
		title: `Question ${overrides.id}`,
		context: "Choose one.",
		options: [{ label: "Ship", consequence: "Release today.", recommended: true }],
		links: [],
		hostId: "h_a",
		machineLabel: "mac-mini",
		ownerSessionId: `worker-${serial}`,
		ownerSessionName: `Worker ${serial}`,
		raisedAt: "2026-11-18T10:00:00Z",
		retired: false,
		...overrides,
	};
}

const lanes = (board: ReturnType<typeof buildDecisionBoard>, bandIndex: number, rowIndex: number) => {
	const row = board.bands[bandIndex].rows[rowIndex];
	return {
		needs_you: row.lanes.needs_you.map((card) => card.id),
		in_progress: row.lanes.in_progress.map((card) => card.id),
		done: row.lanes.done.map((card) => card.id),
	};
};

describe("buildDecisionBoard", () => {
	it("is empty for an empty ledger", () => {
		const board = buildDecisionBoard([]);
		expect(board.bands).toEqual([]);
		expect(board.totals).toEqual({ needs_you: 0, in_progress: 0, done: 0 });
		expect(board.cardById.size).toBe(0);
		expect(board.topology).toEqual([]);
	});

	it("groups by recorded project, then by work stream per origin machine", () => {
		const board = buildDecisionBoard([
			call({ id: "shop_wide", projectId: "shop", projectName: "Shop" }),
			call({ id: "shop_a_epic", projectId: "shop", projectName: "Shop", streamSessionId: "epic", streamName: "Checkout", hostId: "h_a" }),
			// Same project and the same stream session id on another machine: one
			// band, but a separate row, because a stream lives on one machine.
			call({ id: "shop_b_epic", projectId: "shop", projectName: "Shop", streamSessionId: "epic", streamName: "Checkout", hostId: "h_b", machineLabel: "laptop" }),
			call({ id: "blog", projectId: "blog", projectName: "Blog", state: "done", stateLabel: "Done", outcome: "Merged", outcomeAt: "2026-11-18T11:00:00Z" }),
		]);
		expect(board.bands.map((band) => band.key)).toEqual(["shop", "blog"]);
		const shop = board.bands[0];
		expect(shop.name).toBe("Shop");
		expect(shop.rows.map((row) => row.key)).toEqual(["@project", "h_a:epic", "h_b:epic"]);
		expect(shop.rows[0].projectWide).toBe(true);
		expect(shop.rows[1]).toMatchObject({ name: "Checkout", origin: "h_a", projectWide: false });
		expect(shop.rows[2]).toMatchObject({ name: "Checkout", origin: "h_b" });
		expect(shop.counts).toEqual({ needs_you: 3, in_progress: 0, done: 0 });
		expect(board.bands[1].counts).toEqual({ needs_you: 0, in_progress: 0, done: 1 });
		expect(board.totals).toEqual({ needs_you: 3, in_progress: 0, done: 1 });
	});

	it("keeps calls without a recorded project in one No project band", () => {
		const board = buildDecisionBoard([
			call({ id: "loose_1" }),
			call({ id: "loose_2", hostId: "h_b" }),
			call({ id: "named", projectId: "zeta", projectName: "Zeta" }),
		]);
		expect(board.bands.map((band) => [band.key, band.name, band.projectless])).toEqual([
			["zeta", "Zeta", false],
			["@no-project", undefined, true],
		]);
		expect(lanes(board, 1, 0).needs_you).toEqual(["loose_1", "loose_2"]);
	});

	it("labels bands and rows from the newest recorded non-empty name, never live data", () => {
		const board = buildDecisionBoard([
			call({ id: "old", projectId: "shop", projectName: "Old shop", streamSessionId: "epic", streamName: "Old stream", raisedAt: "2026-11-18T08:00:00Z" }),
			call({ id: "new", projectId: "shop", projectName: "New shop", streamSessionId: "epic", streamName: "New stream", raisedAt: "2026-11-18T09:00:00Z" }),
			call({ id: "blank", projectId: "shop", projectName: "", streamSessionId: "epic", streamName: "", raisedAt: "2026-11-18T10:00:00Z" }),
			call({ id: "unnamed", projectId: "nameless", streamSessionId: "lead", hostId: "h_z" }),
		]);
		expect(board.bands.find((band) => band.key === "shop")?.name).toBe("New shop");
		expect(board.bands.find((band) => band.key === "shop")?.rows[0].name).toBe("New stream");
		const nameless = board.bands.find((band) => band.key === "nameless")!;
		expect(nameless.name).toBe("nameless");
		expect(nameless.rows[0].name).toBe("lead");
	});

	it("breaks equal-time name conflicts by call id", () => {
		const at = "2026-11-18T09:00:00Z";
		const board = buildDecisionBoard([
			call({ id: "dec_b", projectId: "shop", projectName: "Bravo", raisedAt: at }),
			call({ id: "dec_a", projectId: "shop", projectName: "Alpha", raisedAt: at }),
		]);
		expect(board.bands[0].name).toBe("Bravo");
	});

	it("orders bands and rows with Needs you first, then by name", () => {
		const board = buildDecisionBoard([
			call({ id: "a_done", projectId: "alpha", projectName: "Alpha", state: "done", outcomeAt: "2026-11-18T11:00:00Z" }),
			call({ id: "z_need", projectId: "zulu", projectName: "Zulu" }),
			call({ id: "m_prog", projectId: "mike", projectName: "Mike", state: "in_progress", answeredAt: "2026-11-18T11:00:00Z" }),
			call({ id: "z_s1", projectId: "zulu", projectName: "Zulu", streamSessionId: "s1", streamName: "Apples", state: "in_progress", answeredAt: "2026-11-18T11:00:00Z" }),
			call({ id: "z_s2", projectId: "zulu", projectName: "Zulu", streamSessionId: "s2", streamName: "Bananas" }),
			call({ id: "z_s3", projectId: "zulu", projectName: "Zulu", streamSessionId: "s3", streamName: "Avocados", state: "done", outcomeAt: "2026-11-18T11:00:00Z" }),
		]);
		expect(board.bands.map((band) => band.key)).toEqual(["zulu", "alpha", "mike"]);
		// The project's own row first, then named rows needing the person, then by name.
		expect(board.bands[0].rows.map((row) => row.key)).toEqual(["@project", "h_a:s2", "h_a:s1", "h_a:s3"]);
		expect(board.bands[0].needsYouCount).toBe(2);
		expect(board.bands[0].rows[1].needsYouCount).toBe(1);
	});

	it("sorts Needs you oldest first and the other lanes newest first, with deterministic ties", () => {
		const board = buildDecisionBoard([
			call({ id: "need_new", raisedAt: "2026-11-18T12:00:00Z" }),
			call({ id: "need_old", raisedAt: "2026-11-18T08:00:00Z" }),
			call({ id: "need_tie_b", raisedAt: "2026-11-18T09:00:00Z" }),
			call({ id: "need_tie_a", raisedAt: "2026-11-18T09:00:00Z" }),
			call({ id: "prog_old", state: "in_progress", answeredAt: "2026-11-18T09:00:00Z" }),
			call({ id: "prog_new", state: "in_progress", answeredAt: "2026-11-18T11:00:00Z" }),
			call({ id: "prog_noanswer", state: "in_progress", raisedAt: "2026-11-18T10:30:00Z" }),
			call({ id: "done_old", state: "done", outcomeAt: "2026-11-17T09:00:00Z" }),
			call({ id: "done_new", state: "done", outcomeAt: "2026-11-18T09:00:00Z" }),
		]);
		expect(lanes(board, 0, 0)).toEqual({
			needs_you: ["need_old", "need_tie_a", "need_tie_b", "need_new"],
			in_progress: ["prog_new", "prog_noanswer", "prog_old"],
			done: ["done_new", "done_old"],
		});
	});

	it("describes each card by lane, delivery sub-state and outcome", () => {
		const board = buildDecisionBoard([
			call({ id: "need", machineLabel: "mac-mini", ownerSessionName: "Worker 5512", ownerSessionId: "w-5512", raisedAt: "2026-11-18T07:00:00Z" }),
			call({ id: "need_noname", machineLabel: "", hostId: "h_raw", ownerSessionName: "", ownerSessionId: "w-9" }),
			call({ id: "sent", state: "in_progress", chosenLabel: "Ship", delivery: "sent", deliveredTo: "w-1", deliveredToName: "Worker 1" }),
			call({ id: "fallback", state: "in_progress", chosenLabel: "Ship", delivery: "sent", deliveredTo: "orch", deliveredFallback: true }),
			call({ id: "pending", state: "in_progress", chosenLabel: "Wait", delivery: "pending" }),
			call({ id: "orphan", state: "in_progress", choice: "other", delivery: "no_session" }),
			call({ id: "done", state: "done", chosenLabel: "Ship", outcome: "Merged PR #412", outcomeAt: "2026-11-18T11:00:00Z", changedAfterClose: true }),
		]);
		const card = (id: string) => board.cardById.get(id)!.card;
		expect(card("need")).toMatchObject({
			lane: "needs_you",
			title: "Question need",
			secondary: { kind: "who", machine: "mac-mini", session: "Worker 5512" },
			status: { kind: "waiting", since: "2026-11-18T07:00:00Z" },
			changedAfterClose: false,
		});
		expect(card("need_noname").secondary).toEqual({ kind: "who", machine: "h_raw", session: "w-9" });
		expect(card("sent")).toMatchObject({ secondary: { kind: "choice", label: "Ship" }, status: { kind: "sent", to: "Worker 1", fallback: false } });
		expect(card("fallback").status).toEqual({ kind: "sent", to: "orch", fallback: true });
		expect(card("pending").status).toEqual({ kind: "pending" });
		expect(card("orphan")).toMatchObject({ secondary: { kind: "something_else" }, status: { kind: "no_session" } });
		expect(card("done")).toMatchObject({
			lane: "done",
			secondary: { kind: "choice", label: "Ship" },
			status: { kind: "outcome", text: "Merged PR #412" },
			changedAfterClose: true,
		});
	});

	it("leaves history-only calls off the board", () => {
		const board = buildDecisionBoard([
			call({ id: "dismissed", state: "dismissed" }),
			call({ id: "withdrawn", state: "withdrawn" }),
			call({ id: "retired", state: "done", retired: true, outcomeAt: "2026-11-18T11:00:00Z" }),
			call({ id: "kept" }),
		]);
		expect([...board.cardById.keys()]).toEqual(["kept"]);
	});

	it("does not change the calls it was given", () => {
		const calls = [call({ id: "b", raisedAt: "2026-11-18T12:00:00Z" }), call({ id: "a", raisedAt: "2026-11-18T08:00:00Z" })];
		const snapshot = structuredClone(calls);
		buildDecisionBoard(calls);
		expect(calls).toEqual(snapshot);
	});

	it("records the visible card topology per row and lane for keyboard movement", () => {
		const board = buildDecisionBoard([
			call({ id: "n1", projectId: "p", projectName: "P" }),
			call({ id: "i1", projectId: "p", projectName: "P", state: "in_progress", answeredAt: "2026-11-18T11:00:00Z" }),
			call({ id: "s1", projectId: "p", projectName: "P", streamSessionId: "s", streamName: "S" }),
		]);
		expect(board.topology).toEqual([
			{ bandKey: "p", rowKey: "@project", lanes: { needs_you: ["n1"], in_progress: ["i1"], done: [] } },
			{ bandKey: "p", rowKey: "h_a:s", lanes: { needs_you: ["s1"], in_progress: [], done: [] } },
		]);
		expect(board.cardById.get("s1")).toMatchObject({ bandKey: "p", rowKey: "h_a:s" });
	});

	it("names the machine on rows whose stream names would otherwise look the same", () => {
		const board = buildDecisionBoard([
			call({ id: "here", projectId: "shop", streamSessionId: "epic", streamName: "Checkout", hostId: "h_a", machineLabel: "mac-mini" }),
			call({ id: "there", projectId: "shop", streamSessionId: "epic", streamName: "Checkout", hostId: "h_b", machineLabel: "laptop" }),
			call({ id: "solo", projectId: "shop", streamSessionId: "other", streamName: "Docs", hostId: "h_b", machineLabel: "laptop" }),
		]);
		const rows = board.bands[0].rows;
		expect(rows.map((row) => [row.name, row.machine])).toEqual([
			["Checkout", "mac-mini"],
			["Checkout", "laptop"],
			["Docs", undefined],
		]);
	});
});

describe("keyboard topology", () => {
	// Two bands; row r2 has an empty In progress lane.
	const topology: DecisionTopologyRow[] = [
		{ bandKey: "a", rowKey: "r1", lanes: { needs_you: ["n1", "n2"], in_progress: ["i1"], done: ["d1", "d2", "d3"] } },
		{ bandKey: "a", rowKey: "r2", lanes: { needs_you: ["n3"], in_progress: [], done: ["d4"] } },
		{ bandKey: "b", rowKey: "r3", lanes: { needs_you: [], in_progress: ["i2"], done: [] } },
	];

	it("moves up and down within a lane across rows and bands, stopping at the ends", () => {
		expect(moveDecisionFocus(topology, "n1", "down")).toBe("n2");
		expect(moveDecisionFocus(topology, "n2", "down")).toBe("n3");
		expect(moveDecisionFocus(topology, "n3", "down")).toBe("n3");
		expect(moveDecisionFocus(topology, "i1", "down")).toBe("i2");
		expect(moveDecisionFocus(topology, "i2", "up")).toBe("i1");
		expect(moveDecisionFocus(topology, "n1", "up")).toBe("n1");
	});

	it("moves left and right to the nearest card of the next lane in the same row", () => {
		expect(moveDecisionFocus(topology, "n2", "right")).toBe("i1");
		expect(moveDecisionFocus(topology, "d3", "left")).toBe("i1");
		expect(moveDecisionFocus(topology, "i1", "right")).toBe("d1");
		// An empty lane is passed over within the row; a row edge stays put.
		expect(moveDecisionFocus(topology, "n3", "right")).toBe("d4");
		expect(moveDecisionFocus(topology, "d1", "right")).toBe("d1");
		expect(moveDecisionFocus(topology, "i2", "left")).toBe("i2");
	});

	it("starts at the selected card, else the first Needs you card, else the first card", () => {
		expect(initialDecisionFocusId(topology, "d4")).toBe("d4");
		expect(initialDecisionFocusId(topology, "gone")).toBe("n1");
		expect(initialDecisionFocusId([{ bandKey: "b", rowKey: "r", lanes: { needs_you: [], in_progress: [], done: ["d9"] } }], undefined)).toBe("d9");
		expect(initialDecisionFocusId([], undefined)).toBeUndefined();
	});

	it("chooses where focus goes when its card leaves: next in lane, then previous, then first", () => {
		const after: DecisionTopologyRow[] = [
			{ bandKey: "a", rowKey: "r1", lanes: { needs_you: ["n2"], in_progress: ["i1"], done: ["d1", "d3"] } },
		];
		expect(replacementDecisionFocusId(topology, "d2", after)).toBe("d3");
		expect(replacementDecisionFocusId(topology, "n1", after)).toBe("n2");
		expect(replacementDecisionFocusId(topology, "d4", after)).toBe("d3");
		expect(replacementDecisionFocusId(topology, "i2", [])).toBeUndefined();
		expect(replacementDecisionFocusId(topology, "n3", [{ bandKey: "z", rowKey: "z", lanes: { needs_you: [], in_progress: ["x"], done: [] } }])).toBe("x");
	});
});
