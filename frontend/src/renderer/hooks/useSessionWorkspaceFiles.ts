import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useSyncExternalStore } from "react";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientFor } from "../lib/host-clients";
import { LOCAL_HOST, refKey, type Ref } from "../lib/hosts";
import {
  getWorkspaceFileConnectionState,
  subscribeWorkspaceFileChanges,
  subscribeWorkspaceFileConnectionState,
  type WorkspaceFileConnectionState,
} from "../lib/workspace-file-events";

export type WorkspaceCompareMode = "base" | "head_fallback";
export type WorkspaceFileSummary =
  components["schemas"]["WorkspaceFileSummary"] & {
    previousPath?: string;
  };
export type WorkspaceFileSections =
  components["schemas"]["WorkspaceFileSections"];
export type WorkspaceCommitSummary =
  components["schemas"]["WorkspaceCommitSummary"];
export type WorkspaceSummary = components["schemas"]["WorkspaceSummary"];
export type WorkspaceFilesResponse =
  components["schemas"]["ListWorkspaceFilesResponse"] & {
    compareMode?: WorkspaceCompareMode;
  };
export type WorkspaceFileDetail =
  components["schemas"]["WorkspaceFileResponse"] & {
    previousPath?: string;
    compareMode?: WorkspaceCompareMode;
  };
const emptySessionRef: Ref = { host: LOCAL_HOST, id: "" };

export const sessionWorkspaceFilesQueryKey = (session: Ref) =>
  ["session-workspace-files", refKey(session)] as const;
const WORKSPACE_FILES_DEGRADED_REFETCH_MS = 30_000;

async function fetchSessionWorkspaceFiles(
  session: Ref,
  errorMessage: string,
): Promise<WorkspaceFilesResponse> {
  const { data, error } = await clientFor(session.host).GET(
    "/api/v1/sessions/{sessionId}/workspace/files",
    {
      params: { path: { sessionId: session.id } },
    },
  );
  if (error) throw new Error(apiErrorMessage(error, errorMessage));
  return (data ?? {
    sessionId: session.id,
    files: [],
    truncated: false,
    sections: { staged: [], unstaged: [], untracked: [], committed: [] },
    commits: [],
    summary: { files: 0, additions: 0, deletions: 0 },
  }) as WorkspaceFilesResponse;
}

export const sessionWorkspaceFileQueryKey = (session: Ref, path: string) =>
  ["session-workspace-file", refKey(session), path] as const;

async function fetchSessionWorkspaceFile(
  session: Ref,
  path: string,
  errorMessage: string,
): Promise<WorkspaceFileDetail> {
  const { data, error } = await clientFor(session.host).GET(
    "/api/v1/sessions/{sessionId}/workspace/file",
    {
      params: { path: { sessionId: session.id }, query: { path } },
    },
  );
  if (error) throw new Error(apiErrorMessage(error, errorMessage));
  if (!data) throw new Error(errorMessage);
  return data as WorkspaceFileDetail;
}

// Shared so the diff view (expand-on-demand) and the plain read-only viewer
// always resolve to the same cache entry for a given (session, path).
export function sessionWorkspaceFileQueryOptions(
  session: Ref,
  path: string,
  errorMessage = "Unable to load workspace file",
) {
  return {
    queryKey: sessionWorkspaceFileQueryKey(session, path),
    queryFn: () => fetchSessionWorkspaceFile(session, path, errorMessage),
  };
}

// Shared so SessionFileExplorer and SessionInspector resolve to the same cache
// entry while SSE invalidation remains the normal refresh path.
export function sessionWorkspaceFilesQueryOptions(
  session: Ref,
  errorMessage = "Unable to load workspace files",
) {
  return {
    queryKey: sessionWorkspaceFilesQueryKey(session),
    queryFn: () => fetchSessionWorkspaceFiles(session, errorMessage),
  };
}

export function workspaceFilesRefetchInterval(
  state: WorkspaceFileConnectionState,
): false | number {
  return state === "degraded" ? WORKSPACE_FILES_DEGRADED_REFETCH_MS : false;
}

export function useWorkspaceFileConnectionState(
  session: Ref,
): WorkspaceFileConnectionState {
  const subscribe = useCallback(
    (listener: () => void) =>
      subscribeWorkspaceFileConnectionState(session, listener),
    [session.host, session.id],
  );
  const getSnapshot = useCallback(
    () => getWorkspaceFileConnectionState(session),
    [session.host, session.id],
  );
  return useSyncExternalStore(subscribe, getSnapshot);
}

export function isChangedWorkspaceFile(file: WorkspaceFileSummary): boolean {
  return file.status !== "unmodified";
}

// Keep the lightweight summary query warm while the inspector is open. The
// Files view then mounts against current cache data instead of flashing a
// misleading zero while its first request starts.
export function useSessionWorkspaceFilesChangedCount(
  session: Ref | undefined,
): number | undefined {
  const queryClient = useQueryClient();
  const sessionHost = session?.host;
  const sessionId = session?.id;
  const query = useQuery({
    ...sessionWorkspaceFilesQueryOptions(session ?? emptySessionRef),
    enabled: Boolean(session),
    // Live invalidations keep the inactive tab fresh; polling starts only
    // when the full Files view is visible.
    refetchInterval: false,
    select: (data: WorkspaceFilesResponse) =>
      data.files.filter(isChangedWorkspaceFile).length,
  });
  useEffect(() => {
    if (!sessionHost || !sessionId) return;
    return subscribeWorkspaceFileChanges(
      { host: sessionHost, id: sessionId },
      queryClient,
    );
  }, [queryClient, sessionHost, sessionId]);
  return session ? query.data : undefined;
}
