import { keepPreviousData, useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo, useSyncExternalStore } from "react";
import {
	answerDecisionInboxCall,
	DECISION_SEARCH_LIMIT,
	decisionKeys,
	fetchDecisionBoard,
	fetchDecisionDetail,
	fetchDecisionInboxCalls,
	fetchDecisionSearch,
	mergeDecisionInboxSources,
	runDecisionAction,
	type DecisionAction,
	type DecisionBoard,
	type DecisionCall,
	type DecisionDetail,
	type DecisionInboxAnswer,
	type DecisionInboxEntry,
} from "../lib/decision-inbox";
import {
	decisionAnswerGuard,
	DecisionAnswerHeldError,
	decisionViewMeta,
	detailView,
	guardedDecisionRead,
	needsYouView,
	type DecisionAnswerHold,
} from "../lib/decision-answer-guard";
import { labelForHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import { useConnectedHosts } from "./useHostConnection";

export const decisionInboxQueryKey = (hostId: HostId) => decisionKeys.needsYou(hostId);

/**
 * The one answer mutation for every decision surface (bell and page). On
 * success the answered call leaves the needs-you list at once and every
 * decision view refetches; on failure the source host refetches, so a call
 * answered elsewhere drops out instead of being sent again.
 */
export function useDecisionAnswer() {
	const queryClient = useQueryClient();
	const guard = decisionAnswerGuard(queryClient);
	return useMutation({
		// A call with an answer in flight, or one whose fate is unknown, is never
		// sent again from any surface until a later read settles it.
		// `view` is the surface sending (its cached copy of the call); without
		// one, any unconfirmed answer counts as held.
		mutationFn: async ({ view = "", ...input }: DecisionInboxAnswer & { view?: string }) => {
			const hold = guard.state(input.sourceHostId, input.callId, view);
			if (hold) throw new DecisionAnswerHeldError(hold);
			const idempotencyKey = guard.begin(input.sourceHostId, input.callId);
			try {
				const confirmed = await answerDecisionInboxCall({ ...input, idempotencyKey });
				guard.settle(input.sourceHostId, input.callId, true);
				return confirmed;
			} catch (error) {
				guard.settle(input.sourceHostId, input.callId, false);
				throw error;
			}
		},
		onSuccess: async (confirmed, input) => {
			queryClient.setQueryData<DecisionCall[]>(decisionInboxQueryKey(input.sourceHostId), (current) =>
				current?.filter((call) => call.id !== input.callId),
			);
			if (input.sourceHostId === LOCAL_HOST) seedConfirmedCall(queryClient, confirmed);
			await queryClient.invalidateQueries({ queryKey: decisionKeys.all });
		},
		// The daemon may have committed the answer even though the response was
		// lost: stay pending until the source host's calls are re-read, so the
		// same answer is not sent twice.
		onError: (_error, input) => queryClient.invalidateQueries({ queryKey: decisionKeys.host(input.sourceHostId) }),
	});
}

/**
 * Dismiss, record an outcome, or mark a call (no longer) applying, on this
 * computer's ledger. Success or failure refetches this computer's decision
 * views before anything is sent again; nothing changes before the daemon says so.
 */
export function useDecisionAction() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (action: DecisionAction) => runDecisionAction(action),
		onSuccess: (confirmed) => seedConfirmedCall(queryClient, confirmed),
		onSettled: () => queryClient.invalidateQueries({ queryKey: decisionKeys.host(LOCAL_HOST) }),
	});
}

/**
 * Puts the daemon's confirmed call into this computer's detail and board
 * views at once. A re-read that then fails keeps it, so a stale call is never
 * shown as still open to the same action.
 */
function seedConfirmedCall(queryClient: ReturnType<typeof useQueryClient>, confirmed: DecisionCall) {
	queryClient.setQueryData<DecisionDetail | null>(decisionKeys.detail(confirmed.id), (current) =>
		current ? { ...current, call: confirmed } : current,
	);
	queryClient.setQueryData<DecisionBoard>(decisionKeys.board(), (current) => {
		const before = current?.calls.find((call) => call.id === confirmed.id);
		if (!current || !before) return current;
		// Keep the board's own window and count true to the confirmed call.
		const stays = confirmed.state === "needs_you" || confirmed.state === "in_progress" || confirmed.state === "decided" ||
			(confirmed.state === "done" && !confirmed.retired && Date.parse(confirmed.outcomeAt ?? "") >= Date.parse(current.doneSince));
		const needsYou = (call: DecisionCall) => (call.state === "needs_you" ? 1 : 0);
		return {
			...current,
			calls: current.calls.flatMap((call) => (call.id !== confirmed.id ? [call] : stays ? [confirmed] : [])),
			needsYouCount: current.needsYouCount - needsYou(before) + (stays ? needsYou(confirmed) : 0),
		};
	});
}

export function useDecisionInbox(enabled: boolean) {
	const connectedHosts = useConnectedHosts();
	const guard = decisionAnswerGuard(useQueryClient());
	const localQuery = useQuery({
		queryKey: decisionInboxQueryKey(LOCAL_HOST),
		queryFn: ({ signal }) => guardedDecisionRead(guard, needsYouView(LOCAL_HOST), () => fetchDecisionInboxCalls(LOCAL_HOST, signal)),
		meta: decisionViewMeta(needsYouView(LOCAL_HOST)),
		enabled,
		retry: 1,
		refetchInterval: enabled ? 2_000 : false,
	});
	const remoteQueries = useQueries({
		queries: connectedHosts.map((hostId) => ({
			queryKey: decisionInboxQueryKey(hostId),
			queryFn: ({ signal }: { signal: AbortSignal }) => guardedDecisionRead(guard, needsYouView(hostId), () => fetchDecisionInboxCalls(hostId, signal)),
			meta: decisionViewMeta(needsYouView(hostId)),
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

	const answerMutation = useDecisionAnswer();
	const guardVersion = useSyncExternalStore(guard.subscribe, guard.snapshot);
	// `${hostId}:${callId}` → why that call's answer cannot be sent right now.
	const held = useMemo(() => {
		void guardVersion;
		const holds = new Map<string, DecisionAnswerHold>();
		for (const { sourceHostId, call } of entries) {
			const hold = guard.state(sourceHostId, call.id, needsYouView(sourceHostId));
			if (hold) holds.set(`${sourceHostId}:${call.id}`, hold);
		}
		return holds;
	}, [entries, guard, guardVersion]);

	const answer = useCallback((entry: DecisionInboxEntry, option: number, note?: string) => {
		const view = needsYouView(entry.sourceHostId);
		if (guard.state(entry.sourceHostId, entry.call.id, view)) return;
		answerMutation.mutate({
			view,
			sourceHostId: entry.sourceHostId,
			callId: entry.call.id,
			option,
			note,
		});
	}, [answerMutation.mutate, guard]);
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
		held,
		answer,
		retry,
	};
}

/**
 * This computer's Decisions board. The sidebar count and the page share this
 * one query; live `decision_updated` events refresh it through the local host
 * branch, so it does not poll.
 */
/** Why the call's answer cannot be sent right now, shared across every surface. */
export function useDecisionAnswerHold(hostId: HostId, callId: string, view: string): DecisionAnswerHold | undefined {
	const guard = decisionAnswerGuard(useQueryClient());
	useSyncExternalStore(guard.subscribe, guard.snapshot);
	return guard.state(hostId, callId, view);
}

export function useDecisionBoard() {
	return useQuery({
		queryKey: decisionKeys.board(),
		queryFn: ({ signal }) => fetchDecisionBoard(signal),
		retry: 1,
		// No event fires when a Done card ages out of the seven-day window, so
		// refetch at the next such moment.
		refetchInterval: (query) => nextDoneExpiryDelay(query.state.data),
	});
}

const DONE_WINDOW_MS = 7 * 24 * 60 * 60 * 1000;
const MAX_TIMER_MS = 24 * 60 * 60 * 1000;

/** Milliseconds until the oldest board Done card passes the window, or false. */
export function nextDoneExpiryDelay(board: DecisionBoard | undefined, now = Date.now()): number | false {
	let soonest = Number.POSITIVE_INFINITY;
	for (const call of board?.calls ?? []) {
		if (call.state !== "done" || call.retired || !call.outcomeAt) continue;
		const expires = Date.parse(call.outcomeAt) + DONE_WINDOW_MS;
		if (Number.isFinite(expires)) soonest = Math.min(soonest, expires);
	}
	if (!Number.isFinite(soonest)) return false;
	const delay = soonest - now;
	// Past due means the daemon's clock has not dropped it yet; look again in a minute.
	if (delay <= 0) return 60_000;
	return Math.min(MAX_TIMER_MS, Math.max(1_000, delay + 250));
}

/** One call and its full history; `null` data means it no longer exists. */
export function useDecisionDetail(callId: string | undefined) {
	const guard = decisionAnswerGuard(useQueryClient());
	return useQuery({
		queryKey: decisionKeys.detail(callId ?? ""),
		queryFn: ({ signal }) => guardedDecisionRead(guard, detailView(callId ?? ""), () => fetchDecisionDetail(callId!, signal)),
		meta: decisionViewMeta(detailView(callId ?? "")),
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
