import type { QueryClient } from "@tanstack/react-query";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { SessionMode } from "../types/conversation";
import {
  OrchestratorSpawnError,
  spawnOrchestrator,
} from "./spawn-orchestrator";
import type { OrchestratorReplacementFailure } from "../stores/ui-store";
import type { Ref } from "./hosts";

type NavigateToSession = (options: {
  to: "/host/$hostId/session/$sessionId";
  params: { hostId: string; sessionId: string };
}) => unknown;

type RestartProjectOrchestratorOptions = {
  project: Ref;
  queryClient: QueryClient;
  navigate: NavigateToSession;
  setProjectRestarting: (project: Ref, restarting: boolean) => void;
  setOrchestratorReplacementError: (
    project: Ref,
    failure: OrchestratorReplacementFailure | null,
  ) => void;
  onError?: (error: unknown) => void;
  mode?: SessionMode;
};

async function refreshWorkspaceState(queryClient: QueryClient) {
  try {
    await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
  } catch {
    // The restart outcome is more important than cache refresh bookkeeping:
    // callers still need navigation/error state even if refetching fails.
  }
}

export async function restartProjectOrchestrator({
  project,
  queryClient,
  navigate,
  setProjectRestarting,
  setOrchestratorReplacementError,
  onError,
  mode,
}: RestartProjectOrchestratorOptions) {
  // Keep the initiating control focused while the replacement is pending; blur
  // only immediately before navigation so keyboard users retain their context.
  const activeElement = document.activeElement;
  setProjectRestarting(project, true);
  try {
    const sessionId = await spawnOrchestrator(project, "restart", true, mode);
    await refreshWorkspaceState(queryClient);
    setOrchestratorReplacementError(project, null);
    if (activeElement instanceof HTMLElement) activeElement.blur();
    void navigate({
      to: "/host/$hostId/session/$sessionId",
      params: { hostId: project.host, sessionId },
    });
  } catch (error) {
    await refreshWorkspaceState(queryClient);
    setOrchestratorReplacementError(project, {
      message:
        error instanceof Error
          ? error.message
          : "Could not replace orchestrator",
      ...(error instanceof OrchestratorSpawnError
        ? { code: error.code, requestId: error.requestId }
        : {}),
    });
    onError?.(error);
  } finally {
    setProjectRestarting(project, false);
  }
}
