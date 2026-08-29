// Standalone shell terminals: shells the user opens by hand from the topbar or
// ⌘T / Ctrl+T, with no agent session behind them. They are deliberately kept out of
// the workspaces query — they are not sessions, never appear on the board, and
// must not invalidate session state when they come and go.

import {
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { useSyncExternalStore } from "react";
import type { components } from "../../api/schema";
import { apiErrorCode } from "../lib/api-client";
import { mockShellTerminals } from "../lib/mock-data";
import {
  clientFor,
  connectedHosts,
  isHostReady,
  subscribeConnectedHosts,
} from "../lib/host-clients";
import { LOCAL_HOST, refKey, type HostId, type Ref } from "../lib/hosts";

export type ShellTerminal = {
  host: HostId;
  /** Runtime handle the terminal mux attaches to, exactly like a session pane's. */
  handleId: string;
  projectId?: string;
  /** Agent session this shell is scoped to; absent for standalone shells. */
  sessionId?: string;
  workingDir: string;
  title: string;
  createdAt: string;
};

export const shellTerminalsQueryKey = (host: HostId) =>
  ["shell-terminals", host] as const;
const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

function toShellTerminal(
  host: HostId,
  t: components["schemas"]["ShellTerminalResponse"],
): ShellTerminal {
  return {
    host,
    handleId: t.handleId,
    projectId: t.projectId,
    sessionId: t.sessionId,
    workingDir: t.workingDir,
    title: t.title,
    createdAt: t.createdAt,
  };
}

// Preview-only shell list. The browser build has no daemon to spawn a PTY, so
// open/close mutate this array instead — keeping the tab strip fully
// interactive (open, select, close) without a backend, which is what the e2e
// suite drives.
let previewShellTerminals: ShellTerminal[] = mockShellTerminals.map(
  (shell) => ({ ...shell, host: LOCAL_HOST }),
);
let previewShellSeq = 0;

async function fetchShellTerminals(host: HostId): Promise<ShellTerminal[]> {
  if (usePreviewData) {
    return previewShellTerminals.filter((shell) => shell.host === host);
  }
  if (!isHostReady(host)) {
    return [];
  }
  const { data, error } = await clientFor(host).GET("/api/v1/shell-terminals");
  if (error) throw error;
  return (data?.shellTerminals ?? []).map((shell) =>
    toShellTerminal(host, shell),
  );
}

// No refetchInterval: shell terminals only change when this client opens or
// closes one, and both mutations invalidate the query. Polling would spend a
// liveness probe per shell per interval for no new information.
export const shellTerminalsQueryOptions = (host: HostId) => ({
  queryKey: shellTerminalsQueryKey(host),
  queryFn: () => fetchShellTerminals(host),
  retry: 1,
});

export function useShellTerminals(host: HostId = LOCAL_HOST) {
  return useQuery(shellTerminalsQueryOptions(host));
}

export function useConnectedShellTerminals(): ShellTerminal[] {
  const remotes = useSyncExternalStore(
    subscribeConnectedHosts,
    connectedHosts,
    connectedHosts,
  );
  return useQueries({
    queries: [LOCAL_HOST, ...remotes].map(shellTerminalsQueryOptions),
    combine: (results) => results.flatMap((result) => result.data ?? []),
  });
}

export type OpenShellTerminalInput = { project?: Ref; session?: Ref };

/**
 * Opens a shell in the given project's root (or the daemon data dir when
 * omitted). When sessionId is set the shell is scoped to that session and only
 * appears in its tab strip; otherwise it is a standalone shell on /terminals.
 */
export function useOpenShellTerminal() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({
      project,
      session,
    }: OpenShellTerminalInput = {}): Promise<ShellTerminal> => {
      if (project && session && project.host !== session.host)
        throw new Error("shell target hosts do not match");
      const host = session?.host ?? project?.host ?? LOCAL_HOST;
      const projectId = project?.id;
      const sessionId = session?.id;
      if (usePreviewData) {
        previewShellSeq += 1;
        const shell: ShellTerminal = {
          host,
          handleId: `shellterm-preview-${previewShellSeq}`,
          projectId,
          sessionId,
          workingDir: `/Users/demo/Projects/${projectId ?? "ao"}`,
          title: projectId ?? "shell",
          createdAt: new Date().toISOString(),
        };
        previewShellTerminals = [...previewShellTerminals, shell];
        return shell;
      }
      const body: { projectId?: string; sessionId?: string } = {};
      if (projectId) body.projectId = projectId;
      if (sessionId) body.sessionId = sessionId;
      const { data, error } = await clientFor(host).POST(
        "/api/v1/shell-terminals",
        { body },
      );
      if (error) throw error;
      if (!data) throw new Error("Daemon returned no shell terminal");
      return toShellTerminal(host, data.shellTerminal);
    },
    onSuccess: (shell) => {
      // The POST already returned the authoritative terminal. Publish it to the
      // shared list immediately so its tab can render and receive focus without
      // waiting for a second daemon round trip. The background refetch still
      // reconciles concurrent changes from another window.
      const queryKey = shellTerminalsQueryKey(shell.host);
      queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) => {
        if (current?.some((candidate) => candidate.handleId === shell.handleId))
          return current;
        return [...(current ?? []), shell];
      });
      void queryClient.invalidateQueries({ queryKey });
    },
    // Without this, a failed open (worktree gone, no shell resolvable, daemon
    // busy) leaves the "+" button looking like it silently did nothing.
    onError: (error) => {
      console.error("Failed to open shell terminal:", error);
    },
  });
}

/** Closes a shell and destroys its PTY. */
export function useCloseShellTerminal() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (terminal: Ref): Promise<void> => {
      if (usePreviewData) {
        previewShellTerminals = previewShellTerminals.filter(
          (s) => refKey({ host: s.host, id: s.handleId }) !== refKey(terminal),
        );
        return;
      }
      const { error } = await clientFor(terminal.host).DELETE(
        "/api/v1/shell-terminals/{handleId}",
        {
          params: { path: { handleId: terminal.id } },
        },
      );
      if (error) throw error;
    },
    onMutate: async (terminal) => {
      const queryKey = shellTerminalsQueryKey(terminal.host);
      const previous = queryClient.getQueryData<ShellTerminal[]>(queryKey);
      const removeClosedShell = () => {
        queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) =>
          current?.filter((shell) => shell.handleId !== terminal.id),
        );
      };
      // Remove the pill synchronously. Waiting for cancellation first leaves the
      // closed tab visible for the duration of an in-flight list request.
      removeClosedShell();
      await queryClient.cancelQueries({ queryKey });
      // A request that resolved while cancellation was being scheduled may have
      // restored its stale snapshot; make the optimistic state authoritative.
      removeClosedShell();
      return { previous, queryKey };
    },
    onError: (error, _handleId, context) => {
      // A 404 means the daemon has already removed the shell, so restoring its
      // stale tab would be misleading. Other failures put the tab back so the
      // user can retry instead of losing access to a still-live PTY.
      if (
        apiErrorCode(error) !== "SHELL_TERMINAL_NOT_FOUND" &&
        context?.previous
      ) {
        queryClient.setQueryData(context.queryKey, context.previous);
      }
    },
    // Settled, not success: a close that 404s means the daemon already lost
    // the shell, and the stale tab still needs to disappear.
    onSettled: (_data, _error, terminal) => {
      void queryClient.invalidateQueries({
        queryKey: shellTerminalsQueryKey(terminal.host),
      });
    },
  });
}

export type RenameShellTerminalInput = { terminal: Ref; title: string };

/** Renames a shell terminal's tab. The new title persists on the daemon. */
export function useRenameShellTerminal() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async ({
      terminal,
      title,
    }: RenameShellTerminalInput): Promise<ShellTerminal> => {
      if (usePreviewData) {
        previewShellTerminals = previewShellTerminals.map((s) =>
          refKey({ host: s.host, id: s.handleId }) === refKey(terminal)
            ? { ...s, title }
            : s,
        );
        const shell = previewShellTerminals.find(
          (s) => refKey({ host: s.host, id: s.handleId }) === refKey(terminal),
        );
        if (!shell) throw new Error("No such shell terminal");
        return shell;
      }
      const { data, error } = await clientFor(terminal.host).PATCH(
        "/api/v1/shell-terminals/{handleId}",
        {
          params: { path: { handleId: terminal.id } },
          body: { title },
        },
      );
      if (error) throw error;
      if (!data) throw new Error("Daemon returned no shell terminal");
      return toShellTerminal(terminal.host, data.shellTerminal);
    },
    onSuccess: (shell) => {
      void queryClient.invalidateQueries({
        queryKey: shellTerminalsQueryKey(shell.host),
      });
    },
  });
}
