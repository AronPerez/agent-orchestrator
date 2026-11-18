import type { WorkspaceSession } from "../types/workspace";
import { buildFleetTree, flattenFleetSessions, fleetSessionKey, type FleetSessionAddress } from "@aoagents/product-ui";
import { LOCAL_HOST } from "./hosts";

export type FleetRow = {
	session: WorkspaceSession;
	depth: number;
	childCount: number;
	role: "Orchestrator" | "Manager" | "Worker";
	parentUnavailable: boolean;
	parentAddress?: FleetSessionAddress;
};

/** Spawn lineage only: names, prompts and report recipients never establish a parent. */
export function fleetRows(
	sessions: WorkspaceSession[],
	collapsed: ReadonlySet<string>,
	showArchived = false,
	localHostId?: string,
): FleetRow[] {
	if (!sessions.length) return [];
	const projectId = sessions[0]!.workspaceId;
	const machineId = sessions[0]!.hostId ?? localHostId ?? LOCAL_HOST;
	const machine = buildFleetTree([{
		project: { id: projectId, name: sessions[0]!.workspaceName },
		machine: { id: machineId, name: machineId, reachable: true },
		sessions,
	}], { showArchived })[0]?.machines[0];
	if (!machine) return [];
	const qualifiedCollapsed = new Set([...collapsed].map((id) => fleetSessionKey(projectId, machineId, id)));
	return flattenFleetSessions(machine.sessions, qualifiedCollapsed).map(({ node, depth }) => ({
		session: node.session,
		depth,
		childCount: node.children.length,
		role: node.role === "orchestrator" ? "Orchestrator" : node.role === "manager" ? "Manager" : "Worker",
		parentUnavailable: node.parentUnavailable,
		parentAddress: node.parentAddress,
	}));
}
