import { DECISION_LANES, type DecisionBoardModel, type DecisionLane } from "./decision-board";

/**
 * The part of the Decisions board a person chose in the list beside it: one
 * project (its band key), or one stream of it (its row key). Empty means every
 * project. It lives in the route, so a link or a reload keeps it.
 */
export type DecisionScope = { project?: string; stream?: string };

const emptyCounts = (): Record<DecisionLane, number> => ({ needs_you: 0, decided: 0, in_progress: 0, done: 0 });
const openOf = (counts: Record<DecisionLane, number>) => counts.needs_you + counts.decided + counts.in_progress;

/**
 * The scope as far as the board can honour it. A stream that has left the
 * board falls back to its project, and a project that has left falls back to
 * everything, so a stale link still opens the board.
 */
export function resolveDecisionScope(model: DecisionBoardModel, scope: DecisionScope): DecisionScope {
	const band = scope.project === undefined ? undefined : model.bands.find((item) => item.key === scope.project);
	if (!band) return {};
	return scope.stream !== undefined && band.rows.some((row) => row.key === scope.stream)
		? { project: band.key, stream: scope.stream }
		: { project: band.key };
}

/**
 * The board narrowed to a scope: only that project's band, or only that
 * stream's row, with totals and lanes counted from what is left. Returns the
 * same model when the scope names nothing on the board.
 */
export function scopeDecisionBoard(model: DecisionBoardModel, scope: DecisionScope): DecisionBoardModel {
	const { project, stream } = resolveDecisionScope(model, scope);
	const band = model.bands.find((item) => item.key === project);
	if (!band) return model;
	const rows = stream === undefined ? band.rows : band.rows.filter((row) => row.key === stream);
	const rowKeys = new Set(rows.map((row) => row.key));
	const totals = emptyCounts();
	for (const row of rows) {
		for (const lane of DECISION_LANES) totals[lane] += row.counts[lane];
	}
	const kept = (item: { bandKey: string; rowKey: string }) => item.bandKey === band.key && rowKeys.has(item.rowKey);
	return {
		lanes: DECISION_LANES.filter((lane) => lane !== "decided" || totals.decided > 0),
		bands: [{ ...band, rows, counts: totals, needsYouCount: totals.needs_you }],
		totals,
		cardById: new Map([...model.cardById].filter(([, at]) => kept(at))),
		topology: model.topology.filter(kept),
	};
}

export type DecisionScopeStream = {
	key: string;
	/** Undefined for the project's own row. */
	name?: string;
	/** Set only when another stream in the project has the same name. */
	machine?: string;
	projectWide: boolean;
	needsYou: number;
	/** Calls not yet done: needs you, decided for you and in progress. */
	open: number;
};

export type DecisionScopeProject = {
	key: string;
	/** Undefined only for calls that recorded no project. */
	name?: string;
	needsYou: number;
	open: number;
	streams: DecisionScopeStream[];
};

export type DecisionScopeTree = { needsYou: number; open: number; projects: DecisionScopeProject[] };

/** Every project and stream on the board, in board order, with the counts the list shows beside them. */
export function decisionScopeTree(model: DecisionBoardModel): DecisionScopeTree {
	return {
		needsYou: model.totals.needs_you,
		open: openOf(model.totals),
		projects: model.bands.map((band) => ({
			key: band.key,
			name: band.name,
			needsYou: band.counts.needs_you,
			open: openOf(band.counts),
			streams: band.rows.map((row) => ({
				key: row.key,
				name: row.name,
				machine: row.machine,
				projectWide: row.projectWide,
				needsYou: row.counts.needs_you,
				open: openOf(row.counts),
			})),
		})),
	};
}

/** Which projects in the list have their streams folded away; keyed by band key. */
export const DECISION_LIST_FOLD_STORAGE_KEY = "ao.decisions.list-fold-state.v1";
/** Set while the person has hidden the list. */
export const DECISION_LIST_HIDDEN_STORAGE_KEY = "ao.decisions.list-hidden.v1";

export function readDecisionListHidden(): boolean {
	try {
		return window.localStorage.getItem(DECISION_LIST_HIDDEN_STORAGE_KEY) === "1";
	} catch {
		return false;
	}
}

export function writeDecisionListHidden(hidden: boolean): void {
	try {
		if (hidden) window.localStorage.setItem(DECISION_LIST_HIDDEN_STORAGE_KEY, "1");
		else window.localStorage.removeItem(DECISION_LIST_HIDDEN_STORAGE_KEY);
	} catch {
		// The list still hides for this visit.
	}
}
