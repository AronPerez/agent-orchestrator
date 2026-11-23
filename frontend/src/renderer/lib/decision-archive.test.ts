import { describe, expect, it } from "vitest";
import { decisionArchiveEntries, filterDecisionArchive } from "./decision-archive";
import { buildDecisionBoard, PROJECT_WIDE_ROW } from "./decision-board";
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
	id, state: "done", stateLabel: "Done", chosenLabel: "Ship", outcome: `Shipped ${id}`, outcomeAt: `2026-11-18T11:${String(minute).padStart(2, "0")}:00Z`, ...extra,
});
const cloud = { projectId: "cloud", projectName: "skyvern-cloud" };
const nango = { ...cloud, streamSessionId: "nango", streamName: "Nango" };
const board = buildDecisionBoard([
	call({ id: "open", ...cloud }),
	done("a", 1, cloud),
	done("b", 5, nango),
	done("c", 3, nango),
	done("d", 9, { projectId: "shop", projectName: "Shop" }),
]);
const ids = (entries: ReturnType<typeof decisionArchiveEntries>) => entries.map((entry) => entry.card.id);
const text = (entry: ReturnType<typeof decisionArchiveEntries>[number]) => `${entry.card.title} ${entry.projectName ?? ""} ${entry.streamName ?? "Project-wide"}`;

describe("decisionArchiveEntries", () => {
	it("lists every done decision newest first, with where it belongs, and nothing still open", () => {
		const entries = decisionArchiveEntries(board);
		expect(ids(entries)).toEqual(["d", "b", "c", "a"]);
		expect(entries.find((entry) => entry.card.id === "b")).toMatchObject({ projectKey: "cloud", projectName: "skyvern-cloud", streamKey: "h_a:nango", streamName: "Nango", projectWide: false });
		expect(entries.find((entry) => entry.card.id === "a")).toMatchObject({ streamKey: PROJECT_WIDE_ROW, streamName: undefined, projectWide: true });
	});

	it("is empty for a board with nothing done", () => {
		expect(decisionArchiveEntries(buildDecisionBoard([call({ id: "open" })]))).toEqual([]);
	});
});

describe("filterDecisionArchive", () => {
	const entries = decisionArchiveEntries(board);

	it("keeps one stream when a scope is given", () => {
		expect(ids(filterDecisionArchive(entries, { project: "cloud", stream: "h_a:nango" }, "", text))).toEqual(["b", "c"]);
		expect(ids(filterDecisionArchive(entries, { project: "cloud", stream: PROJECT_WIDE_ROW }, "", text))).toEqual(["a"]);
		expect(ids(filterDecisionArchive(entries, undefined, "", text))).toEqual(["d", "b", "c", "a"]);
	});

	it("needs every search word, in any order and any case, in what the card shows", () => {
		expect(ids(filterDecisionArchive(entries, undefined, "NANGO question", text))).toEqual(["b", "c"]);
		expect(ids(filterDecisionArchive(entries, undefined, "  shop ", text))).toEqual(["d"]);
		expect(ids(filterDecisionArchive(entries, undefined, "nango shop", text))).toEqual([]);
		// "b" is in no project or stream name here, only in one title.
		expect(ids(filterDecisionArchive(entries, { project: "cloud", stream: "h_a:nango" }, "question b", text))).toEqual(["b"]);
	});
});
