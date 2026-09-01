// Standalone shell terminals: shells the user opens by hand from the topbar or
// ⌘T / Ctrl+T, with no agent session behind them. They are deliberately kept out of
// the workspaces query — they are not sessions, never appear on the board, and
// must not invalidate session state when they come and go.

import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSyncExternalStore } from "react";
import type { components } from "../../api/schema";
import { apiErrorCode } from "../lib/api-client";
import {
	clientFor,
	connectedHosts,
	isHostReady,
	subscribeConnectedHosts,
} from "../lib/host-clients";
import { LOCAL_HOST, refKey, type HostId, type Ref } from "../lib/hosts";
import { mockShellTerminals } from "../lib/mock-data";
import { isWindowsPlatform } from "../lib/platform";
import { terminalShellRequestValue, useTerminalShellStore } from "../stores/terminal-shell-store";

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
	/** Exists only while the daemon is creating the PTY. */
	optimistic?: true;
};

export const shellTerminalsQueryKey = (host: HostId) => ["shell-terminals", host] as const;
const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

function isLegacyDirectoryTitle(title: string, workingDir: string): boolean {
	const parts = workingDir.split(/[\\/]/).filter(Boolean);
	return parts.at(-1) === title;
}

function toShellTerminal(host: HostId, terminal: components["schemas"]["ShellTerminalResponse"]): ShellTerminal {
	const title = isLegacyDirectoryTitle(terminal.title, terminal.workingDir) ? "Terminal" : terminal.title;
	return {
		host,
		handleId: terminal.handleId,
		projectId: terminal.projectId,
		sessionId: terminal.sessionId,
		workingDir: terminal.workingDir,
		title: title === "Terminal" ? "Terminal 1" : title,
		createdAt: terminal.createdAt,
	};
}

// Preview-only shell list. The browser build has no daemon to spawn a PTY, so
// open/close mutate this array instead — keeping the tab strip fully
// interactive (open, select, close) without a backend, which is what the e2e
// suite drives.
let previewShellTerminals: ShellTerminal[] = mockShellTerminals.map((shell) => ({
	...shell,
	host: LOCAL_HOST,
}));
let previewShellSeq = 0;

async function fetchShellTerminals(host: HostId): Promise<ShellTerminal[]> {
	if (usePreviewData) return previewShellTerminals.filter((shell) => shell.host === host);
	if (!isHostReady(host)) return [];
	const { data, error } = await clientFor(host).GET("/api/v1/shell-terminals");
	if (error) throw error;
	return (data?.shellTerminals ?? []).map((shell) => toShellTerminal(host, shell));
}

// No refetchInterval: shell terminals only change when this client opens or
// closes one, and both mutations invalidate the query.
export const shellTerminalsQueryOptions = (host: HostId) => ({
	queryKey: shellTerminalsQueryKey(host),
	queryFn: () => fetchShellTerminals(host),
	retry: 1,
});

export function useShellTerminals(host: HostId = LOCAL_HOST) {
	return useQuery(shellTerminalsQueryOptions(host));
}

export function useConnectedShellTerminals(): ShellTerminal[] {
	const remotes = useSyncExternalStore(subscribeConnectedHosts, connectedHosts, connectedHosts);
	return useQueries({
		queries: [LOCAL_HOST, ...remotes].map(shellTerminalsQueryOptions),
		combine: (results) => results.flatMap((result) => result.data ?? []),
	});
}

export type OpenShellTerminalInput = { project?: Ref; session?: Ref; shell?: string };
type OpenShellTerminalMutationInput = OpenShellTerminalInput & { optimisticShell?: ShellTerminal };
type OpenShellTerminalCallbacks = { onSuccess?: (shell: ShellTerminal) => void };

function targetFromInput({ project, session }: OpenShellTerminalInput) {
	if (project && session && project.host !== session.host) throw new Error("shell target hosts do not match");
	return {
		host: session?.host ?? project?.host ?? LOCAL_HOST,
		projectId: project?.id,
		sessionId: session?.id,
	};
}

function nextShellTerminalTitle(terminals: ShellTerminal[]): string {
	let maxNumber = 0;
	for (const terminal of terminals) {
		if (terminal.title === "Terminal") {
			maxNumber = Math.max(maxNumber, 1);
			continue;
		}
		const match = /^Terminal (\d+)$/.exec(terminal.title);
		if (match) maxNumber = Math.max(maxNumber, Number(match[1]));
	}
	return `Terminal ${maxNumber + 1}`;
}

function createOptimisticShellTerminal(
	input: OpenShellTerminalInput,
	terminals: ShellTerminal[],
): ShellTerminal {
	const { host, projectId, sessionId } = targetFromInput(input);
	const id = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`;
	return {
		host,
		handleId: `pending-shell:${id}`,
		projectId,
		sessionId,
		workingDir: "",
		title: nextShellTerminalTitle(terminals),
		createdAt: new Date().toISOString(),
		optimistic: true,
	};
}

function addOptimisticShell(queryClient: ReturnType<typeof useQueryClient>, shell: ShellTerminal) {
	queryClient.setQueryData<ShellTerminal[]>(shellTerminalsQueryKey(shell.host), (current) =>
		current?.some((candidate) => candidate.handleId === shell.handleId) ? current : [...(current ?? []), shell],
	);
}

/** Opens a shell in the requested project's root or the daemon data dir. */
export function useOpenShellTerminal() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (input: OpenShellTerminalMutationInput = {}): Promise<ShellTerminal> => {
			const { host, projectId, sessionId } = targetFromInput(input);
			if (usePreviewData) {
				previewShellSeq += 1;
				const shell: ShellTerminal = {
					host,
					handleId: `shellterm-preview-${previewShellSeq}`,
					projectId,
					sessionId,
					workingDir: `/Users/demo/Projects/${projectId ?? "ao"}`,
					title: input.optimisticShell?.title ?? `Terminal ${previewShellSeq}`,
					createdAt: new Date().toISOString(),
				};
				previewShellTerminals = [...previewShellTerminals, shell];
				return shell;
			}
			const body: { projectId?: string; sessionId?: string; shell?: string } = {};
			if (projectId) body.projectId = projectId;
			if (sessionId) body.sessionId = sessionId;
			if (isWindowsPlatform()) {
				await useTerminalShellStore.getState().load();
				body.shell = input.shell ?? terminalShellRequestValue(useTerminalShellStore.getState().preference);
			}
			const { data, error } = await clientFor(host).POST("/api/v1/shell-terminals", { body });
			if (error) throw error;
			if (!data) throw new Error("Daemon returned no shell terminal");
			return toShellTerminal(host, data.shellTerminal);
		},
		onMutate: (input) => {
			const { host } = targetFromInput(input);
			const queryKey = shellTerminalsQueryKey(host);
			const optimisticShell =
				input.optimisticShell ??
				createOptimisticShellTerminal(input, queryClient.getQueryData<ShellTerminal[]>(queryKey) ?? []);
			addOptimisticShell(queryClient, optimisticShell);
			return { optimisticHandleId: optimisticShell.handleId, queryKey };
		},
		onSuccess: (shell, _input, context) => {
			const queryKey = shellTerminalsQueryKey(shell.host);
			queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) => {
				const optimisticHandleId = context?.optimisticHandleId;
				const index = current?.findIndex((candidate) => candidate.handleId === optimisticHandleId) ?? -1;
				if (index >= 0) {
					return current?.map((candidate, candidateIndex) => (candidateIndex === index ? shell : candidate)) ?? [shell];
				}
				if (current?.some((candidate) => candidate.handleId === shell.handleId)) return current;
				return [...(current ?? []), shell];
			});
			void queryClient.invalidateQueries({ queryKey });
		},
		onError: (error, _input, context) => {
			if (context) {
				queryClient.setQueryData<ShellTerminal[]>(context.queryKey, (current) =>
					current?.filter((shell) => shell.handleId !== context.optimisticHandleId),
				);
			}
			console.error("Failed to open shell terminal:", error);
			if (isWindowsPlatform() && apiErrorCode(error) === "SHELL_TERMINAL_SHELL_UNAVAILABLE") {
				void useTerminalShellStore.getState().setPreference({ kind: "auto" });
			}
		},
		onSettled: (_data, _error, _input, context) => {
			if (context) void queryClient.invalidateQueries({ queryKey: context.queryKey });
		},
	});

	// Session topbars need the pending shell synchronously so they can select it
	// in the same click event. Other callers can keep using mutation.mutate.
	const open = (input: OpenShellTerminalInput = {}, callbacks?: OpenShellTerminalCallbacks) => {
		const { host } = targetFromInput(input);
		const optimisticShell = createOptimisticShellTerminal(
			input,
			queryClient.getQueryData<ShellTerminal[]>(shellTerminalsQueryKey(host)) ?? [],
		);
		addOptimisticShell(queryClient, optimisticShell);
		mutation.mutate({ ...input, optimisticShell }, callbacks);
		return optimisticShell;
	};

	return { ...mutation, open };
}

/** Closes a shell and destroys its PTY. */
export function useCloseShellTerminal() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async (terminal: Ref): Promise<void> => {
			if (usePreviewData) {
				previewShellTerminals = previewShellTerminals.filter(
					(shell) => refKey({ host: shell.host, id: shell.handleId }) !== refKey(terminal),
				);
				return;
			}
			const { error } = await clientFor(terminal.host).DELETE("/api/v1/shell-terminals/{handleId}", {
				params: { path: { handleId: terminal.id } },
			});
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
			removeClosedShell();
			await queryClient.cancelQueries({ queryKey });
			removeClosedShell();
			return { previous, queryKey };
		},
		onError: (error, _terminal, context) => {
			if (apiErrorCode(error) !== "SHELL_TERMINAL_NOT_FOUND" && context?.previous) {
				queryClient.setQueryData(context.queryKey, context.previous);
			}
		},
		onSettled: (_data, _error, terminal) => {
			void queryClient.invalidateQueries({ queryKey: shellTerminalsQueryKey(terminal.host) });
		},
	});
}

export type RenameShellTerminalInput = { terminal: Ref; title: string };

/** Renames a shell terminal's tab. The new title persists on the daemon. */
export function useRenameShellTerminal() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async ({ terminal, title }: RenameShellTerminalInput): Promise<ShellTerminal> => {
			if (usePreviewData) {
				previewShellTerminals = previewShellTerminals.map((shell) =>
					refKey({ host: shell.host, id: shell.handleId }) === refKey(terminal) ? { ...shell, title } : shell,
				);
				const shell = previewShellTerminals.find(
					(candidate) => refKey({ host: candidate.host, id: candidate.handleId }) === refKey(terminal),
				);
				if (!shell) throw new Error("No such shell terminal");
				return shell;
			}
			const { data, error } = await clientFor(terminal.host).PATCH("/api/v1/shell-terminals/{handleId}", {
				params: { path: { handleId: terminal.id } },
				body: { title },
			});
			if (error) throw error;
			if (!data) throw new Error("Daemon returned no shell terminal");
			return toShellTerminal(terminal.host, data.shellTerminal);
		},
		onMutate: async ({ terminal, title }) => {
			const queryKey = shellTerminalsQueryKey(terminal.host);
			await queryClient.cancelQueries({ queryKey });
			const previous = queryClient.getQueryData<ShellTerminal[]>(queryKey);
			queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) =>
				current?.map((shell) => (shell.handleId === terminal.id ? { ...shell, title } : shell)),
			);
			return { previous, queryKey };
		},
		onError: (_error, _input, context) => {
			if (context?.previous) queryClient.setQueryData(context.queryKey, context.previous);
		},
		onSuccess: (shell) => {
			const queryKey = shellTerminalsQueryKey(shell.host);
			queryClient.setQueryData<ShellTerminal[]>(queryKey, (current) =>
				current?.map((candidate) => (candidate.handleId === shell.handleId ? shell : candidate)),
			);
			void queryClient.invalidateQueries({ queryKey });
		},
	});
}
