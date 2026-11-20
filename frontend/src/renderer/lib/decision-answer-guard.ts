import type { QueryClient } from "@tanstack/react-query";
import { LOCAL_HOST } from "./hosts";

/**
 * Why a call's answer cannot be sent right now: one is in flight, or one
 * failed in a way that may still have been recorded (the daemon can commit an
 * answer whose response is lost).
 */
export type DecisionAnswerHold = "sending" | "unconfirmed";

type Entry = {
	/**
	 * Names the person's current answer attempt for the daemon's idempotency
	 * check. Kept across sends whose result was not seen, so a retry is
	 * recognised; replaced once the source shows the lost answer was recorded,
	 * so a deliberate change is not mistaken for that retry.
	 */
	key: string;
	/** SHA-256 (hex) of `key`, as the daemon reports it on the standing answer. */
	keyDigest?: string;
	/** A read showed the standing answer was sent with this key: the attempt landed. */
	recorded?: boolean;
	/** The machine the latest attempt was sent to: its reads settle what happened. */
	source: string;
	sending: number;
	failedAt?: number;
	/** What the source's own fresh read showed: whether the call still waits. */
	sourceWaiting?: boolean;
	/** Per view, whether its own fresh read still showed the call waiting. */
	observed: Map<string, boolean>;
};

/**
 * One record per decision call of answers whose result is unknown, shared by
 * every decision surface on a query client (the bell and the Decisions page),
 * so closing a panel or switching surfaces never forgets it. Calls are keyed
 * by their stable id, which decision sync keeps across machines: a copy of
 * the call on another machine is held too.
 *
 * Only reads that React Query applied, and that started after the failure,
 * count. If the machine the answer went to shows the call still waiting, the
 * answer was not recorded anywhere and every view is released. If it shows
 * the call has moved on, the answer was recorded: each view stays held until
 * its own fresh data shows that too (a synced copy can lag behind).
 */
export class DecisionAnswerGuard {
	private sequence = 0;
	private readonly entries = new Map<string, Entry>();
	/** Per view: when its most recent fetch started (the one React Query would apply). */
	private readonly fetchStarts = new Map<string, number>();
	private readonly listeners = new Set<() => void>();
	private version = 0;

	/** Why `view` must not send an answer to this call right now, if anything. */
	state(callId: string, view: string): DecisionAnswerHold | undefined {
		const entry = this.entries.get(callId);
		if (!entry) return undefined;
		if (entry.sending > 0) return "sending";
		if (entry.failedAt === undefined || entry.sourceWaiting === true) return undefined;
		if (entry.sourceWaiting === false && entry.observed.get(view) === false) return undefined;
		return "unconfirmed";
	}

	/**
	 * An answer to the call is about to be sent to `source`. Returns the
	 * idempotency key to send with it: the earlier attempt's key while its fate
	 * is unknown or it was shown unrecorded, a new one once it was shown recorded.
	 */
	begin(source: string, callId: string): string {
		const entry = this.entries.get(callId);
		// Only proof that this attempt itself was recorded makes the next send a
		// deliberate change. The call's state cannot say so: changing an answer,
		// or answering a dismissed call, leaves it in progress either way.
		const reuse = entry !== undefined && !entry.recorded;
		const key = reuse ? entry.key : newAttemptKey();
		this.entries.set(callId, {
			...entry,
			key,
			keyDigest: reuse ? entry.keyDigest : undefined,
			recorded: false,
			source,
			sending: (entry?.sending ?? 0) + 1,
			// A new attempt starts a new account of what happened to it.
			sourceWaiting: undefined,
			observed: new Map(),
		});
		this.changed();
		return key;
	}

	/** Records the digest of the key an attempt was sent with. */
	attemptDigest(callId: string, key: string, digest: string): void {
		const entry = this.entries.get(callId);
		if (entry?.key === key) entry.keyDigest = digest;
	}

	/** The answer settled: confirmed by the daemon, or failed with its fate unknown. */
	settle(callId: string, confirmed: boolean): void {
		const entry = this.entries.get(callId);
		if (!entry) return;
		const sending = Math.max(0, entry.sending - 1);
		// A confirmed answer says what the daemon recorded, even after an earlier failure.
		if (confirmed && sending === 0) this.entries.delete(callId);
		else this.entries.set(callId, { ...entry, sending, failedAt: confirmed ? entry.failedAt : ++this.sequence });
		this.changed();
	}

	/** A fetch for `view` is being sent. */
	fetchStarted(view: string): void {
		this.fetchStarts.set(view, ++this.sequence);
	}

	/**
	 * React Query installed `data` as `view`'s result. It applies only the
	 * result of the latest fetch it started (a cancelled one is discarded), so
	 * that fetch's start says whether the read can know about a failure.
	 */
	fetchApplied(view: string, data: unknown): void {
		const startedAt = this.fetchStarts.get(view);
		const scope = viewScope(view);
		if (startedAt === undefined || !scope) return;
		let changed = false;
		for (const [callId, entry] of this.entries) {
			if (entry.sending > 0 || entry.failedAt === undefined || startedAt <= entry.failedAt) continue;
			if (scope.callId !== undefined && scope.callId !== callId) continue;
			if (entry.keyDigest !== undefined && answerKeyDigestIn(scope, callId, data) === entry.keyDigest) entry.recorded = true;
			const stillWaiting = waitingIn(scope, callId, data);
			entry.observed.set(view, stillWaiting);
			if (scope.hostId === entry.source) entry.sourceWaiting = stillWaiting;
			changed = true;
		}
		if (changed) this.changed();
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

const newAttemptKey = () => `ans_${crypto.randomUUID()}`;

const NEEDS_YOU = "needs-you:";
const DETAIL = "detail:";

/** The bell's needs-you list for one machine. */
export const needsYouView = (hostId: string) => `${NEEDS_YOU}${hostId}`;
/** This computer's detail for one call (the Decisions panel). */
export const detailView = (callId: string) => `${DETAIL}${callId}`;

/** The machine, and for a detail the one call, a view's data describes. */
function viewScope(view: string): { hostId: string; callId?: string } | undefined {
	if (view.startsWith(NEEDS_YOU)) return { hostId: view.slice(NEEDS_YOU.length) };
	if (view.startsWith(DETAIL)) return { hostId: LOCAL_HOST, callId: view.slice(DETAIL.length) };
	return undefined;
}

/** The idempotency-key digest of the call's standing answer, as the view's data shows it. */
function answerKeyDigestIn(scope: { callId?: string }, callId: string, data: unknown): unknown {
	const call = scope.callId === undefined
		? (Array.isArray(data) ? data.find((item) => (item as { id?: unknown } | null)?.id === callId) : undefined)
		: (data as { call?: unknown } | null | undefined)?.call;
	return (call as { answerKeyDigest?: unknown } | undefined)?.answerKeyDigest;
}

/** SHA-256 (hex) of an attempt key: what the daemon stores and reports in its place. */
export async function attemptKeyDigest(key: string): Promise<string> {
	const sum = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(key));
	return [...new Uint8Array(sum)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}

/** Whether a view's freshly applied data still shows the call in Needs you. */
function waitingIn(scope: { callId?: string }, callId: string, data: unknown): boolean {
	if (scope.callId === undefined) {
		// A needs-you list: the call is waiting exactly when it is listed.
		return Array.isArray(data) && data.some((call) => (call as { id?: unknown } | null)?.id === callId);
	}
	return (data as { call?: { state?: unknown } } | null | undefined)?.call?.state === "needs_you";
}

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
			if (typeof view === "string") created.fetchApplied(view, event.action.data);
		});
		guards.set(queryClient, created);
		guard = created;
	}
	return guard;
}

