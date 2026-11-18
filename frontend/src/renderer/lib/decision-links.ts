import { isDecisionCallId } from "./decisions-route";

const DECISION_LINK_PREFIX = "ao://decisions/";

export type DecisionLinkTarget = { callId: string };

/**
 * Strictly parse the in-app decision route, `ao://decisions/{callId}`: one
 * percent-decoded id segment, with no query, fragment or further path.
 */
export function parseDecisionLink(value: string): DecisionLinkTarget | undefined {
	if (!value.startsWith(DECISION_LINK_PREFIX) || value.includes("?") || value.includes("#")) return undefined;
	const segment = value.slice(DECISION_LINK_PREFIX.length);
	if (!segment || segment.includes("/") || segment.includes("\\")) return undefined;
	let callId: string;
	try {
		callId = decodeURIComponent(segment);
	} catch {
		return undefined;
	}
	return isDecisionCallId(callId) ? { callId } : undefined;
}

/** The canonical in-app link for one call. */
export function decisionLink(callId: string): string {
	return `${DECISION_LINK_PREFIX}${encodeURIComponent(callId)}`;
}
