import type { components } from "../../api/schema";
import { apiErrorException } from "./api-error-exception";
import { decisionKeys } from "./decision-inbox";
import { clientForSessionHost } from "./host-clients";
import { LOCAL_HOST } from "./hosts";

export type DecisionSyncStatus = components["schemas"]["DecisionSyncStatusResponse"];
type Peer = components["schemas"]["DecisionSyncPeerStatus"];

/** A peer counts as fresh when it last synced within this window. */
export const DECISION_SYNC_FRESH_MS = 2 * 60_000;
/** A peer's clock this far ahead of ours is worth telling the person. */
export const DECISION_SYNC_CLOCK_AHEAD_MS = 5 * 60_000;

export const decisionSyncKey = () => [...decisionKeys.host(LOCAL_HOST), "sync"] as const;

export async function fetchDecisionSyncStatus(signal?: AbortSignal): Promise<DecisionSyncStatus> {
	const { data, error } = await clientForSessionHost(LOCAL_HOST).GET("/api/v1/decision-sync/status", { signal });
	if (error || !data) throw apiErrorException(error, "Could not load decision sync status");
	return data;
}

export type FooterProblem = "revoked" | "incompatible" | "unreachable" | "identity_conflict" | "clock_ahead";

export type FooterPart =
	| { kind: "local-only" }
	| { kind: "in-sync"; machines: string[] }
	| { kind: "behind"; machine: string; lastSuccessAt?: string }
	| { kind: "problem"; machine: string; reason: FooterProblem; detail?: string };

const peerName = (peer: Peer) => peer.label || peer.hostId;

const syncedRecently = (iso: string | null | undefined, now: Date) =>
	iso != null && now.getTime() - new Date(iso).getTime() <= DECISION_SYNC_FRESH_MS;

/**
 * The footer's parts, problems first. Every surface that speaks about sync
 * freshness (footer, cards, panel) reads this one model so they never disagree.
 */
export function footerParts(status: DecisionSyncStatus, now: Date): FooterPart[] {
	if (status.peers.length === 0) return [{ kind: "local-only" }];
	const problems: FooterPart[] = [];
	const inSync: string[] = [];
	const behind: FooterPart[] = [];
	for (const peer of status.peers) {
		const machine = peerName(peer);
		switch (peer.state) {
			case "revoked":
			case "incompatible":
			case "identity_conflict":
				problems.push({ kind: "problem", machine, reason: peer.state });
				continue;
			case "unreachable":
				problems.push({ kind: "problem", machine, reason: "unreachable", detail: peer.lastSuccessAt ?? undefined });
				continue;
		}
		if (peer.clockAheadMs > DECISION_SYNC_CLOCK_AHEAD_MS) {
			problems.push({ kind: "problem", machine, reason: "clock_ahead", detail: String(peer.clockAheadMs) });
		}
		const fresh = syncedRecently(peer.lastSuccessAt, now);
		if (fresh && (peer.state === "linked_in_sync" || peer.state === "inbound")) inSync.push(machine);
		else behind.push({ kind: "behind", machine, lastSuccessAt: peer.lastSuccessAt ?? undefined });
	}
	return [...problems, ...(inSync.length > 0 ? [{ kind: "in-sync" as const, machines: inSync }] : []), ...behind];
}

/** What a remote call's card and panel say about the machine that owns it. */
export function originFreshness(
	originHostId: string,
	status: DecisionSyncStatus | undefined,
	now: Date,
): { machine: string; lastHeardAt?: string; stale: boolean } {
	const origin = status?.origins.find((o) => o.hostId === originHostId);
	const peer = status?.peers.find((p) => p.hostId === originHostId);
	const machine = peer?.label || origin?.machineLabel || originHostId;
	const lastHeardAt = origin?.lastHeardAt ?? undefined;
	return { machine, lastHeardAt, stale: !syncedRecently(lastHeardAt, now) };
}
