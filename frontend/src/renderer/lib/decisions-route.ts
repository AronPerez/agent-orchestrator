/** Search params of the Decisions page: `/decisions?call=<id>&q=<words>`. */
export type DecisionsSearch = { call?: string; q?: string };

export const DECISIONS_CALL_MAX = 200;
export const DECISIONS_QUERY_MAX = 500;

// A call id is one opaque path-safe segment, e.g. dec_6f1c…; nothing that could
// change the detail request's path or carry its own query.
const CALL_ID = /^[A-Za-z0-9_-]+$/;

/** True for a value that can name a decision call in a route or link. */
export function isDecisionCallId(value: string): boolean {
	return value.length > 0 && value.length <= DECISIONS_CALL_MAX && CALL_ID.test(value);
}

function boundedText(value: unknown, max: number): string | undefined {
	if (typeof value === "number" && Number.isFinite(value)) value = String(value);
	if (typeof value !== "string") return undefined;
	const text = value.trim();
	return text && text.length <= max ? text : undefined;
}

/**
 * The router hands search params over already decoded (numbers and booleans
 * parsed). Keep only bounded strings; anything else is dropped rather than
 * reported, since a stale or hand-edited link must still open the board.
 */
export function validateDecisionsSearch(search: Record<string, unknown>): DecisionsSearch {
	const result: DecisionsSearch = {};
	const call = typeof search.call === "string" ? boundedText(search.call, DECISIONS_CALL_MAX) : undefined;
	if (call && isDecisionCallId(call)) result.call = call;
	const q = boundedText(search.q, DECISIONS_QUERY_MAX);
	if (q) result.q = q;
	return result;
}

/** The sidebar badge text: hidden at zero, capped at 99+. */
export function formatDecisionCount(count: number): string | undefined {
	if (!Number.isFinite(count) || count <= 0) return undefined;
	return count > 99 ? "99+" : String(Math.floor(count));
}
