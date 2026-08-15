import { useQueries, type QueryFunctionContext, type UseQueryResult } from "@tanstack/react-query";
import { useSyncExternalStore } from "react";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientFor, connectedHosts, hostLabelFor, isHostReady, subscribeConnectedHosts } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { mockWorkspaces } from "../lib/mock-data";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { parseResponseArray } from "../lib/response-validation";
import { toReviewerHarnessId } from "../lib/reviewer-harnesses";
import { captureRendererEvent } from "../lib/telemetry";
import {
	type AgentSwitchSummary,
	type PRState,
	type PullRequestFacts,
	type HostSection,
	toAgentProvider,
	toProjectKind,
	toSessionActivity,
	toSessionStatus,
	type WorkspaceSummary,
} from "../types/workspace";

export type { HostSection } from "../types/workspace";

function toAgentSwitchSummary(
	agentSwitch: components["schemas"]["AgentSwitch"],
): AgentSwitchSummary {
	return {
		agentHandoffStatus: agentSwitch.agentHandoffStatus,
		errorCode: agentSwitch.errorCode,
		fromHarness: agentSwitch.fromHarness,
		id: agentSwitch.id,
		state: agentSwitch.state,
		targetHarness: agentSwitch.targetHarness,
	};
}

function toPullRequestFacts(pr: components["schemas"]["SessionPRFacts"]): PullRequestFacts {
	return {
		url: pr.url,
		number: pr.number,
		state: pr.state as PRState,
		ci: pr.ci,
		review: pr.review,
		mergeability: pr.mergeability,
		reviewComments: pr.reviewComments,
		updatedAt: pr.updatedAt,
	};
}

export const workspaceQueryKey = ["workspaces"] as const;
const reportedUnknownSessionFields = new Set<string>();

export function workspaceHostQueryKey(host: HostId) {
	return [...workspaceQueryKey, host] as const;
}

function reportUnknownSessionField(field: "status" | "activity", value?: string): void {
	const reason = value ? "unrecognized" : "missing";
	const key = `${field}:${reason}`;
	if (reportedUnknownSessionFields.has(key)) return;
	reportedUnknownSessionFields.add(key);
	void captureRendererEvent("ao.renderer.session_state_unknown", { field, reason });
}

// e2e seam (dev:web only): the Playwright fake-agent harness injects
// `window.__aoFakeAgent` (see e2e/support/fake-bridge.ts) to drive a
// deterministic, mutable session timeline off the SSE refetch path. Compiled
// out of the packaged build — the packaged renderer never sets VITE_NO_ELECTRON
// and always hits the real daemon.
type FakeAgentSeam = { snapshot: () => WorkspaceSummary[] };

type ProjectSummaryDTO = components["schemas"]["ProjectSummary"];
type SessionDTO = components["schemas"]["ControllersSessionView"];

function isProject(value: unknown): value is ProjectSummaryDTO {
	if (typeof value !== "object" || value === null) return false;
	const project = value as Partial<ProjectSummaryDTO>;
	return typeof project.id === "string" && typeof project.name === "string" && typeof project.path === "string";
}

function isSession(value: unknown): value is SessionDTO {
	if (typeof value !== "object" || value === null) return false;
	const session = value as Partial<SessionDTO>;
	return typeof session.id === "string" && typeof session.projectId === "string";
}

function tagWorkspaces(host: HostId, workspaces: WorkspaceSummary[]): WorkspaceSummary[] {
	return workspaces.map((workspace) => ({
		...workspace,
		host,
		sessions: workspace.sessions.map((session) => ({ ...session, host })),
	}));
}

async function fetchWorkspaces(host: HostId): Promise<WorkspaceSummary[]> {
	if (usesPreviewWorkspaceData && host === LOCAL_HOST) {
		const fake =
			typeof window !== "undefined"
				? (window as unknown as { __aoFakeAgent?: FakeAgentSeam }).__aoFakeAgent
				: undefined;
		return tagWorkspaces(host, fake ? fake.snapshot() : mockWorkspaces);
	}
	if (!isHostReady(host)) throw new Error(`host ${host} is not connected`);

	const client = clientFor(host);
	const [{ data: projectsData, error: projectsError }, { data: sessionsData, error: sessionsError }] =
		await Promise.all([client.GET("/api/v1/projects"), client.GET("/api/v1/sessions")]).catch((error: unknown) => {
			if (error instanceof SyntaxError) throw new Error("Host returned malformed workspace data");
			throw error;
		});

	if (projectsError || sessionsError) throw projectsError ?? sessionsError;
	const projects = parseResponseArray(projectsData, "projects", isProject);
	const sessions = parseResponseArray(sessionsData, "sessions", isSession);
	if (projects === null || sessions === null) throw new Error("Host returned malformed workspace data");

	return projects.map((project) => {
		const kind = toProjectKind(project.kind);
		return {
			host,
			id: project.id,
			name: project.name,
			kind,
			path: project.path,
			orchestratorAgent: project.orchestratorAgent ? toAgentProvider(project.orchestratorAgent) : undefined,
			sessions: sessions
				.filter((session) => session.projectId === project.id)
				.map((session) => {
					const status = toSessionStatus(session.status, session.isTerminated);
					const scmStatus = session.scmStatus ? toSessionStatus(session.scmStatus) : undefined;
					const activity = toSessionActivity(session.activity);
					if (status === "unknown") reportUnknownSessionField("status", session.status);
					if (!activity || activity.state === "unknown") {
						reportUnknownSessionField("activity", session.activity?.state);
					}
					return {
						host,
						id: session.id,
						terminalHandleId: session.terminalHandleId,
						workspaceId: project.id,
						workspaceName: project.name,
						title: session.displayName ?? session.issueId ?? session.id,
						issueId: session.issueId,
						provider: toAgentProvider(session.harness),
						reviewerHarness: toReviewerHarnessId(session.reviewerHarness),
						autoReviewEnabled: session.autoReviewEnabled ?? false,
						kind: session.kind === "orchestrator" ? "orchestrator" : session.kind === "worker" ? "worker" : undefined,
						// Carried through verbatim: the session surface must render from
						// the mode this session was created with, not from whatever the
						// current default happens to be.
						mode: session.mode === "chat" ? "chat" : "tui",
						branch: session.branch || undefined,
						status,
						scmStatus,
						isTerminated: session.isTerminated,
						terminateOnPrMerge: session.terminateOnPrMerge ?? false,
						autoInjectReview: session.autoInjectReview ?? true,
						autoInjectCI: session.autoInjectCI ?? true,
						createdAt: session.createdAt,
						updatedAt: session.updatedAt,
						activity,
						activeAgentSwitch: session.activeAgentSwitch
							? toAgentSwitchSummary(session.activeAgentSwitch)
							: undefined,
						previewUrl: session.previewUrl,
						previewRevision: session.previewRevision,
						isPinned: session.isPinned ?? false,
						pinnedAt: session.pinnedAt ?? undefined,
						prs: (session.prs ?? []).map(toPullRequestFacts),
					};
				}),
		};
	});
}

async function fetchHostSection(host: HostId, lastGoodWorkspaces: WorkspaceSummary[] = []): Promise<HostSection[]> {
	try {
		return [{
			host,
			label: host === LOCAL_HOST ? "Local" : hostLabelFor(host),
			status: "ready",
			workspaces: await fetchWorkspaces(host),
			failure: null,
		}];
	} catch (error) {
		return [{
			host,
			label: host === LOCAL_HOST ? "Local" : hostLabelFor(host),
			status: "failed",
			workspaces: lastGoodWorkspaces,
			failure: apiErrorMessage(error, "Could not load projects"),
		}];
	}
}

function workspaceHostQueryOptions(host: HostId) {
	const queryKey = workspaceHostQueryKey(host);
	return {
		queryKey,
		queryFn: ({ client }: QueryFunctionContext<typeof queryKey>) =>
			fetchHostSection(host, client.getQueryData<HostSection[]>(queryKey)?.[0]?.workspaces),
		retry: 1,
		refetchInterval: 15_000,
	};
}

export function localWorkspaceFailure(sections: readonly HostSection[] | undefined): string | undefined {
	const local = sections?.find((section) => section.host === LOCAL_HOST);
	return local?.status === "failed" ? (local.failure ?? "Could not load projects") : undefined;
}

function combineWorkspaceQueries(results: UseQueryResult<HostSection[]>[]) {
	const local = results[0];
	const isSuccess = local?.isSuccess ?? false;
	const data = isSuccess ? results.flatMap((result) => result.data ?? []) : undefined;
	return {
		data,
		dataUpdatedAt: Math.max(0, ...results.map((result) => result.dataUpdatedAt)),
		error: local?.error ?? null,
		isError: local?.isError ?? false,
		isLoading: local?.isLoading ?? true,
		isSuccess,
		localFailure: localWorkspaceFailure(data),
		refetch: () => Promise.all(results.map((result) => result.refetch())),
	};
}

// Shared so route loaders can prefetch the local host via
// queryClient.ensureQueryData and the hook reads the same cache.
export const workspaceQueryOptions = workspaceHostQueryOptions(LOCAL_HOST);

export function useWorkspaceQuery() {
	const remotes = useSyncExternalStore(subscribeConnectedHosts, connectedHosts, connectedHosts);
	return useQueries({
		queries: [LOCAL_HOST, ...remotes].map(workspaceHostQueryOptions),
		combine: combineWorkspaceQueries,
	});
}
