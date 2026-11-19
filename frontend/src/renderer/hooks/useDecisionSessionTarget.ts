import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useSyncExternalStore } from "react";
import { resolveDecisionSessionTarget, type DecisionSessionTarget, type DecisionWorkspaceSource } from "../lib/decision-session-target";
import type { DecisionCall } from "../lib/decision-inbox";
import type { WorkspaceSummary } from "../types/workspace";
import { useDecisionBoard } from "./useDecisionInbox";
import { useConnectedHosts } from "./useHostConnection";
import { remoteWorkspaceQueryKey, useWorkspaceQuery } from "./useWorkspaceQuery";

/** Resolves the call's owning session on its own machine, as the workspaces load. */
export function useDecisionSessionTarget(call: DecisionCall): DecisionSessionTarget {
	const servingHostId = useDecisionBoard().data?.servingHostId;
	const connectedHosts = useConnectedHosts();
	const localQuery = useWorkspaceQuery({ includeCloud: false });
	const local: DecisionWorkspaceSource = {
		status: localQuery.isSuccess ? "ready" : localQuery.isError ? "error" : "loading",
		workspaces: localQuery.data ?? [],
	};
	// The connected host's own workspace list, read from the cache its
	// sidebar section keeps current. Read every render, whichever host the
	// call turns out to belong to, so the hook order never changes.
	const queryClient = useQueryClient();
	const subscribe = useCallback((notify: () => void) => queryClient.getQueryCache().subscribe(notify), [queryClient]);
	const remoteState = useSyncExternalStore(subscribe, () => queryClient.getQueryState<WorkspaceSummary[]>(remoteWorkspaceQueryKey(call.hostId)));
	const remote: DecisionWorkspaceSource = {
		status: remoteState?.status === "success" ? "ready" : remoteState?.status === "error" ? "error" : "loading",
		workspaces: remoteState?.data ?? [],
	};
	return resolveDecisionSessionTarget({ call, servingHostId, connectedHosts, local, remote });
}
