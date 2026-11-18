import type { components } from "../../api/schema";
import { apiErrorException } from "./api-error-exception";
import { clientForSessionHost } from "./host-clients";
import { LOCAL_HOST, type HostId } from "./hosts";

export type DecisionCall = components["schemas"]["DecisionCallResponse"];

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
		body: { choice: "option", option: input.option, ifState: "needs_you" },
	});
	if (error || !data) throw apiErrorException(error, "Could not send decision");
	return data;
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
