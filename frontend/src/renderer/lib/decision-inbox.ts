import type { components } from "../../api/schema";
import { apiErrorException } from "./api-error-exception";
import { clientForSessionHost } from "./host-clients";
import { LOCAL_HOST, type HostId } from "./hosts";

export type DecisionCall = components["schemas"]["DecisionCallResponse"];
export type DecisionBoard = components["schemas"]["DecisionBoardResponse"];
export type DecisionDetail = components["schemas"]["DecisionDetailResponse"];
export type DecisionHistoryEntry = components["schemas"]["DecisionEntryResponse"];

/** Results per desktop search page; the daemon caps any request at 100. */
export const DECISION_SEARCH_LIMIT = 50;

/**
 * One cache root for every decision surface, partitioned by the daemon that
 * served it. The bell's needs-you list is the host branch itself; the
 * Decisions page reads this computer's ledger beneath the local branch, so a
 * single host invalidation refreshes everything that host showed.
 */
export const decisionKeys = {
	all: ["decision-inbox"] as const,
	host: (hostId: HostId) => [...decisionKeys.all, hostId] as const,
	needsYou: (hostId: HostId) => decisionKeys.host(hostId),
	board: () => [...decisionKeys.host(LOCAL_HOST), "board"] as const,
	detail: (callId: string) => [...decisionKeys.host(LOCAL_HOST), "detail", callId] as const,
	search: (query: string, limit: number) => [...decisionKeys.host(LOCAL_HOST), "search", query, limit] as const,
};

export type DecisionInboxSource = {
	hostId: HostId;
	label: string;
	calls: DecisionCall[];
};

export type DecisionInboxEntry = {
	sourceHostId: HostId;
	sourceHostLabel: string;
	call: DecisionCall;
};

/**
 * One answer to a call. The bell sends an offered option; the page can also
 * send "Something else" (`choice: "other"`) and an optional note.
 */
export type DecisionInboxAnswer = {
	sourceHostId: HostId;
	callId: string;
	choice?: "option" | "other";
	option?: number;
	note?: string;
};

export async function fetchDecisionInboxCalls(hostId: HostId = LOCAL_HOST, signal?: AbortSignal): Promise<DecisionCall[]> {
	const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/decisions", {
		params: { query: { scope: "needs_you" } },
		signal,
	});
	if (error || !data) throw apiErrorException(error, "Could not load decisions");
	return data.calls;
}

export async function answerDecisionInboxCall(input: DecisionInboxAnswer): Promise<DecisionCall> {
	const { data, error } = await clientForSessionHost(input.sourceHostId).POST("/api/v1/decisions/{id}/answer", {
		params: { path: { id: input.callId } },
		body: answerBody(input),
	});
	if (error || !data) throw apiErrorException(error, "Could not send decision");
	return data;
}

export async function fetchDecisionBoard(signal?: AbortSignal): Promise<DecisionBoard> {
	const { data, error } = await clientForSessionHost(LOCAL_HOST).GET("/api/v1/decisions/board", { signal });
	if (error || !data) throw apiErrorException(error, "Could not load decisions");
	return data;
}

/** A call that no longer exists resolves to null so the panel can say so. */
export async function fetchDecisionDetail(callId: string, signal?: AbortSignal): Promise<DecisionDetail | null> {
	const { data, error, response } = await clientForSessionHost(LOCAL_HOST).GET("/api/v1/decisions/{id}", {
		params: { path: { id: callId } },
		signal,
	});
	if (response?.status === 404) return null;
	if (error || !data) throw apiErrorException(error, "Could not load this decision");
	return data;
}

export async function fetchDecisionSearch(query: string, limit: number, signal?: AbortSignal): Promise<DecisionCall[]> {
	const { data, error } = await clientForSessionHost(LOCAL_HOST).GET("/api/v1/decisions", {
		params: { query: { q: query, limit } },
		signal,
	});
	if (error || !data) throw apiErrorException(error, "Could not search decisions");
	return data.calls;
}

/**
 * Plan-first's "Approve with changes" is only actionable with the changes
 * written down; the gate stays pending without them. Every surface that
 * answers applies this one rule.
 */
export function decisionOptionRequiresNote(call: Pick<DecisionCall, "reference" | "options">, optionNumber: number): boolean {
	return call.reference === "plan-first" && optionNumber === 2 && call.options[1]?.label === "Approve with changes";
}

function answerBody(input: DecisionInboxAnswer) {
	const note = input.note?.trim();
	const choice = input.choice ?? "option";
	return {
		choice,
		...(choice === "option" ? { option: input.option } : {}),
		...(note ? { note } : {}),
	};
}

/** A ledger action other than answering, on this computer's ledger. */
export type DecisionAction =
	| { kind: "dismiss"; callId: string; note?: string }
	| { kind: "outcome"; callId: string; forEntry?: string; text: string; link?: string }
	| { kind: "retire"; callId: string; retired: boolean };

export async function dismissDecisionCall(callId: string, note?: string): Promise<DecisionCall> {
	const trimmed = note?.trim();
	const { data, error } = await clientForSessionHost(LOCAL_HOST).POST("/api/v1/decisions/{id}/dismiss", {
		params: { path: { id: callId } },
		body: trimmed ? { note: trimmed } : {},
	});
	if (error || !data) throw apiErrorException(error, "Could not dismiss this decision");
	return data;
}

export async function recordDecisionOutcome(callId: string, outcome: { forEntry?: string; text: string; link?: string }): Promise<DecisionCall> {
	const link = outcome.link?.trim();
	const { data, error } = await clientForSessionHost(LOCAL_HOST).POST("/api/v1/decisions/{id}/outcome", {
		params: { path: { id: callId } },
		body: {
			...(outcome.forEntry ? { forEntry: outcome.forEntry } : {}),
			text: outcome.text.trim(),
			...(link ? { link } : {}),
		},
	});
	if (error || !data) throw apiErrorException(error, "Could not record the outcome");
	return data;
}

export async function retireDecisionCall(callId: string, retired: boolean): Promise<DecisionCall> {
	const { data, error } = await clientForSessionHost(LOCAL_HOST).POST("/api/v1/decisions/{id}/retire", {
		params: { path: { id: callId } },
		body: { retired },
	});
	if (error || !data) throw apiErrorException(error, "Could not update this decision");
	return data;
}

export function runDecisionAction(action: DecisionAction): Promise<DecisionCall> {
	switch (action.kind) {
		case "dismiss": return dismissDecisionCall(action.callId, action.note);
		case "outcome": return recordDecisionOutcome(action.callId, action);
		case "retire": return retireDecisionCall(action.callId, action.retired);
	}
}

/** Prefer the local copy once ledger replication makes the same call visible on several hosts. */
export function mergeDecisionInboxSources(sources: DecisionInboxSource[]): DecisionInboxEntry[] {
	const seen = new Set<string>();
	return sources
		.flatMap((source) => source.calls.map((call) => ({
			sourceHostId: source.hostId,
			sourceHostLabel: source.label,
			call,
		})))
		.filter(({ call }) => {
			if (seen.has(call.id)) return false;
			seen.add(call.id);
			return true;
		})
		.sort((a, b) => a.call.raisedAt.localeCompare(b.call.raisedAt));
}
