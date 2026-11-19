// Presentation rules for the notification history list. Pure — no React Native
// or Expo imports — so the wording and the routing decision are unit-testable,
// the same split as pushStatus.ts / push.ts.
import type { Theme } from "./theme";
import type { MachineAppearance } from "./machineAppearance";

export type NotificationVisual = {
	/** lucide icon names, drawn from the renderer's own path data. */
	icon: "message-square-dot" | "git-pull-request-arrow" | "git-merge" | "git-pull-request-closed" | "bell";
	color: string;
	label: string;
};

export type NotificationSection<T> = {
	key: "attention" | "earlier";
	/**
	 * Absent for the read group: the split still orders unread first, but the
	 * rows below it are plainly older and did not need a word to say so.
	 */
	title?: "Needs attention";
	data: T[];
};

/** One daemon row behind a feed item: a decision call can be announced by several machines. */
export type NotificationSource = { hostId: string; id: string };

export type HostedNotification<T> = T & {
	hostId: string;
	hostName: string;
	hostAppearance?: MachineAppearance;
	/** Every daemon row this item stands for, in a stable order. */
	sources?: readonly NotificationSource[];
};

/** The decision-call target the daemon attaches to a decision_needed row. */
export type DecisionTargetFields = { target?: { kind?: string; callId?: string; originHostId?: string } };

/** Stable identity for an item whose daemon-local id may exist on another host. */
export function notificationIdentity(hostId: string, id: string): string {
	return `${hostId}:${id}`;
}

/** The call a well-formed decision notification announces, else undefined. */
export function decisionCallId(n: { type?: string } & DecisionTargetFields): string | undefined {
	if (n.type !== "decision_needed" || n.target?.kind !== "decision") return undefined;
	return n.target.callId?.trim() || undefined;
}

/**
 * One call is one item however many machines announce it; every other row is
 * its own item, since daemon-local ids repeat across machines.
 */
export function notificationGroupKey(hostId: string, n: { id: string; type?: string } & DecisionTargetFields): string {
	const callId = decisionCallId(n);
	return callId ? `decision:${callId}` : notificationIdentity(hostId, n.id);
}

/**
 * Adds the owning machine to daemon records and produces one recent-first feed.
 * Copies of one decision call collapse into one item led by the call's origin
 * machine (else the newest copy); it stays unread while any copy is unread.
 */
export function notificationsAcrossHosts<T extends { id: string; createdAt: string; type?: string; status?: string } & DecisionTargetFields>(
	batches: readonly { hostId: string; hostName: string; hostAppearance?: MachineAppearance; items: readonly T[] }[],
): HostedNotification<T>[] {
	const ordered = batches
		.flatMap(({ hostId, hostName, hostAppearance, items }) => items.map((item) => ({ ...item, hostId, hostName, hostAppearance })))
		.sort((left, right) => {
			const recentFirst = Date.parse(right.createdAt) - Date.parse(left.createdAt);
			if (Number.isFinite(recentFirst) && recentFirst !== 0) return recentFirst;
			return `${left.hostId}:${left.id}`.localeCompare(`${right.hostId}:${right.id}`);
		});
	const groups = new Map<string, HostedNotification<T>[]>();
	for (const item of ordered) {
		const key = notificationGroupKey(item.hostId, item);
		groups.set(key, [...(groups.get(key) ?? []), item]);
	}
	// Map order is first-seen order, so groups stay recent-first by their newest copy.
	return [...groups.values()].map((copies) => {
		if (!decisionCallId(copies[0])) return copies[0];
		const primary = copies.find((copy) => copy.hostId === copy.target?.originHostId) ?? copies[0];
		const sources = copies
			.map(({ hostId, id }) => ({ hostId, id }))
			.sort((left, right) => notificationIdentity(left.hostId, left.id).localeCompare(notificationIdentity(right.hostId, right.id)));
		const unread = copies.some((copy) => copy.status === "unread");
		return { ...primary, ...(unread ? { status: "unread" } : {}), sources };
	});
}

/** The daemon rows behind one feed item. */
export function notificationSources(item: { hostId: string; id: string; sources?: readonly NotificationSource[] }): readonly NotificationSource[] {
	return item.sources ?? [{ hostId: item.hostId, id: item.id }];
}

export function notificationRowsForHost<T>(items: readonly T[], itemsHostId: string | undefined, activeHostId: string | undefined): readonly T[] {
	return activeHostId && itemsHostId === activeHostId ? items : [];
}

/**
 * Keeps actionable unread history ahead of settled items without disturbing
 * the daemon's newest-first order inside either group.
 */
export function notificationSections<T extends { status: string }>(items: readonly T[]): NotificationSection<T>[] {
	const attention = items.filter((item) => item.status === "unread");
	const earlier = items.filter((item) => item.status !== "unread");
	const sections: NotificationSection<T>[] = [];
	if (attention.length > 0) sections.push({ key: "attention", title: "Needs attention", data: attention });
	if (earlier.length > 0) sections.push({ key: "earlier", data: earlier });
	return sections;
}

/** Icon, colour and short label for one notification type. */
export function notificationVisual(t: Theme, type: string): NotificationVisual {
	switch (type) {
		case "needs_input":
			return { icon: "message-square-dot", color: t.amber, label: "Needs input" };
		case "ready_to_merge":
			return { icon: "git-pull-request-arrow", color: t.green, label: "Ready to merge" };
		case "pr_merged":
			// Not an action colour: merged is settled and not actionable. It went from
			// blue (an action still open) to the grey shared with "done" and "killed",
			// which undersold the one thing that did happen. Purple is merged's own
			// hue now, here and on the row and the card, so the word means one colour.
			return { icon: "git-merge", color: t.purple, label: "Merged" };
		case "pr_closed_unmerged":
			return { icon: "git-pull-request-closed", color: t.red, label: "Closed" };
		case "review_completed":
			return { icon: "git-pull-request-arrow", color: t.green, label: "Review completed" };
		case "review_changes_requested":
			return { icon: "message-square-dot", color: t.amber, label: "Review changes requested" };
		case "decision_needed":
			return { icon: "bell", color: t.amber, label: "Decision needed" };
		// Orchestrator Cloud's types, which share this list.
		case "agent_failed":
			return { icon: "bell", color: t.red, label: "Agent failed" };
		case "agent_completed":
			return { icon: "bell", color: t.green, label: "Completed" };
		case "pr_opened":
			return { icon: "git-pull-request-arrow", color: t.blue, label: "PR opened" };
		case "review_feedback":
			return { icon: "message-square-dot", color: t.amber, label: "Review feedback" };
		case "merge_requested":
			return { icon: "git-pull-request-arrow", color: t.amber, label: "Merge requested" };
		case "merge_request_merged":
			return { icon: "git-merge", color: t.purple, label: "Merged" };
		case "merge_request_rejected":
			return { icon: "git-pull-request-closed", color: t.red, label: "Merge rejected" };
		default:
			return { icon: "bell", color: t.textTertiary, label: type || "Notification" };
	}
}

/**
 * Where tapping a notification should land. Mirrors the routing PushManager
 * already applies to a notification tap, so opening an item from history and
 * opening it from the tray agree — the rule lives here rather than being written
 * twice.
 */
export function notificationTarget(n: { type: string; sessionId?: string; hostId?: string; prUrl?: string; decisionCallId?: string }, knownHosts: string | ReadonlySet<string> | undefined): string {
	const known = Boolean(n.hostId) && (typeof knownHosts === "string" ? n.hostId === knownHosts : Boolean(knownHosts?.has(n.hostId!)));
	if (n.type === "decision_needed") {
		// Until the phone can open a call, a decision lands on its row in the
		// feed. A machine that is no longer paired is said so, never guessed.
		const callId = n.decisionCallId?.trim();
		if (!callId || !n.hostId) return "/";
		const call = `/notifications?decisionCallId=${encodeURIComponent(callId)}`;
		return known ? `${call}&hostId=${encodeURIComponent(n.hostId)}` : `${call}&hostUnavailable=1`;
	}
	// An older push has no machine identity. It cannot safely open (or mark read)
	// a same-ID session on whichever machine is currently selected. History passes
	// its current host; push taps can name any paired host.
	if (!known || !n.hostId) return "/";
	const sessionId = n.sessionId?.trim();
	const hostId = encodeURIComponent(n.hostId);
	if ((n.type === "review_completed" || n.type === "review_changes_requested") && sessionId && n.prUrl) {
		return `/review/${encodeURIComponent(sessionId)}?prUrl=${encodeURIComponent(n.prUrl)}&hostId=${hostId}`;
	}
	return n.type === "needs_input" && sessionId ? `/session/${encodeURIComponent(sessionId)}?hostId=${hostId}` : `/prs?hostId=${hostId}`;
}

/** Which kind of page a notificationTarget destination opens. */
export function notificationOpenKind(destination: string): "review" | "session" | "notifications" | "prs" {
	if (destination.startsWith("/review")) return "review";
	if (destination.startsWith("/session")) return "session";
	if (destination.startsWith("/notifications")) return "notifications";
	return "prs";
}

/** Compact "3m" / "4h" / "2d" stamp. Returns "" for an unparseable timestamp. */
export function relativeTime(iso: string, now: number = Date.now()): string {
	const then = Date.parse(iso);
	if (Number.isNaN(then)) return "";
	const secs = Math.max(0, Math.round((now - then) / 1000));
	if (secs < 60) return "now";
	const mins = Math.floor(secs / 60);
	if (mins < 60) return `${mins}m`;
	const hours = Math.floor(mins / 60);
	if (hours < 24) return `${hours}h`;
	const days = Math.floor(hours / 24);
	if (days < 7) return `${days}d`;
	return `${Math.floor(days / 7)}w`;
}

/**
 * What tapping a notification should do, given the state of the session behind
 * it. Ported from the renderer's NotificationItem, which decides this with:
 *
 *   offerRestore    = terminated && type === "needs_input"
 *   canOpenSession  = sessionId && sessionsReady && (!terminated || !offerRestore)
 *
 * Review outcomes are the exception: their payload identifies the exact PR,
 * so they can open review detail without waiting for the session board.
 * The distinction the remaining notifications draw is worth keeping: a terminated session behind
 * "needs input" has a paused agent and nothing to show, so restore is the only
 * sensible action. A terminated session behind a PR outcome describes work that
 * already finished — there is nothing to resume, so it stays readable rather
 * than being gated behind a restore nobody wants.
 */
export type NotificationAction =
	| { kind: "open"; sessionId: string }
	| { kind: "review"; sessionId: string; prUrl: string }
	| { kind: "restore"; sessionId: string }
	| { kind: "prs" }
	| { kind: "decision"; callId: string }
	| { kind: "none" };

export function notificationAction(
	n: { type: string; sessionId?: string; prUrl?: string } & DecisionTargetFields,
	state: { terminated: boolean; sessionsReady: boolean },
): NotificationAction {
	// A decision targets its call, never a session or the PR list. One without
	// a call (an old or malformed row) has nowhere safe to go.
	if (n.type === "decision_needed") {
		const callId = decisionCallId(n);
		return callId ? { kind: "decision", callId } : { kind: "none" };
	}
	const sessionId = n.sessionId?.trim();
	if (n.type === "review_completed" || n.type === "review_changes_requested") {
		return sessionId && n.prUrl
			? { kind: "review", sessionId, prUrl: n.prUrl }
			: { kind: "prs" };
	}
	// No session to open: a PR outcome still has somewhere useful to go.
	if (!sessionId) return n.type === "needs_input" ? { kind: "none" } : { kind: "prs" };
	// The board has not loaded yet, so whether it is terminated is unknown.
	// Guessing "open" would land on a screen that cannot resolve the session.
	if (!state.sessionsReady) return { kind: "none" };
	const offerRestore = state.terminated && n.type === "needs_input";
	if (offerRestore) return { kind: "restore", sessionId };
	return { kind: "open", sessionId };
}
