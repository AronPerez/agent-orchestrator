import {
	buildFleetTree,
	type FleetProjectNode,
	type FleetTreeSession,
} from "../../product-ui/src/fleet-tree";
import { toSessionStatus, type SessionStatus } from "../../product-ui/src/session-models";
import { attentionZone, type AttentionZone } from "../../product-ui/src/session-presentation";
import type { DashboardPR, DashboardSession, OrchestratorLink } from "./api";
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
const rollupOrder: AttentionZone[] = ["action", "merge", "pending", "working", "done"];

function pullRequestStatus(pr: DashboardPR): SessionStatus {
	if (pr.ciStatus === "failing") return "ci_failed";
	if (pr.isDraft) return "draft";
	if (pr.reviewDecision === "changes_requested") return "changes_requested";
	if (pr.unresolvedThreads) return "commented";
	if (pr.mergeability?.mergeable) return "mergeable";
	if (pr.reviewDecision === "pending") return "review_pending";
	if (pr.mergeability?.state === "blocked") return "pr_open";
	if (pr.reviewDecision === "approved") return "approved";
	return "pr_open";
}

function sessionRollupStatus(session: DashboardSession, liveStatus: SessionStatus): SessionStatus {
	const normalizedLive = liveStatus === "merged" && !session.isTerminated ? "idle" : liveStatus;
	const pullRequests = session.prs?.length ? session.prs : session.pr ? [session.pr] : [];
	const statuses = new Set(
		pullRequests
			.filter((pr) => pr.state !== "merged" && pr.state !== "closed")
			.map(pullRequestStatus),
	);
	const deliveryStatus = ([
		"ci_failed",
		"changes_requested",
		"commented",
		"draft",
		"review_pending",
		"pr_open",
		"approved",
		"mergeable",
	] as const).find((status) => statuses.has(status));
	if (!deliveryStatus) return normalizedLive;
	return rollupOrder.indexOf(attentionZone(normalizedLive)) <= rollupOrder.indexOf(attentionZone(deliveryStatus))
		? normalizedLive
		: deliveryStatus;
}

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
		const terminated = item.isTerminal === true;
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
		const status = toSessionStatus(item.status ?? undefined, item.isTerminated);
		add(item.hostId, item.projectId, {
			id: item.id,
			projectId: item.projectId,
			hostId: item.hostId,
			title: sessionTitle(item),
			parentSessionId: item.parentSessionId,
			kind: item.kind === "orchestrator" ? "orchestrator" : "worker",
			status,
			rollupStatus: sessionRollupStatus(item, status),
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
