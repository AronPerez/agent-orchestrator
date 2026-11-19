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

export type DecisionInboxAnswer = {
	sourceHostId: HostId;
	callId: string;
	option: number;
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
		body: { choice: "option", option: input.option, ...(input.note ? { note: input.note } : {}) },
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
