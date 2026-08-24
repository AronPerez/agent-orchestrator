import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientFor } from "../lib/host-clients";
import { LOCAL_HOST, refKey, type Ref } from "../lib/hosts";
import { subscribeWorkspaceFileChanges } from "../lib/workspace-file-events";

export type WorkspaceCompareMode = "base" | "head_fallback";
export type WorkspaceFileSummary = components["schemas"]["WorkspaceFileSummary"] & {
	previousPath?: string;
};
export type WorkspaceFilesResponse = components["schemas"]["ListWorkspaceFilesResponse"] & {
	compareMode?: WorkspaceCompareMode;
};
const emptySessionRef: Ref = { host: LOCAL_HOST, id: "" };

export const sessionWorkspaceFilesQueryKey = (session: Ref) => ["session-workspace-files", refKey(session)] as const;

async function fetchSessionWorkspaceFiles(session: Ref, errorMessage: string): Promise<WorkspaceFilesResponse> {
	const { data, error } = await clientFor(session.host).GET("/api/v1/sessions/{sessionId}/workspace/files", {
		params: { path: { sessionId: session.id } },
	});
	if (error) throw new Error(apiErrorMessage(error, errorMessage));
	return (data ?? { sessionId: session.id, files: [], truncated: false }) as WorkspaceFilesResponse;
}

// Shared so SessionFilesView (full fetch + polling) and SessionInspector
// (eager fetch + live invalidation) always resolve to the same cache entry.
export function sessionWorkspaceFilesQueryOptions(session: Ref, errorMessage = "Unable to load workspace files") {
	return {
		queryKey: sessionWorkspaceFilesQueryKey(session),
		queryFn: () => fetchSessionWorkspaceFiles(session, errorMessage),
		// SSE (subscribeWorkspaceFileChanges) already invalidates this query
		// immediately on real filesystem changes and on reconnect, triggering
		// an instant refetch regardless of this interval. Polling is only a
		// safety net for missed/dropped SSE events, so it can stay slow.
		refetchInterval: 30_000,
	};
}

export function isChangedWorkspaceFile(file: WorkspaceFileSummary): boolean {
	return file.status !== "unmodified";
}

// Keep the lightweight summary query warm while the inspector is open. The
// Files view then mounts against current cache data instead of flashing a
// misleading zero while its first request starts.
export function useSessionWorkspaceFilesChangedCount(session: Ref | undefined): number | undefined {
	const queryClient = useQueryClient();
	const sessionHost = session?.host;
	const sessionId = session?.id;
	const query = useQuery({
		...sessionWorkspaceFilesQueryOptions(session ?? emptySessionRef),
		enabled: Boolean(session),
		// Live invalidations keep the inactive tab fresh; polling starts only
		// when the full Files view is visible.
		refetchInterval: false,
		select: (data: WorkspaceFilesResponse) => data.files.filter(isChangedWorkspaceFile).length,
	});
	useEffect(() => {
		if (!sessionHost || !sessionId) return;
		return subscribeWorkspaceFileChanges({ host: sessionHost, id: sessionId }, queryClient);
	}, [queryClient, sessionHost, sessionId]);
	return session ? query.data : undefined;
}
