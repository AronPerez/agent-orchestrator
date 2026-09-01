import {
  useQueries,
  useQuery,
  type QueryFunctionContext,
  type UseQueryResult,
} from "@tanstack/react-query";
import type { TraySessionEntry } from "../../shared/tray";
import { useMemo, useSyncExternalStore } from "react";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import type { CloudCpProject, CloudCpSession } from "../lib/cloud-cp";
import {
  clientFor,
  connectedHosts,
  hostLabelFor,
  isHostReady,
  subscribeConnectedHosts,
} from "../lib/host-clients";
import { hostConnectionState } from "../lib/host-events";
import { reportHostQueryFailed } from "../lib/host-telemetry";
import {
  cloudHost,
  isCloudHost,
  LOCAL_HOST,
  type HostId,
  type Ref,
} from "../lib/hosts";
import { useCloudCp } from "./useCloudCp";
import { useCloudOrg } from "./useCloudOrg";
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
  flattenHostSections,
  toAgentProvider,
  toKanbanColumn,
  toProjectKind,
  toSessionActivity,
  toSessionStatus,
	newestActiveOrchestrator,
	attentionZone,
	workerSessions,
  type WorkspaceSession,
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

function toPullRequestFacts(
  pr: components["schemas"]["SessionPRFacts"],
): PullRequestFacts {
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

function reportUnknownSessionField(
  field: "status" | "activity",
  value?: string,
): void {
  const reason = value ? "unrecognized" : "missing";
  const key = `${field}:${reason}`;
  if (reportedUnknownSessionFields.has(key)) return;
  reportedUnknownSessionFields.add(key);
  void captureRendererEvent("ao.renderer.session_state_unknown", {
    field,
    reason,
  });
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
  return (
    typeof project.id === "string" &&
    typeof project.name === "string" &&
    typeof project.path === "string"
  );
}

function isSession(value: unknown): value is SessionDTO {
  if (typeof value !== "object" || value === null) return false;
  const session = value as Partial<SessionDTO>;
  return (
    typeof session.id === "string" && typeof session.projectId === "string"
  );
}

function tagWorkspaces(
  host: HostId,
  workspaces: WorkspaceSummary[],
): WorkspaceSummary[] {
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
  const [
    { data: projectsData, error: projectsError, response: projectsResponse },
    { data: sessionsData, error: sessionsError, response: sessionsResponse },
  ] = await Promise.all([
    client.GET("/api/v1/projects"),
    client.GET("/api/v1/sessions"),
  ]).catch((error: unknown) => {
    if (error instanceof SyntaxError)
      throw new Error("Host returned malformed workspace data");
    throw error;
  });

  if (projectsError || sessionsError) {
    // The status lives on the response and nowhere else, and it is what
    // separates a rotated password (401) from a host the proxy cannot reach
    // (502). Carried on the thrown error so the caller can report it; the
    // message is precomputed from the daemon's envelope so what the user
    // reads is unchanged.
    const failed = projectsError ? projectsResponse : sessionsResponse;
    throw Object.assign(
      new Error(
        apiErrorMessage(
          projectsError ?? sessionsError,
          "Could not load projects",
        ),
      ),
      {
        status: failed?.status,
      },
    );
  }
  const projects = parseResponseArray(projectsData, "projects", isProject);
  const sessions = parseResponseArray(sessionsData, "sessions", isSession);
  if (projects === null || sessions === null)
    throw new Error("Host returned malformed workspace data");

  return projects.map((project) => {
    const kind = toProjectKind(project.kind);
    return {
      host,
      id: project.id,
      name: project.name,
      kind,
      path: project.path,
      orchestratorAgent: project.orchestratorAgent
        ? toAgentProvider(project.orchestratorAgent)
        : undefined,
      sessions: sessions
        .filter((session) => session.projectId === project.id)
        .map((session) => {
          const status = toSessionStatus(session.status, session.isTerminated);
          const scmStatus = session.scmStatus
            ? toSessionStatus(session.scmStatus)
            : undefined;
          const kanbanColumn = toKanbanColumn(session.kanbanColumn, status);
          const activity = toSessionActivity(session.activity);
          if (status === "unknown")
            reportUnknownSessionField("status", session.status);
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
            kind:
              session.kind === "orchestrator"
                ? "orchestrator"
                : session.kind === "worker"
                  ? "worker"
                  : undefined,
            // Carried through verbatim: the session surface must render from
            // the mode this session was created with, not from whatever the
            // current default happens to be.
            mode: session.mode === "chat" ? "chat" : "tui",
            branch: session.branch || undefined,
            status,
            scmStatus,
            kanbanColumn,
            displayStatus: session.displayStatus || undefined,
            isTerminated: session.isTerminated,
            terminateOnPrMerge: session.terminateOnPrMerge ?? false,
            autoInjectReview: session.autoInjectReview ?? true,
            autoInjectCI: session.autoInjectCI ?? true,
            createdAt: session.createdAt,
            updatedAt: session.updatedAt,
            lastUserMessageAt: session.lastUserMessageAt ?? undefined,
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

function errorStatus(error: unknown): number | undefined {
  const status = (error as { status?: unknown } | null)?.status;
  return typeof status === "number" ? status : undefined;
}

async function fetchHostSection(
  host: HostId,
  lastGoodWorkspaces: WorkspaceSummary[] = [],
): Promise<HostSection[]> {
  const label = host === LOCAL_HOST ? "Local" : hostLabelFor(host);
  try {
    const workspaces = await fetchWorkspaces(host);
    // Read after the fetch, not before: this is what the sidebar shows when it
    // renders these workspaces, and a stream that dropped mid-fetch is exactly
    // the case worth catching.
    return [
      {
        host,
        label,
        status: "ready",
        streamState: hostConnectionState(host),
        workspaces,
        failure: null,
      },
    ];
  } catch (error) {
    // Remote clients are plain openapi-fetch clients, so none of this reaches
    // api-client's ao.renderer.api_error. Without this a remote host's data
    // simply stopped loading, silently.
    reportHostQueryFailed(host, errorStatus(error));
    return [
      {
        host,
        label,
        status: "failed",
        streamState: hostConnectionState(host),
        workspaces: lastGoodWorkspaces,
        failure: apiErrorMessage(error, "Could not load projects"),
      },
    ];
  }
}

function workspaceHostQueryOptions(host: HostId) {
  const queryKey = workspaceHostQueryKey(host);
  return {
    queryKey,
    queryFn: ({ client }: QueryFunctionContext<typeof queryKey>) =>
      fetchHostSection(
        host,
        client.getQueryData<HostSection[]>(queryKey)?.[0]?.workspaces,
      ),
    retry: 1,
    refetchInterval: 15_000,
  };
}

export function localWorkspaceFailure(
  sections: readonly HostSection[] | undefined,
): string | undefined {
  const local = sections?.find((section) => section.host === LOCAL_HOST);
  return local?.status === "failed"
    ? (local.failure ?? "Could not load projects")
    : undefined;
}

function combineWorkspaceQueries(results: UseQueryResult<HostSection[]>[]) {
  const local = results[0];
  const isSuccess = local?.isSuccess ?? false;
  const data = isSuccess
    ? results.flatMap((result) => result.data ?? [])
    : undefined;
  return {
    data,
    dataUpdatedAt: Math.max(
      0,
      ...results.map((result) => result.dataUpdatedAt),
    ),
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

// Cloud projects are a separate query so a control-plane failure can never
// break the local list: on error TanStack keeps this query's last known data,
// and until the first successful fetch the merge below simply omits cloud
// items. Invalidated by the cloud create flow (CreateProjectFlow).
export const cloudProjectsQueryKey = ["cloud-projects"] as const;
export const cloudSessionsQueryKey = ["cloud-sessions"] as const;

// Maps one control-plane session onto the board's session shape. Cloud sessions
// carry the same status/activity/harness vocabulary as local ones, so the same
// product-ui mappers apply; fields with no cloud analogue take safe defaults.
function toCloudWorkspaceSession(
  session: CloudCpSession,
  project: CloudCpProject,
  orgId: string,
): WorkspaceSession {
  return {
    host: cloudHost(orgId),
    id: session.id,
    // The terminal pane only mounts for a session that has a terminal handle.
    // A cloud session's PTY is addressed by the session id over its ticketed
    // CP WebSocket, so the session id is its handle.
    terminalHandleId: session.id,
    workspaceId: project.id,
    workspaceName: project.displayName,
    title: session.displayName || session.id,
    provider: toAgentProvider(session.harness),
    kind: session.kind === "orchestrator" ? "orchestrator" : "worker",
    branch: session.branch || undefined,
    status: toSessionStatus(session.status, session.isTerminated),
    isTerminated: session.isTerminated,
    createdAt: session.createdAt,
    updatedAt: session.updatedAt,
    activity: toSessionActivity({ state: session.activityState }),
    prs: [],
    // Marks this as a control-plane session so the terminal opens against the
    // CP (ticket + sandbox WebSocket) instead of the local daemon mux.
    cloud: { orgId },
  };
}

function toCloudWorkspace(
  project: CloudCpProject,
  sessions: CloudCpSession[],
  orgId: string,
): WorkspaceSummary {
  return {
    host: cloudHost(orgId),
    id: project.id,
    name: project.displayName,
    kind: "cloud",
    // Cloud projects run in control-plane sandboxes; there is no local folder.
    path: "",
    sessions: sessions
      .filter((session) => session.projectId === project.id)
      .map((session) => toCloudWorkspaceSession(session, project, orgId)),
  };
}

type WorkspaceSubscriptionOptions = {
	subscribed?: boolean;
};

export function useCloudProjectsQuery(options: WorkspaceSubscriptionOptions = {}) {
  const { client, ready, baseUrl } = useCloudCp();
  const { org } = useCloudOrg();
  const orgId = org?.id;
  return useQuery({
    queryKey: [...cloudProjectsQueryKey, baseUrl, orgId ?? ""],
    enabled: ready && orgId !== undefined,
		subscribed: options.subscribed,
    retry: 1,
    queryFn: async (): Promise<CloudCpProject[]> => {
      if (orgId === undefined) return [];
      // First page only (control-plane max page size); pagination UI is a
      // later phase alongside cloud sessions.
      const response = await client.listProjects(orgId, { limit: 100 });
      return response.items;
    },
  });
}

export function useCloudSessionsQuery(options: WorkspaceSubscriptionOptions = {}) {
  const { client, ready, baseUrl } = useCloudCp();
  const { org } = useCloudOrg();
  const orgId = org?.id;
  return useQuery({
    queryKey: [...cloudSessionsQueryKey, baseUrl, orgId ?? ""],
    enabled: ready && orgId !== undefined,
		subscribed: options.subscribed,
    retry: 1,
    // A provisioning sandbox changes state without a client action, so poll to
    // reflect requested -> running -> ready the same way local sessions stream.
    refetchInterval: 5000,
    queryFn: async (): Promise<CloudCpSession[]> => {
      if (orgId === undefined) return [];
      const response = await client.listSessions(orgId, { limit: 100 });
      return response.items;
    },
  });
}

export function useWorkspaceQuery(options: WorkspaceSubscriptionOptions = {}) {
  const remotes = useSyncExternalStore(
    subscribeConnectedHosts,
    connectedHosts,
    connectedHosts,
  );
  const daemon = useQueries({
    queries: [LOCAL_HOST, ...remotes].map((host) => ({
      ...workspaceHostQueryOptions(host),
      subscribed: options.subscribed,
    })),
    combine: combineWorkspaceQueries,
  });
  const cloud = useCloudProjectsQuery(options);
  const cloudSessions = useCloudSessionsQuery(options);
  const { org, ready } = useCloudOrg();
  const orgId = org?.id;
  const localData = daemon.data;
  const cloudData = cloud.data;
  const cloudSessionData = cloudSessions.data;
  const data = useMemo(() => {
    // Local stays authoritative for loading/error semantics: cloud items only
    // render once the local list exists, and never replace it.
    if (
      localData === undefined ||
      cloudData === undefined ||
      cloudData.length === 0
    )
      return localData;
    // Signing out (or turning the offering off) disables the cloud queries,
    // but react-query keeps their last data; without this gate the stale
    // cloud projects would keep rendering for a signed-out user.
    if (!ready || orgId === undefined) return localData;
    const host = cloudHost(orgId);
    const sessions = cloudSessionData ?? [];
    return [
      ...localData,
      {
        host,
        label: org?.displayName ?? "Cloud",
        status: "ready" as const,
        workspaces: cloudData.map((project) =>
          toCloudWorkspace(project, sessions, orgId),
        ),
        failure: null,
      },
    ];
  }, [localData, cloudData, cloudSessionData, orgId, org?.displayName, ready]);
  return { ...daemon, data };
}

/**
 * Subscribe a detail surface to one session instead of the complete workspace
 * tree. TanStack Query applies structural sharing to the selected value, so an
 * activity update elsewhere no longer redraws the open session workspace.
 */
export function useWorkspaceSession(sessionRef: Ref) {
	const selectLocalSession = useMemo(
		() => (sections: HostSection[]) =>
			flattenHostSections(sections)
				.flatMap((workspace) => workspace.sessions)
				.find((session) => session.host === sessionRef.host && session.id === sessionRef.id),
		[sessionRef.host, sessionRef.id],
	);
	const local = useQuery({
		...workspaceHostQueryOptions(sessionRef.host),
		enabled: !isCloudHost(sessionRef.host),
		select: selectLocalSession,
	});
	const cloud = useCloudProjectsQuery();
	const cloudSessions = useCloudSessionsQuery();
	const { org, ready } = useCloudOrg();
	const cloudSession = useMemo(() => {
		if (!ready || !org?.id || sessionRef.host !== cloudHost(org.id) || !cloud.data || !cloudSessions.data)
			return undefined;
		const session = cloudSessions.data.find((candidate) => candidate.id === sessionRef.id);
		if (!session) return undefined;
		const project = cloud.data.find((candidate) => candidate.id === session.projectId);
		return project ? toCloudWorkspaceSession(session, project, org.id) : undefined;
	}, [cloud.data, cloudSessions.data, org?.id, ready, sessionRef.host, sessionRef.id]);
	return { ...local, data: local.data ?? cloudSession };
}

export type WorkspaceScope = {
	project?: Pick<WorkspaceSummary, "id" | "kind" | "name" | "orchestratorAgent">;
	session?: WorkspaceSession;
	orchestrator?: WorkspaceSession;
};

function selectWorkspaceScope(
	workspaces: WorkspaceSummary[],
	projectId: string | undefined,
	sessionId: string | undefined,
): WorkspaceScope {
	const session = sessionId
		? workspaces.flatMap((workspace) => workspace.sessions).find((candidate) => candidate.id === sessionId)
		: undefined;
	const resolvedProjectId = session?.workspaceId ?? projectId;
	const workspace = resolvedProjectId ? workspaces.find((candidate) => candidate.id === resolvedProjectId) : undefined;
	// Do not carry the project's complete sessions array into shell chrome. With
	// React Query's structural sharing, this small metadata projection retains
	// its identity when another session in the same project streams an update.
	const project = workspace
		? {
				id: workspace.id,
				kind: workspace.kind,
				name: workspace.name,
				orchestratorAgent: workspace.orchestratorAgent,
			}
		: undefined;
	return { project, session, orchestrator: workspace ? newestActiveOrchestrator(workspace.sessions) : undefined };
}

function selectHostWorkspaceScope(
	sections: HostSection[],
	projectId: string | undefined,
	sessionId: string | undefined,
): WorkspaceScope {
	return selectWorkspaceScope(flattenHostSections(sections), projectId, sessionId);
}

/**
 * Subscribe shell chrome to just the routed project and session. This avoids
 * redrawing the topbar for streamed activity from every other project.
 */
export function useWorkspaceScope(projectId?: string, sessionId?: string) {
	const selectLocalScope = useMemo(
		() => (sections: HostSection[]) => selectHostWorkspaceScope(sections, projectId, sessionId),
		[projectId, sessionId],
	);
	const local = useQuery({ ...workspaceQueryOptions, select: selectLocalScope });
	const cloud = useCloudProjectsQuery();
	const cloudSessions = useCloudSessionsQuery();
	const { org, ready } = useCloudOrg();
	const cloudScope = useMemo(() => {
		if (!ready || !org?.id || !cloud.data) return undefined;
		const workspaces = cloud.data.map((project) => toCloudWorkspace(project, cloudSessions.data ?? [], org.id));
		return selectWorkspaceScope(workspaces, projectId, sessionId);
	}, [cloud.data, cloudSessions.data, org?.id, projectId, ready, sessionId]);
	// Match useWorkspaceQuery's local-first semantics: do not reveal cloud
	// records before the local workspace query has resolved successfully.
	return { ...local, data: local.data ?? (local.isSuccess ? cloudScope : undefined) };
}

function selectTraySessions(workspaces: WorkspaceSummary[]): TraySessionEntry[] {
	const entries: TraySessionEntry[] = [];
	for (const workspace of workspaces) {
		for (const session of workerSessions(workspace.sessions)) {
			const zone = attentionZone(session);
			if ((zone === "merge" && session.status === "merged") || (zone !== "action" && zone !== "merge")) continue;
				entries.push({
					host: session.host,
					projectId: workspace.id,
				projectName: workspace.name,
				sessionId: session.id,
				title: session.title,
				zone,
			});
		}
	}
	return entries;
}

function selectHostTraySessions(sections: HostSection[]): TraySessionEntry[] {
	return selectTraySessions(flattenHostSections(sections));
}

/**
 * The tray lives for the whole app lifetime, but only attention-worthy worker
 * sessions affect its native payload. Select that compact projection at the
 * query boundary so ordinary streamed activity does not wake the runtime.
 */
export function useWorkspaceTraySessions() {
	const local = useQuery({ ...workspaceQueryOptions, select: selectHostTraySessions });
	const cloud = useCloudProjectsQuery();
	const cloudSessions = useCloudSessionsQuery();
	const { org, ready } = useCloudOrg();
	const cloudEntries = useMemo(() => {
		if (!ready || !org?.id || !cloud.data) return [];
		return selectTraySessions(cloud.data.map((project) => toCloudWorkspace(project, cloudSessions.data ?? [], org.id)));
	}, [cloud.data, cloudSessions.data, org?.id, ready]);
	const data = useMemo(() => {
		if (local.data === undefined) return undefined;
		return cloudEntries.length === 0 ? local.data : [...local.data, ...cloudEntries];
	}, [cloudEntries, local.data]);
	return { ...local, data };
}
