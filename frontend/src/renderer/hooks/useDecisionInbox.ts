import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo } from "react";
import {
	answerDecisionInboxCall,
	fetchDecisionInboxCalls,
	mergeDecisionInboxSources,
	type DecisionCall,
	type DecisionInboxEntry,
} from "../lib/decision-inbox";
import { labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { useConnectedHosts } from "./useHostConnection";

export const decisionInboxQueryKey = (hostId: HostId) => ["decision-inbox", hostId] as const;

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
		onSuccess: (_call, input) => {
			queryClient.setQueryData<DecisionCall[]>(decisionInboxQueryKey(input.sourceHostId), (current) =>
				current?.filter((call) => call.id !== input.callId),
			);
			void queryClient.invalidateQueries({ queryKey: decisionInboxQueryKey(input.sourceHostId), exact: true });
		},
		onError: (_error, input) => {
			void queryClient.invalidateQueries({ queryKey: decisionInboxQueryKey(input.sourceHostId), exact: true });
		},
	});

	const answer = useCallback((entry: DecisionInboxEntry, option: number) => {
		answerMutation.mutate({
			sourceHostId: entry.sourceHostId,
			callId: entry.call.id,
			option,
		});
	}, [answerMutation.mutate]);
	const retry = useCallback(() => {
		void localQuery.refetch();
		for (const query of remoteQueries) void query.refetch();
	}, [localQuery, remoteQueries]);
	const retryAnswer = useCallback(() => {
		if (answerMutation.variables) answerMutation.mutate(answerMutation.variables);
	}, [answerMutation.mutate, answerMutation.variables]);
	const clearAnswerError = useCallback(() => answerMutation.reset(), [answerMutation.reset]);

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
		retryAnswer,
		clearAnswerError,
	};
}
