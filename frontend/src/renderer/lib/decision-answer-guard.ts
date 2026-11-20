import type { QueryClient } from "@tanstack/react-query";

/**
 * Why a call's answer cannot be sent right now: one is in flight, or one
 * failed in a way that may still have been recorded (the daemon can commit an
 * answer whose response is lost).
 */
export type DecisionAnswerHold = "sending" | "unconfirmed";

/** `key` names the person's current answer attempt for the daemon's idempotency check. */
type Entry = { sending: number; failedAt?: number; key: string };

/**
 * One record per host and call of answers whose result is unknown, shared by
 * every decision surface on a query client (the bell and the Decisions page),
 * so closing a panel or switching surfaces never forgets it.
 *
 * Each surface acts on its own cached copy of a call, so a failed answer is
 * released per *view*: only once that view has applied a read that *started*
 * after the failure. Another view's fresh read does not refresh this one's
 * cache, and a read already under way may have looked before the answer was
 * committed.
 */
export class DecisionAnswerGuard {
	private sequence = 0;
	private readonly entries = new Map<string, Entry>();
	/** Per view: when its latest successful read started. */
	private readonly viewReads = new Map<string, number>();
	/** Per view: when its most recent fetch started (the one React Query would apply). */
	private readonly fetchStarts = new Map<string, number>();
	private readonly listeners = new Set<() => void>();
	private version = 0;

	/** Why `view` must not send an answer to this call right now, if anything. */
	state(hostId: string, callId: string, view: string): DecisionAnswerHold | undefined {
		const entry = this.entries.get(key(hostId, callId));
		if (!entry) return undefined;
		if (entry.sending > 0) return "sending";
		if (entry.failedAt !== undefined && (this.viewReads.get(view) ?? 0) <= entry.failedAt) return "unconfirmed";
		return undefined;
	}

	/**
	 * An answer for the call is about to be sent. Returns the idempotency key to
	 * send with it: the same key as an earlier send whose result was not seen,
	 * so the daemon returns that answer instead of recording a second one.
	 */
	begin(hostId: string, callId: string): string {
		const id = key(hostId, callId);
		const entry = this.entries.get(id) ?? { sending: 0, key: newAttemptKey() };
		this.entries.set(id, { ...entry, sending: entry.sending + 1 });
		this.changed();
		return entry.key;
	}

	/** The answer settled: confirmed by the daemon, or failed with its fate unknown. */
	settle(hostId: string, callId: string, confirmed: boolean): void {
		const id = key(hostId, callId);
		const entry = this.entries.get(id);
		if (!entry) return;
		const sending = Math.max(0, entry.sending - 1);
		// A confirmed answer says what the daemon recorded, even after an earlier failure.
		if (confirmed && sending === 0) this.entries.delete(id);
		else this.entries.set(id, { ...entry, sending, failedAt: confirmed ? entry.failedAt : ++this.sequence });
		this.changed();
	}

	/** Call when a decision read is sent; pass the result to viewRead. */
	readStarted(): number {
		return ++this.sequence;
	}

	/** A fetch for `view` is being sent. */
	fetchStarted(view: string): void {
		this.fetchStarts.set(view, this.readStarted());
	}

	/**
	 * React Query installed a fetched result for `view`. It applies only the
	 * result of the latest fetch it started (a cancelled one is discarded), so
	 * that fetch's start is what the view now reflects.
	 */
	fetchApplied(view: string): void {
		const startedAt = this.fetchStarts.get(view);
		if (startedAt !== undefined) this.viewRead(view, startedAt);
	}

	/** `view` successfully applied a read that started at `startedAt`. */
	viewRead(view: string, startedAt: number): void {
		if (startedAt <= (this.viewReads.get(view) ?? 0)) return;
		this.viewReads.set(view, startedAt);
		this.changed();
	}

	subscribe = (listener: () => void): (() => void) => {
		this.listeners.add(listener);
		return () => this.listeners.delete(listener);
	};

	/** Changes whenever any hold starts or ends; for useSyncExternalStore. */
	snapshot = (): number => this.version;

	private changed(): void {
		this.version += 1;
		for (const listener of this.listeners) listener();
	}
}

/** Query `meta` naming the answer-bearing view a decision query feeds. */
export const decisionViewMeta = (view: string) => ({ decisionAnswerView: view });

/** The bell's needs-you list for one machine. */
export const needsYouView = (hostId: string) => `needs-you:${hostId}`;
/** This computer's detail for one call (the Decisions panel). */
export const detailView = (callId: string) => `detail:${callId}`;

/**
 * The query function wrapper for an answer-bearing view: records when the
 * fetch started. The view advances only when React Query applies the result
 * (see decisionAnswerGuard), never from this continuation, so a cancelled or
 * superseded fetch whose response arrives late releases nothing.
 */
export function guardedDecisionRead<T>(guard: DecisionAnswerGuard, view: string, read: () => Promise<T>): Promise<T> {
	guard.fetchStarted(view);
	return read();
}

/** A refused send: an earlier answer for the call is in flight or unconfirmed. */
export class DecisionAnswerHeldError extends Error {
	constructor(readonly hold: DecisionAnswerHold) {
		super("An earlier answer to this call has not been confirmed yet");
		this.name = "DecisionAnswerHeldError";
	}
}

const guards = new WeakMap<QueryClient, DecisionAnswerGuard>();

/** The guard shared by every decision surface on this query client. */
export function decisionAnswerGuard(queryClient: QueryClient): DecisionAnswerGuard {
	let guard = guards.get(queryClient);
	if (!guard) {
		const created = new DecisionAnswerGuard();
		// A fetched result counts once React Query installs it. Manual cache
		// writes (setQueryData) are not reads of the daemon.
		queryClient.getQueryCache().subscribe((event) => {
			if (event.type !== "updated" || event.action.type !== "success" || event.action.manual) return;
			const view = event.query.meta?.decisionAnswerView;
			if (typeof view === "string") created.fetchApplied(view);
		});
		guards.set(queryClient, created);
		guard = created;
	}
	return guard;
}

const newAttemptKey = () => `ans_${crypto.randomUUID()}`;

// JSON keeps host and call ids apart whatever characters they contain.
const key = (hostId: string, callId: string) => JSON.stringify([hostId, callId]);
