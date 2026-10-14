function finite(value: number | null | undefined): number | null {
	return typeof value === "number" && Number.isFinite(value) ? value : null;
}

/**
 * Processed tokens for one usage scope, or null when the daemon did not state them.
 *
 * The generated type promises `number | null`, but that is only true of a daemon
 * built after the field existed. An older one omits `processedTokens` from the
 * payload entirely, so the value arrives as `undefined`. Federation makes that
 * routine rather than exotic: the app talks to many daemons at once and any of
 * them may predate the field.
 *
 * `undefined` is the dangerous half, because it slips through the obvious guards
 * -- `undefined === null` is false and `undefined <= 0` is false -- and reaches
 * code that assumed a number. Every reader goes through here so there is one
 * place that decides what "unknown" looks like.
 *
 * `totalTokens` is the documented deprecated alias, and a current daemon derives
 * it from `processedTokens` (writing 0 whenever that is null). So it never
 * carries a figure the daemon meant to withhold, and on a daemon that predates
 * `processedTokens` it is the only count on the wire -- which is why an absent
 * field falls back to it rather than blanking the reading. Scopes that carry no
 * alias, such as the inspector's totals, simply read as unknown.
 */
export function processedTokensOf(
	scope: { processedTokens?: number | null; totalTokens?: number | null } | null | undefined,
): number | null {
	const processed = finite(scope?.processedTokens);
	if (processed !== null) return processed;
	const alias = finite(scope?.totalTokens);
	return alias !== null && alias > 0 ? alias : null;
}
