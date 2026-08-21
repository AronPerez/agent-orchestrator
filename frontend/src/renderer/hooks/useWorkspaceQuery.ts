import { isCancelledError, useQueries, type QueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientFor, connectedHosts, isHostReady } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { mockWorkspaces } from "../lib/mock-data";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import { parseResponseArray } from "../lib/response-validation";
import { toReviewerHarnessId } from "../lib/reviewer-harnesses";
import { captureRendererEvent } from "../lib/telemetry";
import {
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
		await Promise.all([client.GET("/api/v1/projects"), client.GET("/api/v1/sessions")]);

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
						createdAt: session.createdAt,
						updatedAt: session.updatedAt,
						activity,
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

export function workspaceHostQueryKey(host: HostId) {
	return [...workspaceQueryKey, host] as const;
}

function workspaceHostQueryOptions(host: HostId) {
	return {
		queryKey: workspaceHostQueryKey(host),
		queryFn: () => fetchWorkspaces(host),
		retry: 1,
		refetchInterval: 15_000,
	};
}

function readyHostSection(host: HostId, workspaces: WorkspaceSummary[]): HostSection {
	return {
		host,
		label: host === LOCAL_HOST ? "Local" : host,
		status: "ready",
		workspaces,
		failure: null,
	};
}

function failedHostSection(host: HostId, error: unknown, workspaces: WorkspaceSummary[] = []): HostSection {
	return {
		host,
		label: host === LOCAL_HOST ? "Local" : host,
		status: "failed",
		workspaces,
		failure: apiErrorMessage(error, "Could not load projects"),
	};
}

export async function fetchWorkspaceSections(client: QueryClient, staleTime?: number): Promise<HostSection[]> {
	const hosts = [LOCAL_HOST, ...connectedHosts()];
	const outcomes = await Promise.allSettled(
		hosts.map((host) =>
			client.fetchQuery({
				...workspaceHostQueryOptions(host),
				...(staleTime === undefined ? {} : { staleTime }),
			}),
		),
	);
	const cancelled = outcomes.find((outcome) => outcome.status === "rejected" && isCancelledError(outcome.reason));
	if (cancelled?.status === "rejected") throw cancelled.reason;
	return outcomes.map((outcome, index) => {
		const host = hosts[index];
		return outcome.status === "fulfilled"
			? readyHostSection(host, outcome.value)
			: failedHostSection(host, outcome.reason, client.getQueryData<WorkspaceSummary[]>(workspaceHostQueryKey(host)));
	});
}

export function localWorkspaceFailure(sections: readonly HostSection[] | undefined): string | undefined {
	const local = sections?.find((section) => section.host === LOCAL_HOST);
	return local?.status === "failed" ? (local.failure ?? "Could not load projects") : undefined;
}

export function useWorkspaceQuery() {
	const hosts = [LOCAL_HOST, ...connectedHosts()];
	const queries = useQueries({ queries: hosts.map(workspaceHostQueryOptions) });
	const sections = hosts.flatMap((host, index) => {
		const query = queries[index];
		if (query.isPending) return [];
		return [
			query.isError ? failedHostSection(host, query.error, query.data) : readyHostSection(host, query.data ?? []),
		];
	});
	const local = queries[0];
	const data = local?.isPending ? undefined : sections;
	return {
		data,
		dataUpdatedAt: Math.max(0, ...queries.map((query) => query.dataUpdatedAt)),
		error: local?.error ?? null,
		isError: local?.isError ?? false,
		isLoading: local?.isLoading ?? true,
		isPending: local?.isPending ?? true,
		isSuccess: local?.isSuccess ?? false,
		localFailure: localWorkspaceFailure(data),
		refetch: () => Promise.all(queries.map((query) => query.refetch())),
	};
}
