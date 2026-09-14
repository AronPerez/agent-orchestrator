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
export type WorkspaceFileSummary = Omit<
  components["schemas"]["WorkspaceFileSummary"],
  "editable" | "fileFingerprint"
> & {
  editable?: boolean;
  previousPath?: string;
  fileFingerprint?: string;
};
export type WorkspaceFileSections =
  components["schemas"]["WorkspaceFileSections"];
export type WorkspaceCommitSummary =
  components["schemas"]["WorkspaceCommitSummary"];
export type WorkspaceSummary = components["schemas"]["WorkspaceSummary"];
export type WorkspaceFilesResponse = Omit<
  components["schemas"]["ListWorkspaceFilesResponse"],
  "files" | "sections" | "workspaceVersion"
> & {
  compareMode?: WorkspaceCompareMode;
  files: WorkspaceFileSummary[];
  sections: {
    committed: WorkspaceFileSummary[];
    staged: WorkspaceFileSummary[];
    unstaged: WorkspaceFileSummary[];
    untracked: WorkspaceFileSummary[];
  };
  workspaceVersion?: string;
};
export type WorkspaceFileDetail = Omit<
  components["schemas"]["WorkspaceFileResponse"],
  "editable" | "fileFingerprint" | "workspaceVersion"
> & {
  editable?: boolean;
  previousPath?: string;
  compareMode?: WorkspaceCompareMode;
  fileFingerprint?: string;
  workspaceVersion?: string;
};
export type WorkspaceDiffScope =
  components["schemas"]["WorkspaceDiffRequest"]["scope"];
export type WorkspaceDiffsResponse =
  components["schemas"]["WorkspaceDiffsResponse"];
export type WorkspaceFileRevision =
  components["schemas"]["WorkspaceFileRevisionResponse"];
export type WorkspaceFileSearchResponse =
  components["schemas"]["WorkspaceFileSearchResponse"];
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
  const response = (data ?? {
    sessionId: session.id,
    files: [],
    truncated: false,
    sections: { staged: [], unstaged: [], untracked: [], committed: [] },
    commits: [],
    summary: { files: 0, additions: 0, deletions: 0 },
  }) as WorkspaceFilesResponse;
  return {
    ...response,
    commits: (response.commits ?? []).map((commit) => ({
      ...commit,
      files: commit.files ?? [],
    })),
    files: response.files ?? [],
    sections: response.sections ?? {
      staged: [],
      unstaged: [],
      untracked: [],
      committed: [],
    },
  };
}

export const sessionWorkspaceFileQueryKey = (
  session: Ref,
  path: string,
  scope: WorkspaceDiffScope = "combined",
  commitSha?: string,
) =>
  [
    "session-workspace-file",
    refKey(session),
    scope,
    commitSha ?? "",
    path,
  ] as const;

async function fetchSessionWorkspaceFile(
  session: Ref,
  path: string,
  scope: WorkspaceDiffScope,
  errorMessage: string,
  commitSha?: string,
): Promise<WorkspaceFileDetail> {
  const { data, error } = await clientFor(session.host).GET(
    "/api/v1/sessions/{sessionId}/workspace/file",
    {
      params: {
        path: { sessionId: session.id },
        query: {
          path,
          section: scope === "combined" ? undefined : scope,
          commitSha,
        },
      },
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
  scope: WorkspaceDiffScope = "combined",
  commitSha?: string,
) {
  return {
    queryKey: sessionWorkspaceFileQueryKey(session, path, scope, commitSha),
    queryFn: () =>
      fetchSessionWorkspaceFile(session, path, scope, errorMessage, commitSha),
  };
}

export const sessionWorkspaceDiffsQueryKey = (
  session: Ref,
  scope: WorkspaceDiffScope,
  paths: readonly string[],
  contextLines: number,
  ignoreWhitespace: boolean,
  workspaceVersion?: string,
  commitSha?: string,
) =>
  [
    "session-workspace-diffs",
    refKey(session),
    scope,
    commitSha ?? "",
    paths,
    contextLines,
    ignoreWhitespace,
    workspaceVersion ?? "",
  ] as const;

export function sessionWorkspaceDiffsQueryOptions({
  contextLines = 3,
  errorMessage = "Unable to load workspace changes",
  ignoreWhitespace = false,
  paths,
  scope,
  session,
  workspaceVersion,
  commitSha,
}: {
  contextLines?: number;
  errorMessage?: string;
  ignoreWhitespace?: boolean;
  paths: readonly string[];
  scope: WorkspaceDiffScope;
  session: Ref;
  workspaceVersion?: string;
  commitSha?: string;
}) {
  return {
    queryKey: sessionWorkspaceDiffsQueryKey(
      session,
      scope,
      paths,
      contextLines,
      ignoreWhitespace,
      workspaceVersion,
      commitSha,
    ),
    queryFn: async (): Promise<WorkspaceDiffsResponse> => {
      const { data, error } = await clientFor(session.host).POST(
        "/api/v1/sessions/{sessionId}/workspace/diffs",
        {
          params: { path: { sessionId: session.id } },
          body: {
            commitSha,
            contextLines,
            ignoreWhitespace,
            paths: [...paths],
            scope,
            workspaceVersion,
          },
        },
      );
      if (error) throw new Error(apiErrorMessage(error, errorMessage));
      if (!data) throw new Error(errorMessage);
      return data;
    },
  };
}

export async function fetchWorkspaceFileRevision({
  errorMessage = "Unable to load file revision",
  expectedRevision,
  path,
  scope,
  session,
  side,
  workspaceVersion,
  commitSha,
}: {
  errorMessage?: string;
  expectedRevision?: string;
  path: string;
  scope: WorkspaceDiffScope;
  session: Ref;
  side: "before" | "after";
  workspaceVersion?: string;
  commitSha?: string;
}): Promise<WorkspaceFileRevision> {
  const { data, error } = await clientFor(session.host).GET(
    "/api/v1/sessions/{sessionId}/workspace/file/revision",
    {
      params: {
        path: { sessionId: session.id },
        query: {
          path,
          scope,
          side,
          workspaceVersion,
          expectedRevision,
          commitSha,
        },
      },
    },
  );
  if (error) throw new Error(apiErrorMessage(error, errorMessage));
  if (!data) throw new Error(errorMessage);
  return data;
}

export function sessionWorkspaceFileRevisionQueryOptions({
  path,
  scope,
  session,
  side,
  workspaceVersion,
  commitSha,
}: {
  path: string;
  scope: WorkspaceDiffScope;
  session: Ref;
  side: "before" | "after";
  workspaceVersion?: string;
  commitSha?: string;
}) {
  return {
    queryKey: [
      "session-workspace-file-revision",
      refKey(session),
      scope,
      commitSha ?? "",
      side,
      path,
      workspaceVersion ?? "",
    ] as const,
    queryFn: () =>
      fetchWorkspaceFileRevision({
        session,
        path,
        scope,
        side,
        workspaceVersion,
        commitSha,
      }),
  };
}

export async function updateSessionWorkspaceFile({
  content,
  expectedFileFingerprint,
  path,
  session,
}: {
  content: string;
  expectedFileFingerprint: string;
  path: string;
  session: Ref;
}): Promise<WorkspaceFileDetail> {
  const { data, error } = await clientFor(session.host).PUT(
    "/api/v1/sessions/{sessionId}/workspace/file",
    {
      params: { path: { sessionId: session.id } },
      body: { content, expectedFileFingerprint, path },
    },
  );
  if (error)
    throw new Error(apiErrorMessage(error, "Unable to save workspace file"));
  if (!data) throw new Error("Unable to save workspace file");
  return data as WorkspaceFileDetail;
}

export function sessionWorkspaceSearchQueryOptions(
  session: Ref,
  query: string,
  errorMessage = "Unable to search workspace files",
) {
  return {
    queryKey: ["session-workspace-search", refKey(session), query] as const,
    queryFn: async (): Promise<WorkspaceFileSearchResponse> => {
      const { data, error } = await clientFor(session.host).GET(
        "/api/v1/sessions/{sessionId}/workspace/search",
        {
          params: {
            path: { sessionId: session.id },
            query: { query, limit: 100 },
          },
        },
      );
      if (error) throw new Error(apiErrorMessage(error, errorMessage));
      if (!data) throw new Error(errorMessage);
      return data;
    },
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
