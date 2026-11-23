import { isDecisionCallId } from "./decisions-route";

/**
 * The decisions a person keeps open as tabs on the Decisions page. They are
 * saved on this computer rather than in the URL, so a link, the bell or coming
 * back to the page does not drop them; the URL says only which tab is showing.
 */
export const DECISION_TABS_STORAGE_KEY = "ao.decisions.open-tabs.v1";

/** More tabs than this stop being readable in the strip. */
export const DECISION_TABS_MAX = 12;

/** Keep one more decision open. Returns the same list when it is already kept, the strip is full, or the id is not a call id. */
export function keepDecisionTab(ids: readonly string[], id: string): readonly string[] {
	if (ids.includes(id) || ids.length >= DECISION_TABS_MAX || !isDecisionCallId(id)) return ids;
	return [...ids, id];
}

/** Close one tab. Returns the same list when it was not kept. */
export function closeDecisionTab(ids: readonly string[], id: string): readonly string[] {
	return ids.includes(id) ? ids.filter((item) => item !== id) : ids;
}

export function readDecisionTabs(): readonly string[] {
	try {
		const parsed: unknown = JSON.parse(window.localStorage.getItem(DECISION_TABS_STORAGE_KEY) ?? "[]");
		if (!Array.isArray(parsed)) return [];
		const ids = parsed.filter((item): item is string => typeof item === "string" && isDecisionCallId(item));
		return [...new Set(ids)].slice(0, DECISION_TABS_MAX);
	} catch {
		return [];
	}
}

export function writeDecisionTabs(ids: readonly string[]): void {
	try {
		if (ids.length === 0) window.localStorage.removeItem(DECISION_TABS_STORAGE_KEY);
		else window.localStorage.setItem(DECISION_TABS_STORAGE_KEY, JSON.stringify(ids));
	} catch {
		// The tabs still hold for this visit.
	}
}
