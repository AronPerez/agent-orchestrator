import { keepPreviousData, useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo } from "react";
import {
	answerDecisionInboxCall,
	DECISION_SEARCH_LIMIT,
	decisionKeys,
	fetchDecisionBoard,
	fetchDecisionDetail,
	fetchDecisionInboxCalls,
	fetchDecisionSearch,
	mergeDecisionInboxSources,
	type DecisionCall,
	type DecisionInboxEntry,
} from "../lib/decision-inbox";
import { labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { useConnectedHosts } from "./useHostConnection";

export const decisionInboxQueryKey = (hostId: HostId) => decisionKeys.needsYou(hostId);

export function useDecisionInbox(enabled: boolean) {
	const queryClient = useQueryClient();
	const connectedHosts = useConnectedHosts();
	const localQuery = useQuery({
		queryKey: decisionInboxQueryKey(LOCAL_HOST),
		queryFn: ({ signal }) => fetchDecisionInboxCalls(LOCAL_HOST, signal),
		enabled,
		retry: 1,
		refetchInterval: enabled ? 2_000 : false,
	});
	const remoteQueries = useQueries({
		queries: connectedHosts.map((hostId) => ({
			queryKey: decisionInboxQueryKey(hostId),
			queryFn: ({ signal }: { signal: AbortSignal }) => fetchDecisionInboxCalls(hostId, signal),
			enabled,
			retry: 1,
			refetchInterval: enabled ? 2_000 : false,
		})),
	});
	const entries = useMemo(() => mergeDecisionInboxSources([
		{ hostId: LOCAL_HOST, label: "", calls: localQuery.data ?? [] },
		...connectedHosts.map((hostId, index) => ({
			hostId,
			label: labelForHost(hostId) ?? hostId,
			calls: remoteQueries[index]?.data ?? [],
		})),
	]), [connectedHosts, localQuery.data, remoteQueries]);

	const answerMutation = useMutation({
		mutationFn: answerDecisionInboxCall,
		onSuccess: async (_call, input) => {
			queryClient.setQueryData<DecisionCall[]>(decisionInboxQueryKey(input.sourceHostId), (current) =>
				current?.filter((call) => call.id !== input.callId),
			);
			await queryClient.invalidateQueries({ queryKey: decisionKeys.all });
		},
		onError: (_error, input) => {
			void queryClient.invalidateQueries({ queryKey: decisionInboxQueryKey(input.sourceHostId) });
		},
	});

	const answer = useCallback((entry: DecisionInboxEntry, option: number, note?: string) => {
		answerMutation.mutate({
			sourceHostId: entry.sourceHostId,
			callId: entry.call.id,
			option,
			note,
		});
	}, [answerMutation.mutate]);
	const retry = useCallback(() => {
		void localQuery.refetch();
		for (const query of remoteQueries) void query.refetch();
	}, [localQuery, remoteQueries]);

	return {
		entries,
		isLoading: localQuery.isLoading || remoteQueries.some((query) => query.isLoading),
		loadFailed: localQuery.isError || remoteQueries.some((query) => query.isError),
		pending: answerMutation.isPending ? answerMutation.variables : undefined,
		answerError: answerMutation.isError && answerMutation.variables ? {
			sourceHostId: answerMutation.variables.sourceHostId,
			callId: answerMutation.variables.callId,
			message: answerMutation.error instanceof Error ? answerMutation.error.message : "Could not send decision",
		} : undefined,
		answer,
		retry,
	};
}

/**
 * This computer's Decisions board. The sidebar count and the page share this
 * one query; live `decision_updated` events refresh it through the local host
 * branch, so it does not poll.
 */
export function useDecisionBoard() {
	return useQuery({
		queryKey: decisionKeys.board(),
		queryFn: ({ signal }) => fetchDecisionBoard(signal),
		retry: 1,
	});
}

/** One call and its full history; `null` data means it no longer exists. */
export function useDecisionDetail(callId: string | undefined) {
	return useQuery({
		queryKey: decisionKeys.detail(callId ?? ""),
		queryFn: ({ signal }) => fetchDecisionDetail(callId!, signal),
		enabled: Boolean(callId),
		retry: 1,
	});
}

/** Full-ledger search. Earlier results stay visible while a new query loads. */
export function useDecisionSearch(query: string) {
	const words = query.trim();
	return useQuery({
		queryKey: decisionKeys.search(words, DECISION_SEARCH_LIMIT),
		queryFn: ({ signal }) => fetchDecisionSearch(words, DECISION_SEARCH_LIMIT, signal),
		enabled: words.length > 0,
		placeholderData: keepPreviousData,
		retry: 1,
	});
}
