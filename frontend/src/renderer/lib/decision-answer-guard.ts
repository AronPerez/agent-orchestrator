import type { QueryClient } from "@tanstack/react-query";

/**
 * Why a call's answer cannot be sent right now: one is in flight, or one
 * failed in a way that may still have been recorded (the daemon can commit an
 * answer whose response is lost).
 */
export type DecisionAnswerHold = "sending" | "unconfirmed";

type Entry = { sending: number; failedAt?: number };

/**
 * One record per host and call of answers whose result is unknown, shared by
 * every decision surface on a query client (the bell and the Decisions page),
 * so closing a panel or switching surfaces never forgets it. A failed answer
 * is released only by a read that *started* after the failure: a read already
 * under way may have looked before the answer was committed.
 */
export class DecisionAnswerGuard {
	private sequence = 0;
	private readonly entries = new Map<string, Entry>();
	private readonly listeners = new Set<() => void>();
	private version = 0;

	state(hostId: string, callId: string): DecisionAnswerHold | undefined {
		const entry = this.entries.get(key(hostId, callId));
		if (!entry) return undefined;
		return entry.sending > 0 ? "sending" : "unconfirmed";
	}

	/** An answer for the call is about to be sent. */
	begin(hostId: string, callId: string): void {
		const id = key(hostId, callId);
		const entry = this.entries.get(id) ?? { sending: 0 };
		this.entries.set(id, { ...entry, sending: entry.sending + 1 });
		this.changed();
	}

	/** The answer settled: confirmed by the daemon, or failed with its fate unknown. */
	settle(hostId: string, callId: string, confirmed: boolean): void {
		const id = key(hostId, callId);
		const entry = this.entries.get(id);
		if (!entry) return;
		const sending = Math.max(0, entry.sending - 1);
		// A confirmed answer says what the daemon recorded, even after an earlier failure.
		if (confirmed && sending === 0) this.entries.delete(id);
		else this.entries.set(id, { sending, failedAt: confirmed ? entry.failedAt : ++this.sequence });
		this.changed();
	}

	/** Call when a decision read is sent; pass the result to readSucceeded. */
	readStarted(): number {
		return ++this.sequence;
	}

	/**
	 * A read of the host (or of one call on it) that started at `startedAt`
	 * succeeded, so it shows what the daemon recorded for answers that failed
	 * before it started. Answers still in flight stay held.
	 */
	readSucceeded(hostId: string, startedAt: number, callId?: string): void {
		let freed = false;
		for (const [id, entry] of this.entries) {
			const [entryHost, entryCall] = split(id);
			if (entryHost !== hostId || (callId !== undefined && entryCall !== callId)) continue;
			if (entry.sending > 0 || entry.failedAt === undefined || startedAt <= entry.failedAt) continue;
			this.entries.delete(id);
			freed = true;
		}
		if (freed) this.changed();
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

/**
 * Runs a decision read and, if it succeeds, releases the answers on that host
 * (or that one call) that failed before the read started.
 */
export async function guardedDecisionRead<T>(
	guard: DecisionAnswerGuard,
	hostId: string,
	callId: string | undefined,
	read: () => Promise<T>,
): Promise<T> {
	const startedAt = guard.readStarted();
	const result = await read();
	guard.readSucceeded(hostId, startedAt, callId);
	return result;
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
		guard = new DecisionAnswerGuard();
		guards.set(queryClient, guard);
	}
	return guard;
}

// JSON keeps host and call ids apart whatever characters they contain.
const key = (hostId: string, callId: string) => JSON.stringify([hostId, callId]);
const split = (id: string) => JSON.parse(id) as [string, string];
