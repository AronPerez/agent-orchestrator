import {
	buildFleetTree,
	type FleetProjectNode,
	type FleetTreeSession,
} from "../../product-ui/src/fleet-tree";
import { toSessionStatus } from "../../product-ui/src/session-models";
import type { DashboardSession, OrchestratorLink } from "./api";
import type { HostedOrchestrator, HostedProject, HostedSession } from "./hostedRows";
import { sessionTitle } from "./sessionStatus";

export type MobileFleetHost = {
	hostId: string;
	name: string;
	connection: "closed" | "connecting" | "open";
};

export type MobileFleetSession = FleetTreeSession & {
	projectId: string;
	hostId: string;
	title: string;
	source: DashboardSession | OrchestratorLink;
};

type MobileFleetInput = {
	projects: readonly HostedProject[];
	sessions: readonly HostedSession[];
	orchestrators: readonly HostedOrchestrator[];
	hosts: readonly MobileFleetHost[];
};

const scopeKey = (hostId: string, projectId: string) => JSON.stringify([hostId, projectId]);

/** Adapt the phone's host-qualified cache to the shared project → machine tree. */
export function buildMobileFleetTree({
	projects,
	sessions,
	orchestrators,
	hosts,
}: MobileFleetInput): FleetProjectNode<MobileFleetSession>[] {
	const projectsByScope = new Map(projects.map((project) => [scopeKey(project.hostId, project.id), project]));
	const hostsById = new Map(hosts.map((host) => [host.hostId, host]));
	const grouped = new Map<string, MobileFleetSession[]>();

	const add = (hostId: string, projectId: string, item: MobileFleetSession) => {
		const key = scopeKey(hostId, projectId);
		const current = grouped.get(key) ?? [];
		current.push(item);
		grouped.set(key, current);
	};
	for (const item of orchestrators) {
		const terminated = item.isTerminal === true || item.hasRuntime === false;
		add(item.hostId, item.projectId, {
			id: item.id,
			projectId: item.projectId,
			hostId: item.hostId,
			title: "Orchestrator",
			kind: "orchestrator",
			status: toSessionStatus(item.status ?? undefined, terminated),
			isTerminated: terminated,
			source: item,
		});
	}
	for (const item of sessions) {
		add(item.hostId, item.projectId, {
			id: item.id,
			projectId: item.projectId,
			hostId: item.hostId,
			title: sessionTitle(item),
			parentSessionId: item.parentSessionId,
			kind: item.kind === "orchestrator" ? "orchestrator" : "worker",
			status: toSessionStatus(item.status ?? undefined, item.isTerminated),
			isTerminated: item.isTerminated,
			source: item,
		});
	}

	return buildFleetTree([...grouped.entries()].map(([key, rows]) => {
		const first = rows[0]!;
		const project = projectsByScope.get(key);
		const host = hostsById.get(first.hostId);
		return {
			project: {
				id: first.projectId,
				name: project?.name ?? (first.projectId || "Standalone"),
			},
			machine: {
				id: first.hostId,
				name: project?.hostName ?? host?.name ?? first.hostId,
				reachable: host ? host.connection === "open" : true,
			},
			sessions: rows,
		};
	}));
}
