import { limitFleetCollapsePreferences, parseFleetCollapsePreferences, rememberFleetCollapsePreference } from "@aoagents/product-ui";
import { DECISION_LANES, type DecisionBoardModel, type DecisionLane, type DecisionTopologyRow } from "./decision-board";

/**
 * Which projects and streams on the Decisions board are folded. A person's
 * explicit choice is saved; a group without one is folded only while nothing
 * in it is open, so finished work stays out of the way without hiding a call
 * that needs an answer.
 */
export type DecisionFoldPreferences = Readonly<Record<string, boolean>>;

export const DECISION_FOLD_STORAGE_KEY = "ao.decisions.fold-state.v1";

/** A stream's Done cell shows this many of its newest cards until it is expanded. */
export const DECISION_DONE_SHOWN = 3;

// JSON keys keep arbitrary project and stream ids unambiguous.
export const decisionBandFoldKey = (bandKey: string): string => JSON.stringify(["project", bandKey]);
export const decisionRowFoldKey = (bandKey: string, rowKey: string): string => JSON.stringify(["stream", bandKey, rowKey]);

const OPEN_LANES: readonly DecisionLane[] = DECISION_LANES.filter((lane) => lane !== "done");
const hasOpen = (counts: Record<DecisionLane, number>) => OPEN_LANES.some((lane) => counts[lane] > 0);

/** The fold keys of the project and the stream that hold a call, or none when it is not on the board. */
function groupKeysOf(model: DecisionBoardModel, callId: string | undefined): string[] {
	const at = callId ? model.cardById.get(callId) : undefined;
	return at ? [decisionBandFoldKey(at.bandKey), decisionRowFoldKey(at.bandKey, at.rowKey)] : [];
}

/**
 * The keys folded right now: the saved choice where there is one, else folded
 * when nothing is open. The selected call's project and stream never fold by
 * default, so a call that finishes while its panel is open stays on the board
 * in the same render; a fold the person made still wins.
 */
export function resolveDecisionFolds(model: DecisionBoardModel, preferences: DecisionFoldPreferences, selectedId?: string): ReadonlySet<string> {
	const held = new Set(groupKeysOf(model, selectedId));
	const folded = new Set<string>();
	const settle = (key: string, open: boolean) => {
		if (preferences[key] ?? !(open || held.has(key))) folded.add(key);
	};
	for (const band of model.bands) {
		settle(decisionBandFoldKey(band.key), hasOpen(band.counts));
		for (const row of band.rows) settle(decisionRowFoldKey(band.key, row.key), hasOpen(row.counts));
	}
	return folded;
}

/** Remember one choice as the newest entry. */
export function rememberDecisionFold(preferences: DecisionFoldPreferences, key: string, folded: boolean): DecisionFoldPreferences {
	return rememberFleetCollapsePreference(preferences, key, folded);
}

/** Fold or open every project and stream on the board. */
export function foldAllDecisions(model: DecisionBoardModel, preferences: DecisionFoldPreferences, folded: boolean): DecisionFoldPreferences {
	let next = preferences;
	for (const band of model.bands) {
		next = rememberDecisionFold(next, decisionBandFoldKey(band.key), folded);
		for (const row of band.rows) next = rememberDecisionFold(next, decisionRowFoldKey(band.key, row.key), folded);
	}
	return next;
}

/**
 * Open the project and stream that hold a call, so opening it always shows it.
 * Returns the same object when the call is already on screen or not on the board.
 */
export function revealDecisionCard(model: DecisionBoardModel, preferences: DecisionFoldPreferences, callId: string): DecisionFoldPreferences {
	const folded = resolveDecisionFolds(model, preferences);
	let next = preferences;
	for (const key of groupKeysOf(model, callId)) {
		if (folded.has(key)) next = rememberDecisionFold(next, key, false);
	}
	return next;
}

/**
 * Save as open the project and stream of a call that would otherwise fold by
 * default, so they stay open once the call is no longer selected. A fold the
 * person made is left alone. Returns the same object when there is nothing to save.
 */
export function keepDecisionCardOpen(model: DecisionBoardModel, preferences: DecisionFoldPreferences, callId: string): DecisionFoldPreferences {
	const folded = resolveDecisionFolds(model, preferences);
	let next = preferences;
	for (const key of groupKeysOf(model, callId)) {
		if (preferences[key] === undefined && folded.has(key)) next = rememberDecisionFold(next, key, false);
	}
	return next;
}

export function readDecisionFolds(): DecisionFoldPreferences {
	try {
		return parseFleetCollapsePreferences(window.localStorage.getItem(DECISION_FOLD_STORAGE_KEY));
	} catch {
		return {};
	}
}

export function writeDecisionFolds(preferences: DecisionFoldPreferences): void {
	try {
		window.localStorage.setItem(DECISION_FOLD_STORAGE_KEY, JSON.stringify(limitFleetCollapsePreferences(preferences)));
	} catch {
		// A full or unavailable store must never make the board unusable.
	}
}

/**
 * The cards of one Done cell that are drawn while it is cut back: its newest
 * few, and the selected card wherever it sits, so the selection is never
 * hidden behind the cap. Works on cards or on their ids.
 */
export function cappedDoneCards<Card>(cards: readonly Card[], idOf: (card: Card) => string, selectedId: string | undefined): Card[] {
	return cards.filter((card, index) => index < DECISION_DONE_SHOWN || idOf(card) === selectedId);
}

/**
 * The cards a person can reach right now, for arrow-key movement: folded
 * projects and streams are left out, and a Done cell is cut back as it is
 * drawn unless its row key is in `expandedDone`.
 */
export function visibleDecisionTopology(
	model: DecisionBoardModel,
	folded: ReadonlySet<string>,
	expandedDone: ReadonlySet<string>,
	selectedId?: string,
): DecisionTopologyRow[] {
	return model.topology
		.filter((row) => !folded.has(decisionBandFoldKey(row.bandKey)) && !folded.has(decisionRowFoldKey(row.bandKey, row.rowKey)))
		.map((row) => {
			if (row.lanes.done.length <= DECISION_DONE_SHOWN || expandedDone.has(decisionRowFoldKey(row.bandKey, row.rowKey))) return row;
			return { ...row, lanes: { ...row.lanes, done: cappedDoneCards(row.lanes.done, (id) => id, selectedId) } };
		});
}
