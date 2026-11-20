import type { DecisionCall } from "./decision-inbox";

/** The three board columns, in display order. */
/**
 * Every board lane, in display order. "decided" holds decisions a session made
 * for the person that still stand; it sits before In progress because an
 * overruled decision moves there, and is shown only when it has cards.
 */
export const DECISION_LANES = ["needs_you", "decided", "in_progress", "done"] as const;
export type DecisionLane = (typeof DECISION_LANES)[number];

/** Row key for calls with no work stream; it is always first in its band. */
export const PROJECT_WIDE_ROW = "@project";
/** Band key for calls that recorded no project. */
export const NO_PROJECT_BAND = "@no-project";

/** The card's "who or what" line. Components translate the fixed kinds. */
export type DecisionCardSecondary =
	| { kind: "who"; machine: string; session: string }
	| { kind: "choice"; label: string }
	| { kind: "something_else" };

/** The card's status line. Components translate and format these. */
export type DecisionCardStatus =
	| { kind: "waiting"; since: string }
	| { kind: "sent"; to: string; fallback: boolean }
	/** originHostId is set when another machine owns the call and must deliver it. */
	| { kind: "pending"; originHostId?: string }
	| { kind: "no_session" }
	| { kind: "outcome"; text: string }
	| { kind: "decided"; carriedOut: boolean; carriedOutAt?: string };

export type DecisionCardModel = {
	id: string;
	lane: DecisionLane;
	title: string;
	secondary: DecisionCardSecondary;
	status: DecisionCardStatus;
	changedAfterClose: boolean;
	/** Set when a decision made for the person was recorded outside the project's policy. */
	outsidePolicy?: string;
	call: DecisionCall;
	/** The timestamp the card's lane sorts by. */
	sortAt: string;
};

export type DecisionLaneCards = Record<DecisionLane, DecisionCardModel[]>;

export type DecisionStreamRow = {
	key: string;
	/** Undefined for the project's own row. */
	name?: string;
	/** Origin host id of a named stream. */
	origin?: string;
	/**
	 * The stream's machine, set only when another row in the band has the same
	 * name, so two machines' streams are told apart.
	 */
	machine?: string;
	projectWide: boolean;
	lanes: DecisionLaneCards;
	needsYouCount: number;
};

export type DecisionProjectBand = {
	key: string;
	/** Undefined only for the No project band, which components label. */
	name?: string;
	projectless: boolean;
	rows: DecisionStreamRow[];
	counts: Record<DecisionLane, number>;
	needsYouCount: number;
};

export type DecisionTopologyRow = {
	bandKey: string;
	rowKey: string;
	lanes: Record<DecisionLane, string[]>;
};

export type DecisionCardLocation = { card: DecisionCardModel; bandKey: string; rowKey: string };

export type DecisionBoardModel = {
	/** The lanes to draw: the decided lane only when it has cards. */
	lanes: DecisionLane[];
	bands: DecisionProjectBand[];
	totals: Record<DecisionLane, number>;
	cardById: Map<string, DecisionCardLocation>;
	/** Every row's card ids per lane, in display order, for keyboard movement. */
	topology: DecisionTopologyRow[];
};

const emptyCounts = (): Record<DecisionLane, number> => ({ needs_you: 0, decided: 0, in_progress: 0, done: 0 });
const emptyLanes = (): DecisionLaneCards => ({ needs_you: [], decided: [], in_progress: [], done: [] });

function laneOf(call: DecisionCall): DecisionLane | undefined {
	if (call.state === "needs_you" || call.state === "in_progress" || call.state === "decided") return call.state;
	if (call.state === "done" && !call.retired) return "done";
	return undefined;
}

function sortTimeOf(call: DecisionCall, lane: DecisionLane): string {
	if (lane === "in_progress") return call.answeredAt ?? call.raisedAt;
	if (lane === "done") return call.outcomeAt ?? call.raisedAt;
	return call.raisedAt;
}

const time = (iso: string) => {
	const at = Date.parse(iso);
	return Number.isFinite(at) ? at : 0;
};

/** Needs you waits longest first; the other lanes show the newest first. */
function compareCards(lane: DecisionLane) {
	return (a: DecisionCardModel, b: DecisionCardModel) => {
		const primary = lane === "needs_you" ? time(a.sortAt) - time(b.sortAt) : time(b.sortAt) - time(a.sortAt);
		if (primary !== 0) return primary;
		const raised = lane === "needs_you" ? time(a.call.raisedAt) - time(b.call.raisedAt) : time(b.call.raisedAt) - time(a.call.raisedAt);
		if (raised !== 0) return raised;
		return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
	};
}

function secondaryOf(call: DecisionCall, lane: DecisionLane): DecisionCardSecondary {
	if (lane === "needs_you" || lane === "decided") {
		return {
			kind: "who",
			machine: call.machineLabel || call.hostId,
			session: call.ownerSessionName || call.ownerSessionId,
		};
	}
	if (call.choice === "other") return { kind: "something_else" };
	return { kind: "choice", label: call.chosenLabel ?? "" };
}

function statusOf(call: DecisionCall, lane: DecisionLane, servingHostId?: string): DecisionCardStatus {
	if (lane === "needs_you") return { kind: "waiting", since: call.raisedAt };
	if (lane === "decided") {
		return { kind: "decided", carriedOut: Boolean(call.decided?.carriedOut), carriedOutAt: call.decided?.carriedOutAt ?? undefined };
	}
	if (lane === "done") return { kind: "outcome", text: call.outcome ?? "" };
	if (call.delivery === "sent") {
		return { kind: "sent", to: call.deliveredToName || call.deliveredTo || "", fallback: Boolean(call.deliveredFallback) };
	}
	if (call.delivery === "no_session") return { kind: "no_session" };
	if (servingHostId && call.hostId && call.hostId !== servingHostId) return { kind: "pending", originHostId: call.hostId };
	return { kind: "pending" };
}

/** Tracks the newest non-empty recorded label, by raise time then call id. */
type Label = { value?: string; at: number; id: string };
function offerLabel(current: Label, value: string | undefined, call: DecisionCall): Label {
	if (!value) return current;
	const at = time(call.raisedAt);
	if (current.value === undefined || at > current.at || (at === current.at && call.id > current.id)) {
		return { value, at, id: call.id };
	}
	return current;
}

type RowDraft = { row: DecisionStreamRow; label: Label; machine: Label; fallbackName?: string };
type BandDraft = { band: DecisionProjectBand; label: Label; rows: Map<string, RowDraft> };

const collator = new Intl.Collator(undefined, { sensitivity: "base", numeric: true });
const compareNames = (a: string | undefined, b: string | undefined, aKey: string, bKey: string) =>
	collator.compare(a ?? "", b ?? "") || (aKey < bKey ? -1 : aKey > bKey ? 1 : 0);

/**
 * Groups board calls into project bands and work-stream rows using only what
 * each call recorded. The same project on several machines shares a band; a
 * stream is keyed by its origin machine as well as its manager session id.
 */
export function buildDecisionBoard(calls: readonly DecisionCall[], servingHostId?: string): DecisionBoardModel {
	const bands = new Map<string, BandDraft>();
	const totals = emptyCounts();

	for (const call of calls) {
		const lane = laneOf(call);
		if (!lane) continue;
		const bandKey = call.projectId || NO_PROJECT_BAND;
		let band = bands.get(bandKey);
		if (!band) {
			band = {
				band: { key: bandKey, projectless: !call.projectId, rows: [], counts: emptyCounts(), needsYouCount: 0 },
				label: { at: Number.NEGATIVE_INFINITY, id: "" },
				rows: new Map(),
			};
			bands.set(bandKey, band);
		}
		band.label = offerLabel(band.label, call.projectName, call);

		const streamId = call.streamSessionId;
		const rowKey = streamId ? `${call.hostId}:${streamId}` : PROJECT_WIDE_ROW;
		let row = band.rows.get(rowKey);
		if (!row) {
			row = {
				row: {
					key: rowKey,
					origin: streamId ? call.hostId : undefined,
					projectWide: !streamId,
					lanes: emptyLanes(),
					needsYouCount: 0,
				},
				label: { at: Number.NEGATIVE_INFINITY, id: "" },
				machine: { at: Number.NEGATIVE_INFINITY, id: "" },
				fallbackName: streamId,
			};
			band.rows.set(rowKey, row);
		}
		if (streamId) {
			row.label = offerLabel(row.label, call.streamName, call);
			row.machine = offerLabel(row.machine, call.machineLabel || call.hostId, call);
		}

		const card: DecisionCardModel = {
			id: call.id,
			lane,
			title: call.title,
			secondary: secondaryOf(call, lane),
			status: statusOf(call, lane, servingHostId),
			changedAfterClose: Boolean(call.changedAfterClose),
			outsidePolicy: call.decided?.outsidePolicy || undefined,
			call,
			sortAt: sortTimeOf(call, lane),
		};
		row.row.lanes[lane].push(card);
		band.band.counts[lane] += 1;
		totals[lane] += 1;
		if (lane === "needs_you") {
			row.row.needsYouCount += 1;
			band.band.needsYouCount += 1;
		}
	}

	const ordered = [...bands.values()].map(({ band, label, rows }) => {
		band.name = band.projectless ? undefined : label.value ?? band.key;
		const drafts = [...rows.values()];
		for (const { row, label: rowLabel, fallbackName } of drafts) {
			if (!row.projectWide) row.name = rowLabel.value ?? fallbackName;
			for (const lane of DECISION_LANES) row.lanes[lane].sort(compareCards(lane));
		}
		const nameCounts = new Map<string, number>();
		for (const { row } of drafts) {
			if (row.name !== undefined) nameCounts.set(row.name, (nameCounts.get(row.name) ?? 0) + 1);
		}
		for (const { row, machine } of drafts) {
			if (row.name !== undefined && (nameCounts.get(row.name) ?? 0) > 1) row.machine = machine.value;
		}
		band.rows = drafts
			.map(({ row }) => row)
			.sort((a, b) => {
				if (a.projectWide !== b.projectWide) return a.projectWide ? -1 : 1;
				const needs = Number(b.needsYouCount > 0) - Number(a.needsYouCount > 0);
				return needs || compareNames(a.name, b.name, a.key, b.key);
			});
		return band;
	});
	ordered.sort((a, b) => {
		const needs = Number(b.needsYouCount > 0) - Number(a.needsYouCount > 0);
		if (needs) return needs;
		if (a.projectless !== b.projectless) return a.projectless ? 1 : -1;
		return compareNames(a.name, b.name, a.key, b.key);
	});

	const cardById = new Map<string, DecisionCardLocation>();
	const topology: DecisionTopologyRow[] = [];
	for (const band of ordered) {
		for (const row of band.rows) {
			const ids: Record<DecisionLane, string[]> = { needs_you: [], decided: [], in_progress: [], done: [] };
			for (const lane of DECISION_LANES) {
				for (const card of row.lanes[lane]) {
					ids[lane].push(card.id);
					cardById.set(card.id, { card, bandKey: band.key, rowKey: row.key });
				}
			}
			topology.push({ bandKey: band.key, rowKey: row.key, lanes: ids });
		}
	}
	const lanes = DECISION_LANES.filter((lane) => lane !== "decided" || totals.decided > 0);
	return { lanes, bands: ordered, totals, cardById, topology };
}

export type DecisionFocusDirection = "up" | "down" | "left" | "right";

function locate(topology: readonly DecisionTopologyRow[], id: string) {
	for (let row = 0; row < topology.length; row += 1) {
		for (const lane of DECISION_LANES) {
			const index = (topology[row]!.lanes[lane] ?? []).indexOf(id);
			if (index >= 0) return { row, lane, index };
		}
	}
	return undefined;
}

const laneIds = (row: DecisionTopologyRow, lane: DecisionLane) => row.lanes[lane] ?? [];
const column = (topology: readonly DecisionTopologyRow[], lane: DecisionLane) => topology.flatMap((row) => laneIds(row, lane));

/**
 * Arrow-key movement over the visible cards. Up/Down walk one lane down the
 * whole board; Left/Right go to the nearest card of the next lane in the same
 * row, passing empty lanes. At an edge focus stays where it is.
 */
export function moveDecisionFocus(topology: readonly DecisionTopologyRow[], currentId: string, direction: DecisionFocusDirection): string {
	const at = locate(topology, currentId);
	if (!at) return currentId;
	if (direction === "up" || direction === "down") {
		const ids = column(topology, at.lane);
		const index = ids.indexOf(currentId) + (direction === "down" ? 1 : -1);
		return ids[index] ?? currentId;
	}
	const step = direction === "right" ? 1 : -1;
	const row = topology[at.row]!;
	for (let lane = DECISION_LANES.indexOf(at.lane) + step; lane >= 0 && lane < DECISION_LANES.length; lane += step) {
		const ids = row.lanes[DECISION_LANES[lane]!] ?? [];
		if (ids.length > 0) return ids[Math.min(at.index, ids.length - 1)]!;
	}
	return currentId;
}

/** The board's single tab stop: the selected card, else the first Needs you card, else the first card. */
export function initialDecisionFocusId(topology: readonly DecisionTopologyRow[], selectedId: string | undefined): string | undefined {
	if (selectedId && locate(topology, selectedId)) return selectedId;
	return column(topology, "needs_you")[0] ?? topology.flatMap((row) => DECISION_LANES.flatMap((lane) => laneIds(row, lane)))[0];
}

/**
 * Where focus goes when its card leaves the visible board (it changed state,
 * was removed, or its band folded): the next card that was below it in its
 * lane, else the one above, else the first visible card.
 */
export function replacementDecisionFocusId(
	before: readonly DecisionTopologyRow[],
	removedId: string,
	after: readonly DecisionTopologyRow[],
): string | undefined {
	const visible = new Set(after.flatMap((row) => DECISION_LANES.flatMap((lane) => laneIds(row, lane))));
	const at = locate(before, removedId);
	if (at) {
		const ids = column(before, at.lane);
		const index = ids.indexOf(removedId);
		const next = ids.slice(index + 1).find((id) => visible.has(id));
		if (next) return next;
		const previous = ids.slice(0, index).reverse().find((id) => visible.has(id));
		if (previous) return previous;
	}
	return initialDecisionFocusId(after, undefined);
}
