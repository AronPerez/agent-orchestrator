import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { isEditorId, type EditorHandoffState, type OpenTargetId } from "../../shared/editor-handoff";
import { aoBridge } from "../lib/bridge";
import type { HostId } from "../lib/hosts";
import { captureRendererEvent } from "../lib/telemetry";

export const editorHandoffQueryKey = (host: HostId, sessionId: string) => ["editor-handoff", host, sessionId] as const;

// Electron wraps anything an ipcMain handler throws as
// "Error invoking remote method '<channel>': Error: <real message>". That prefix
// is developer noise, and the topbar renders the message verbatim, so strip it
// and surface only what the main process actually said.
const IPC_WRAPPER = /^Error invoking remote method '[^']*':\s*(?:[A-Za-z]*Error:\s*)?/;

export function editorHandoffErrorMessage(error: unknown): string | null {
	if (!(error instanceof Error)) return null;
	const message = error.message.replace(IPC_WRAPPER, "").trim();
	return message || error.message;
}

export function useEditorHandoffState(host: HostId, sessionId: string) {
	return useQuery({
		queryKey: editorHandoffQueryKey(host, sessionId),
		enabled: Boolean(sessionId),
		staleTime: 10_000,
		retry: false,
		queryFn: () => aoBridge.editorHandoff.getState({ host, sessionId }),
	});
}

export type OpenSessionTargetMutationInput = {
	host: HostId;
	sessionId: string;
	projectId: string;
	targetId?: OpenTargetId;
};

export function useOpenSessionTarget() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async ({ host, sessionId, projectId, targetId }: OpenSessionTargetMutationInput) => {
			void captureRendererEvent("ao.renderer.open_in_editor_requested", {
				project_id: projectId,
				target_kind: targetId === "file-manager" ? "file_manager" : targetId === "terminal" ? "terminal" : "editor",
				...(targetId && isEditorId(targetId) ? { editor_id: targetId } : {}),
			});
			try {
				return await aoBridge.editorHandoff.open({ host, sessionId, ...(targetId ? { targetId } : {}) });
			} catch (error) {
				// Normalize here, once, so every consumer of this mutation gets the
				// reason rather than the IPC wrapper. Callers render error.message
				// directly and should not each have to strip it.
				throw new Error(editorHandoffErrorMessage(error) ?? String(error));
			}
		},
		onSuccess: (result, input) => {
			if (result.kind === "editor" && isEditorId(result.id)) {
				queryClient.setQueryData<EditorHandoffState>(editorHandoffQueryKey(input.host, input.sessionId), (state) =>
					state ? { ...state, preferredEditorId: result.id as typeof state.preferredEditorId } : state,
				);
			}
			void captureRendererEvent("ao.renderer.open_in_editor_succeeded", {
				project_id: input.projectId,
				target_kind: result.kind,
				...(result.kind === "editor" ? { editor_id: result.id } : {}),
			});
		},
		onError: (_error, input) => {
			// The usual cause is the worktree going away after the cached state was
			// read (session killed, merged, cleaned up). Refetch so the control
			// disables itself instead of inviting the same failing click again.
			void queryClient.invalidateQueries({ queryKey: editorHandoffQueryKey(input.host, input.sessionId) });
			void captureRendererEvent("ao.renderer.open_in_editor_failed", { project_id: input.projectId });
		},
	});
}
