import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { DecisionBoardModel } from "../lib/decision-board";
import {
	foldAllDecisions,
	keepDecisionCardOpen,
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
	const latestSelected = useRef(selectedId);
	latestSelected.current = selectedId;

	// Save choices, but not the ones just read on mount.
	const loaded = useRef(preferences);
	useEffect(() => {
		if (preferences !== loaded.current) writeDecisionFolds(preferences);
	}, [preferences]);

	// Selecting a call opens the project and stream that hold it, even ones the
	// person folded. This follows the selection and the call's place on the
	// board, not the fold state, so folding its group afterwards stays the
	// person's choice. Before paint, so the group never flashes folded.
	const at = selectedId ? model.cardById.get(selectedId) : undefined;
	const place = at ? `${at.bandKey}\n${at.rowKey}` : undefined;
	useLayoutEffect(() => {
		if (!selectedId || place === undefined) return;
		setPreferences((current) => revealDecisionCard(latestModel.current, current, selectedId));
	}, [selectedId, place]);

	// The selected call's group can be left with nothing open: the call itself
	// finishes, or the last other open call beside it does. resolveDecisionFolds
	// already keeps that group open in the same render, so the card and its
	// focus are never dropped. This saves it as open whenever that hold is all
	// that keeps it there, so it stays once the panel closes. Unlike a new
	// selection, it never undoes a fold the person made.
	const heldOpen = selectedId ? keepDecisionCardOpen(model, preferences, selectedId) !== preferences : false;
	useLayoutEffect(() => {
		if (!heldOpen || !selectedId) return;
		setPreferences((current) => keepDecisionCardOpen(latestModel.current, current, selectedId));
	}, [heldOpen, selectedId]);

	const folded = useMemo(() => resolveDecisionFolds(model, preferences, selectedId), [model, preferences, selectedId]);
	const toggle = useCallback((key: string) => {
		setPreferences((current) => rememberDecisionFold(current, key, !resolveDecisionFolds(latestModel.current, current, latestSelected.current).has(key)));
	}, []);
	const setAll = useCallback((value: boolean) => {
		setPreferences((current) => foldAllDecisions(latestModel.current, current, value));
	}, []);
	return useMemo(() => ({ folded, toggle, setAll }), [folded, toggle, setAll]);
}
