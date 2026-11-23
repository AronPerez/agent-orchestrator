import { createContext, useCallback, useContext, useEffect, useId, useRef, type ReactNode } from "react";

type ReportDraft = (formId: string, dirty: boolean) => void;

const DecisionDraftContext = createContext<ReportDraft | undefined>(undefined);

/**
 * Gathers whether any form of one decision holds text that has not been sent,
 * and tells its owner when that changes. The Decisions page uses it to ask
 * before closing a tab that would throw such text away.
 */
export function DecisionDraftScope({ onDraftChange, children }: { onDraftChange: (dirty: boolean) => void; children: ReactNode }) {
	const dirtyForms = useRef(new Set<string>());
	const latest = useRef(onDraftChange);
	latest.current = onDraftChange;
	const report = useCallback<ReportDraft>((formId, dirty) => {
		const before = dirtyForms.current.size > 0;
		if (dirty) dirtyForms.current.add(formId);
		else dirtyForms.current.delete(formId);
		const after = dirtyForms.current.size > 0;
		if (before !== after) latest.current(after);
	}, []);
	return <DecisionDraftContext.Provider value={report}>{children}</DecisionDraftContext.Provider>;
}

/** A form says whether it holds unsent text. Outside a scope this does nothing. */
export function useDecisionDraft(dirty: boolean): void {
	const report = useContext(DecisionDraftContext);
	const formId = useId();
	useEffect(() => {
		report?.(formId, dirty);
	}, [report, formId, dirty]);
	// A form that goes away, sent or not, no longer holds anything.
	useEffect(() => () => report?.(formId, false), [report, formId]);
}
