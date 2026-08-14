import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientFor } from "../lib/host-clients";
import { refKey, type Ref } from "../lib/hosts";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";

type GeneratedAgentSwitch = components["schemas"]["AgentSwitch"];

// Keep forward compatibility with newer daemons so unknown errors can fall
// back to a generic label instead of becoming impossible to represent.
export type AgentSwitch = Omit<GeneratedAgentSwitch, "errorCode"> & {
	errorCode?: string;
};

const terminalAgentSwitchStates = new Set<AgentSwitch["state"]>(["completed", "failed"]);

export const agentSwitchesQueryKey = (session?: Ref) =>
	session ? (["session-agent-switches", refKey(session)] as const) : (["session-agent-switches"] as const);

export function isTerminalAgentSwitch(agentSwitch: AgentSwitch): boolean {
	return terminalAgentSwitchStates.has(agentSwitch.state);
}

export function agentSwitchNeedsRecovery(agentSwitch: AgentSwitch): boolean {
	return agentSwitch.state === "starting_target" && agentSwitch.errorCode === "target_start_unconfirmed";
}

export function findActiveAgentSwitch(agentSwitches: AgentSwitch[]): AgentSwitch | undefined {
	return agentSwitches.find(
		(agentSwitch) => !isTerminalAgentSwitch(agentSwitch) && !agentSwitchNeedsRecovery(agentSwitch),
	);
}

export function findRecoveryRequiredAgentSwitch(agentSwitches: AgentSwitch[]): AgentSwitch | undefined {
	return agentSwitches.find(agentSwitchNeedsRecovery);
}

export function agentSwitchesRefetchInterval(agentSwitches: AgentSwitch[]): 1_000 | false {
	return findActiveAgentSwitch(agentSwitches) ? 1_000 : false;
}

async function fetchAgentSwitches(session: Ref): Promise<AgentSwitch[]> {
	const { data, error } = await clientFor(session.host).GET("/api/v1/sessions/{sessionId}/agent-switches", {
		params: { path: { sessionId: session.id } },
	});
	if (error) {
		throw new Error(apiErrorMessage(error, "Unable to load agent switch status"));
	}
	return data?.switches ?? [];
}

export function useAgentSwitches(session: Ref | undefined) {
	return useQuery({
		queryKey: agentSwitchesQueryKey(session),
		enabled: Boolean(session?.id),
		queryFn: () => (usesPreviewWorkspaceData ? Promise.resolve([]) : fetchAgentSwitches(session!)),
		// Once a durable saga is active, keep its phase fresh even if the CDC
		// connection is temporarily unavailable. Recovery-required records are
		// intentionally static until an external recovery changes them.
		refetchInterval: (query) =>
			agentSwitchesRefetchInterval((query.state.data as AgentSwitch[] | undefined) ?? []),
		retry: 1,
	});
}
