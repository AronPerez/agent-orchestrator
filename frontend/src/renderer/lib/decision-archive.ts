import type { DecisionBoardModel, DecisionCardModel } from "./decision-board";

/**
 * One done decision in the archive the board's "N more" opens: the card as the
 * board draws it, and where it belongs.
 */
export type DecisionArchiveEntry = {
	card: DecisionCardModel;
	projectKey: string;
	/** Undefined only for calls that recorded no project. */
	projectName?: string;
	streamKey: string;
	/** Undefined for the project's own row. */
	streamName?: string;
	/** Set only when another stream in the project has the same name. */
	streamMachine?: string;
	projectWide: boolean;
};

/** The stream the archive was opened from. */
export type DecisionArchiveScope = { project: string; stream: string };

const finishedAt = (card: DecisionCardModel) => {
	const at = Date.parse(card.sortAt);
	return Number.isFinite(at) ? at : 0;
};

/** Every done decision on the board, newest first across all projects and streams. */
export function decisionArchiveEntries(model: DecisionBoardModel): DecisionArchiveEntry[] {
	const entries: DecisionArchiveEntry[] = [];
	for (const band of model.bands) {
		for (const row of band.rows) {
			for (const card of row.lanes.done) {
				entries.push({
					card,
					projectKey: band.key,
					projectName: band.name,
					streamKey: row.key,
					streamName: row.name,
					streamMachine: row.machine,
					projectWide: row.projectWide,
				});
			}
		}
	}
	return entries.sort((a, b) => finishedAt(b.card) - finishedAt(a.card) || (a.card.id < b.card.id ? -1 : a.card.id > b.card.id ? 1 : 0));
}

/**
 * The entries of one stream, when a scope is given, that hold every word of
 * the search. `textOf` supplies what a person can read on the card, in their
 * language, so the search matches what they see.
 */
export function filterDecisionArchive(
	entries: readonly DecisionArchiveEntry[],
	scope: DecisionArchiveScope | undefined,
	query: string,
	textOf: (entry: DecisionArchiveEntry) => string,
): DecisionArchiveEntry[] {
	const words = query.toLocaleLowerCase().split(/\s+/).filter(Boolean);
	return entries.filter((entry) => {
		if (scope && (entry.projectKey !== scope.project || entry.streamKey !== scope.stream)) return false;
		if (words.length === 0) return true;
		const text = textOf(entry).toLocaleLowerCase();
		return words.every((word) => text.includes(word));
	});
}
