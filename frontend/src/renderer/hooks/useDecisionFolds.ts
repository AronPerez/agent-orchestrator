import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { DecisionBoardModel } from "../lib/decision-board";
import {
	foldAllDecisions,
	readDecisionFolds,
	rememberDecisionFold,
	resolveDecisionFolds,
	revealDecisionCard,
	writeDecisionFolds,
} from "../lib/decision-folds";

export type DecisionFolds = {
	/** Fold keys of the projects and streams folded right now. */
	folded: ReadonlySet<string>;
	toggle: (key: string) => void;
	setAll: (folded: boolean) => void;
};

/**
 * The Decisions board's fold state: a person's saved choices over the default
 * (a group folds only while nothing in it is open). Selecting a call opens the
 * project and stream that hold it.
 */
export function useDecisionFolds(model: DecisionBoardModel, selectedId?: string): DecisionFolds {
	const [preferences, setPreferences] = useState(readDecisionFolds);
	const latestModel = useRef(model);
	latestModel.current = model;

	// Save choices, but not the ones just read on mount.
	const loaded = useRef(preferences);
	useEffect(() => {
		if (preferences !== loaded.current) writeDecisionFolds(preferences);
	}, [preferences]);

	// The selected call is always shown. This follows the selection, the call's
	// place on the board and its state, not the fold state: a call that finishes
	// can leave its stream with nothing open, which folds by default, so the
	// state change reveals it again; folding the group afterwards stays the
	// person's choice. Before paint, so the group never flashes folded.
	const at = selectedId ? model.cardById.get(selectedId) : undefined;
	const place = at ? `${at.bandKey}\n${at.rowKey}\n${at.card.lane}` : undefined;
	useLayoutEffect(() => {
		if (!selectedId || place === undefined) return;
		setPreferences((current) => revealDecisionCard(latestModel.current, current, selectedId));
	}, [selectedId, place]);

	const folded = useMemo(() => resolveDecisionFolds(model, preferences), [model, preferences]);
	const toggle = useCallback((key: string) => {
		setPreferences((current) => rememberDecisionFold(current, key, !resolveDecisionFolds(latestModel.current, current).has(key)));
	}, []);
	const setAll = useCallback((value: boolean) => {
		setPreferences((current) => foldAllDecisions(latestModel.current, current, value));
	}, []);
	return useMemo(() => ({ folded, toggle, setAll }), [folded, toggle, setAll]);
}
