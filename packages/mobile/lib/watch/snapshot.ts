import { boardZoneOf, isArchived } from "../agentsView";
import type { ServerConfig } from "../config";
import type { SessionPoll } from "../store";
import { sessionTitle } from "../sessionStatus";

export type WatchItem = { sessionId: string; title: string; reason: string; mode: "chat" | "tui" };
export type WatchHost = { hostId: string; name: string; capturedAt: number | null; available: boolean; count: number | null; items: WatchItem[] };
export type WatchSnapshot = { version: 1; generatedAt: number; omittedHosts: number; hosts: WatchHost[] };
export type WatchSource = { config: ServerConfig | null; connection: string; error: string | null; poll: SessionPoll | null };

const short = (text: string, max: number) => Array.from(text).slice(0, max).join("");

/** Only the active host's display fields cross the bridge, never credentials or endpoints. */
export function buildWatchSnapshot(source: WatchSource, now: number, previous?: WatchSnapshot): WatchSnapshot {
	const snapshot: WatchSnapshot = { version: 1, generatedAt: now, omittedHosts: 0, hosts: [] };
	const { config, connection, error, poll } = source;
	if (!config?.hostId) return snapshot;
	const identity = { hostId: config.hostId, name: "Active host" };
	const available = connection === "open" && !error && poll?.config === config && poll.capturedAt > 0 && poll.capturedAt <= now;
	if (!available) {
		const cached = previous?.hosts.find((entry) => entry.hostId === config.hostId);
		snapshot.hosts = [{ ...identity, available: false, capturedAt: cached?.capturedAt ?? null, count: cached?.count ?? null, items: cached?.items ?? [] }];
		return snapshot;
	}
	const attention = poll.sessions.filter((session) => !isArchived(session) && boardZoneOf(session) === "action");
	attention.sort((a, b) => (Date.parse(a.lastActivityAt ?? "") || 0) - (Date.parse(b.lastActivityAt ?? "") || 0));
	snapshot.hosts = [{
		...identity, available: true, capturedAt: poll.capturedAt, count: attention.length,
		items: attention.slice(0, 8).map((session) => ({
			sessionId: session.id,
			title: short(sessionTitle(session), 80),
			reason: session.status === "stuck" ? "Agent stuck" : session.status === "ci_failed" ? "CI failed" : session.status === "changes_requested" ? "Review changes requested" : "Needs your input",
			mode: session.mode === "chat" ? "chat" : "tui",
		})),
	}];
	return snapshot;
}
