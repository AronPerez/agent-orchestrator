import type { PendingSideClose } from "../shared/side-close";

interface RecoveryRecord {
	side: { id: string; sessionId: string; label?: string; [key: string]: unknown };
	providerHostId?: string;
	providerForkId?: string;
	harness?: string;
	cleanupOnly?: boolean;
	hostStopped?: boolean;
}
export interface SideLaunchSnapshot {
	appRunId: string;
	sides: unknown[];
	numbers?: Record<string, number>;
}

export function cleanupSideRecord(original: RecoveryRecord): RecoveryRecord {
	return {
		side: Object.fromEntries(Object.entries(original.side).filter(([key]) => ["id", "sessionId", "mainConversationId", "label", "state", "contextMode", "hasWork", "createdAt", "updatedAt"].includes(key))) as RecoveryRecord["side"],
		providerHostId: original.providerHostId, providerForkId: original.providerForkId, harness: original.harness,
		cleanupOnly: true, hostStopped: original.hostStopped,
	};
}

// Close intent is owned by Electron for this launch, including while the daemon
// is unavailable. A stale open snapshot must never replace a cleanup record.
export class SideCloseRecovery {
	state?: SideLaunchSnapshot;
	private closing = new Map<string, { original: RecoveryRecord; cleanup: RecoveryRecord; observed: boolean; accepted: boolean; cleanupAttachment?: string }>();
	private closed = new Set<string>();

	remember(state: SideLaunchSnapshot): void {
		const records = state.sides as RecoveryRecord[];
		for (const [id, intent] of this.closing) {
			const incoming = records.find((record) => record.side?.id === id);
			if (incoming?.cleanupOnly) {
				intent.cleanup = incoming;
				intent.observed = true;
			} else if (!incoming && (intent.observed || intent.accepted)) {
				this.closing.delete(id);
			}
		}
		this.state = { ...state, sides: [
			...records.filter((record) => record.side?.id && !this.closing.has(record.side.id) && (record.cleanupOnly || !this.closed.has(record.side.id))),
			...[...this.closing.values()].map((intent) => intent.cleanup),
		] };
	}

	begin(id: string, snapshot?: SideLaunchSnapshot): void {
		if (this.closing.has(id)) return;
		let original = (this.state?.sides as RecoveryRecord[] | undefined)?.find((record) => record.side?.id === id);
		if (!original && snapshot && (!this.state || snapshot.appRunId === this.state.appRunId)) {
			original = (snapshot.sides as RecoveryRecord[]).find(record => record.side?.id === id);
			if (original) this.state = { ...snapshot, ...this.state, sides: [...(this.state?.sides ?? []), original] };
		}
		if (!original) throw new Error("Side chat recovery ownership is unavailable. Refresh and retry close.");
		const cleanup = cleanupSideRecord(original);
		this.closing.set(id, { original, cleanup, observed: Boolean(original.cleanupOnly), accepted: false });
		this.closed.add(id);
		this.state = { ...this.state!, sides: this.state!.sides.map((record) => (record as RecoveryRecord).side?.id === id ? cleanup : record) };
	}

	cancel(id: string): void {
		const intent = this.closing.get(id);
		if (!intent || intent.observed || intent.accepted) return;
		this.closing.delete(id);
		this.closed.delete(id);
		this.state = { ...this.state!, sides: this.state!.sides.map((record) => (record as RecoveryRecord).side?.id === id ? intent.original : record) };
	}

	confirm(id: string): void {
		const intent = this.closing.get(id);
		if (intent) intent.accepted = true;
	}

	acknowledgeCleanup(id: string, attachment: string): void {
		const intent = this.closing.get(id);
		if (intent) intent.cleanupAttachment = attachment;
	}

	cleanupAcknowledged(id: string, attachment: string): boolean {
		return this.closing.get(id)?.cleanupAttachment === attachment;
	}

	markRestoreAttempt(id?: string): void {
		// Once cleanup may have reached a replacement daemon, an old daemon's
		// rejection cannot establish that the close was never accepted.
		for (const [key, intent] of this.closing) if (!id || key === id) intent.observed = true;
	}

	// Only call after an already-closed DELETE and a fresh snapshot from the
	// same daemon: absence then proves its stop-before-delete cleanup finished.
	reconcileMissing(id: string, snapshot: SideLaunchSnapshot): void {
		if (this.state?.appRunId === snapshot.appRunId && !snapshot.sides.some((record) => (record as RecoveryRecord).side?.id === id)) {
			this.closing.delete(id);
			this.closed.add(id);
			// A close-only snapshot need not contain other locally retained sides.
			this.state = { ...this.state, sides: this.state.sides.filter(record => (record as RecoveryRecord).side?.id !== id) };
		}
	}

	pending(sessionId?: string): PendingSideClose[] {
		return [...this.closing.values()]
			.filter((intent) => !intent.accepted && !intent.cleanup.hostStopped && (!sessionId || intent.original.side.sessionId === sessionId))
			.map(({ original }) => ({ id: original.side.id, sessionId: original.side.sessionId, label: original.side.label ?? "" }));
	}
}
