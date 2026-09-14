import {
  type QueryClient,
  useMutation,
  useMutationState,
  useQueryClient,
} from "@tanstack/react-query";
import {
  toKanbanColumn,
  type WorkspaceSession,
  type WorkspaceSummary,
} from "../types/workspace";
import { cloudSessionsQueryKey, workspaceQueryKey } from "./useWorkspaceQuery";
import { apiErrorMessage } from "../lib/api-client";
import { clientFor } from "../lib/host-clients";
import { refKey, type Ref } from "../lib/hosts";
import { captureRendererEvent } from "../lib/telemetry";
import { createRendererCloudCpClient } from "./useCloudCp";
import { settingsQueryKey, type Settings } from "./useSettings";

type TerminateSessionOptions = {
  onSuccess?: (session: WorkspaceSession) => void;
};

export const terminateSessionMutationKey = ["terminate-session"] as const;

async function terminateSession(
  queryClient: QueryClient,
  session: WorkspaceSession,
): Promise<void> {
  if (session.cloud) {
    const settings = queryClient.getQueryData<Settings>(settingsQueryKey);
    const baseUrl = settings?.cloudControlPlaneUrl ?? "";
    if (baseUrl === "")
      throw new Error("The cloud control plane is not configured.");
    await createRendererCloudCpClient(baseUrl).deleteSession(
      session.cloud.orgId,
      session.id,
    );
    return;
  }

  const { error, response } = await clientFor(session.host).POST(
    "/api/v1/sessions/{sessionId}/kill",
    {
      params: { path: { sessionId: session.id } },
    },
  );
  if (error) {
    const fallback = response
      ? `Failed to terminate session (${response.status})`
      : "Failed to terminate session";
    throw new Error(apiErrorMessage(error, fallback));
  }
}

// A killed session keeps its row and flips to terminated, which is exactly what
// the next workspace fetch would report. Applying it locally lets the board
// settle on the click rather than on the refetch.
function markTerminated(ref: Ref) {
  return (session: WorkspaceSession): WorkspaceSession =>
    refKey(session) === refKey(ref)
      ? {
          ...session,
          isTerminated: true,
          status: "terminated",
          kanbanColumn: toKanbanColumn(undefined, "terminated"),
        }
      : session;
}

type TerminateSessionMutationState = {
  error: unknown;
  session?: WorkspaceSession;
  status: "error" | "idle" | "pending" | "success";
  submittedAt: number;
};

function useTerminateSessionMutations() {
  return useMutationState<TerminateSessionMutationState>({
    filters: { mutationKey: terminateSessionMutationKey },
    select: (mutation) => ({
      error: mutation.state.error,
      session: mutation.state.variables as WorkspaceSession | undefined,
      status: mutation.state.status,
      submittedAt: mutation.state.submittedAt,
    }),
  });
}

function summarizeBySession(mutations: TerminateSessionMutationState[]) {
  const summaries = new Map<
    string,
    {
      isPending: boolean;
      latest: TerminateSessionMutationState;
      session: WorkspaceSession;
    }
  >();
  for (const mutation of mutations) {
    if (!mutation.session) continue;
    const key = refKey(mutation.session);
    const current = summaries.get(key);
    if (!current) {
      summaries.set(key, {
        isPending: mutation.status === "pending",
        latest: mutation,
        session: mutation.session,
      });
      continue;
    }
    current.isPending ||= mutation.status === "pending";
    if (mutation.submittedAt >= current.latest.submittedAt)
      current.latest = mutation;
  }
  return [...summaries.values()];
}

export function useTerminateSession(options: TerminateSessionOptions = {}) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: terminateSessionMutationKey,
    mutationFn: async (session: WorkspaceSession) => {
      void captureRendererEvent("ao.renderer.session_kill_requested", {
        project_id: session.workspaceId,
      });
      await terminateSession(queryClient, session);
    },
    onSuccess: (_data, session) => {
      void captureRendererEvent("ao.renderer.session_kill_succeeded", {
        project_id: session.workspaceId,
      });
      // Write the outcome into the cached board first, then refresh in the
      // background. A mutation stays `pending` until its onSuccess settles,
      // so awaiting the refetch here kept the row's spinner up for a whole
      // extra round trip after the daemon had already finished the kill.
      queryClient.setQueryData<WorkspaceSummary[]>(
        workspaceQueryKey,
        (workspaces) =>
          workspaces?.map((workspace) =>
            workspace.host === session.host &&
            workspace.id === session.workspaceId
              ? {
                  ...workspace,
                  sessions: workspace.sessions.map(markTerminated(session)),
                }
              : workspace,
          ),
      );
      void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
      // A cloud kill also lives in the cloud sessions query, which the board
      // merges in separately, so refresh it too.
      if (session.cloud)
        void queryClient.invalidateQueries({ queryKey: cloudSessionsQueryKey });
      options.onSuccess?.(session);
    },
    onError: (_error, session) => {
      void captureRendererEvent("ao.renderer.session_kill_failed", {
        project_id: session.workspaceId,
      });
    },
  });
}

export function useTerminateSessionState(ref: Ref) {
  const summary = summarizeBySession(useTerminateSessionMutations()).find(
    ({ session }) => refKey(session) === refKey(ref),
  );

  return {
    error:
      !summary?.isPending &&
      summary?.latest.status === "error" &&
      summary.latest.error instanceof Error
        ? summary.latest.error.message
        : null,
    isPending: summary?.isPending ?? false,
  };
}

export function useProjectTerminateSessionStates(project: Ref | undefined) {
  return summarizeBySession(useTerminateSessionMutations())
    .filter(({ isPending, latest, session }) => {
      return (
        project !== undefined &&
        session.host === project.host &&
        session.workspaceId === project.id &&
        (isPending || latest.status === "error")
      );
    })
    .sort((a, b) => b.latest.submittedAt - a.latest.submittedAt)
    .map(({ isPending, latest, session }) => ({
      error:
        !isPending && latest.error instanceof Error
          ? latest.error.message
          : null,
      isPending,
      session,
    }));
}

export function clearTerminateSessionState(queryClient: QueryClient, ref: Ref) {
  const mutationCache = queryClient.getMutationCache();
  for (const mutation of mutationCache.findAll({
    mutationKey: terminateSessionMutationKey,
  })) {
    const target = mutation.state.variables as WorkspaceSession | undefined;
    if (
      target &&
      refKey(target) === refKey(ref) &&
      mutation.state.status !== "pending"
    ) {
      mutationCache.remove(mutation);
    }
  }
}
