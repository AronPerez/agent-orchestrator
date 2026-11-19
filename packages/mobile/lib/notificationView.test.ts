import { describe, expect, it } from "vitest";
import { notificationAction, notificationGroupKey, notificationOpenKind, notificationsAcrossHosts, notificationSources, notificationRowsForHost, notificationSections, notificationTarget, notificationVisual, relativeTime } from "./notificationView";
import { darkTheme } from "./theme";

describe("notificationVisual", () => {
	it("gives every known type its own label", () => {
		const labels = ["needs_input", "ready_to_merge", "pr_merged", "pr_closed_unmerged", "review_completed", "review_changes_requested"].map(
			(t) => notificationVisual(darkTheme, t).label,
		);
		expect(new Set(labels).size).toBe(6);
	});

	// The renderer draws these with GitHub's own vocabulary, and merged has its own
	// hue rather than an action colour: it is settled, not still open — and it is
	// no longer the grey it shared with a session that had merely switched off.
	it("marks a merged PR the way the palette and the renderer both say", () => {
		const merged = notificationVisual(darkTheme, "pr_merged");
		expect(merged.icon).toBe("git-merge");
		expect(merged.color).toBe(darkTheme.purple);
		expect(merged.color).not.toBe(darkTheme.textSecondary);
		expect(merged.color).not.toBe(darkTheme.accent);
	});

	it("uses pull-request glyphs for pull-request outcomes", () => {
		expect(notificationVisual(darkTheme, "ready_to_merge").icon).toBe("git-pull-request-arrow");
		expect(notificationVisual(darkTheme, "pr_closed_unmerged").icon).toBe("git-pull-request-closed");
	});

	it("gives every known type a distinct semantic icon", () => {
		const icons = ["needs_input", "ready_to_merge", "pr_merged", "pr_closed_unmerged"].map(
			(type) => notificationVisual(darkTheme, type).icon,
		);
		expect(new Set(icons).size).toBe(4);
	});

	it("falls back to a usable label for an unknown type", () => {
		expect(notificationVisual(darkTheme, "something_new").label).toBe("something_new");
		expect(notificationVisual(darkTheme, "").label).toBe("Notification");
	});
});

describe("notificationTarget", () => {
	// Must agree with PushManager's tap routing: the same notification opened
	// from history and from the tray has to land in the same place.
	it("opens the session for a needs_input notification", () => {
		expect(notificationTarget({ type: "needs_input", sessionId: "abc", hostId: "host-a" }, "host-a"))
			.toBe("/session/abc?hostId=host-a");
		expect(notificationTarget({ type: "needs_input", sessionId: "abc", hostId: "host-a" }, new Set(["host-a", "host-b"])))
			.toBe("/session/abc?hostId=host-a");
	});

	it("sends legacy and other-machine pushes to the board", () => {
		expect(notificationTarget({ type: "needs_input", sessionId: "abc" }, "host-b")).toBe("/");
		expect(notificationTarget({ type: "needs_input", sessionId: "abc", hostId: "host-a" }, "host-b")).toBe("/");
		expect(notificationTarget({ type: "ready_to_merge", hostId: "host-a" }, "host-b")).toBe("/");
		expect(notificationTarget({ type: "needs_input", sessionId: "abc", hostId: "forgotten" }, new Set(["host-a", "host-b"]))).toBe("/");
	});

	it("falls back to the PRs tab when there is no session to open", () => {
		expect(notificationTarget({ type: "needs_input", sessionId: "", hostId: "host-a" }, "host-a")).toBe("/prs?hostId=host-a");
		expect(notificationTarget({ type: "needs_input", hostId: "host-a" }, "host-a")).toBe("/prs?hostId=host-a");
	});

	it("sends PR notifications to the PRs tab", () => {
		expect(notificationTarget({ type: "ready_to_merge", sessionId: "abc", hostId: "host-a" }, "host-a")).toBe("/prs?hostId=host-a");
		expect(notificationTarget({ type: "pr_merged", sessionId: "abc", hostId: "host-a" }, new Set(["host-a", "host-b"]))).toBe("/prs?hostId=host-a");
	});

	it("opens the matching review result when the payload identifies its pull request", () => {
		expect(notificationTarget({ type: "review_completed", sessionId: "session 1", prUrl: "https://github.com/acme/repo/pull/42", hostId: "host-a" }, "host-a"))
			.toBe("/review/session%201?prUrl=https%3A%2F%2Fgithub.com%2Facme%2Frepo%2Fpull%2F42&hostId=host-a");
		expect(notificationTarget({ type: "review_changes_requested", sessionId: "s1", prUrl: "https://github.com/acme/repo/pull/43", hostId: "host-b" }, new Set(["host-a", "host-b"])))
			.toBe("/review/s1?prUrl=https%3A%2F%2Fgithub.com%2Facme%2Frepo%2Fpull%2F43&hostId=host-b");
		expect(notificationTarget({ type: "review_completed", sessionId: "s1", prUrl: "https://github.com/acme/repo/pull/42", hostId: "forgotten" }, new Set(["host-a", "host-b"]))).toBe("/");
	});

	it("falls back to the PR list when a review result payload is incomplete", () => {
		expect(notificationTarget({ type: "review_completed", sessionId: "s1", hostId: "host-a" }, "host-a")).toBe("/prs?hostId=host-a");
		expect(notificationTarget({ type: "review_changes_requested", prUrl: "https://github.com/acme/repo/pull/43", hostId: "host-a" }, "host-a")).toBe("/prs?hostId=host-a");
		expect(notificationTarget({ type: "review_completed", sessionId: "s1", prUrl: "https://github.com/acme/repo/pull/43" }, "host-a")).toBe("/");
	});

	// A tray payload carries no guarantee of a type field, and PushManager passes
	// "" when it is missing. An unknown or absent type must still land somewhere
	// rather than routing to "/session/undefined".
	it("sends an unknown or missing type to the PRs tab", () => {
		expect(notificationTarget({ type: "", hostId: "host-a" }, "host-a")).toBe("/prs?hostId=host-a");
		expect(notificationTarget({ type: "", sessionId: "abc", hostId: "host-a" }, "host-a")).toBe("/prs?hostId=host-a");
		expect(notificationTarget({ type: "something_new", sessionId: "abc", hostId: "host-a" }, "host-a")).toBe("/prs?hostId=host-a");
	});
});

describe("notificationSections", () => {
	it("hides old machine notifications even when the new machine reuses the ID", () => {
		const fromA = [{ id: "same-id", title: "Alice's worker" }];
		expect(notificationRowsForHost(fromA, "host-a", "host-b")).toEqual([]);
		expect(notificationRowsForHost(fromA, "host-a", "host-a")).toEqual(fromA);
	});
	it("puts unread notifications in the attention section before earlier history", () => {
		const read = { id: "read", status: "read" };
		const unread = { id: "unread", status: "unread" };

		expect(notificationSections([read, unread])).toEqual([
			{ key: "attention", title: "Needs attention", data: [unread] },
			{ key: "earlier", data: [read] },
		]);
	});

	it("omits empty sections", () => {
		const read = { id: "read", status: "read" };

		expect(notificationSections([read])).toEqual([
			{ key: "earlier", data: [read] },
		]);
		expect(notificationSections([])).toEqual([]);
	});
});

describe("notificationsAcrossHosts", () => {
	it("adds machine identity and sorts all machines into one recent-first timeline", () => {
		const oldest = { id: "old", status: "read", createdAt: "2026-10-08T08:00:00Z" };
		const newest = { id: "new", status: "unread", createdAt: "2026-10-08T10:00:00Z" };
		const middle = { id: "middle", status: "read", createdAt: "2026-10-08T09:00:00Z" };

		expect(notificationsAcrossHosts([
			{ hostId: "host-a", hostName: "Laptop", hostAppearance: { symbol: "laptop" }, items: [oldest, newest] },
			{ hostId: "host-b", hostName: "Desktop", hostAppearance: { symbol: "desktop" }, items: [middle] },
		])).toEqual([
			{ ...newest, hostId: "host-a", hostName: "Laptop", hostAppearance: { symbol: "laptop" } },
			{ ...middle, hostId: "host-b", hostName: "Desktop", hostAppearance: { symbol: "desktop" } },
			{ ...oldest, hostId: "host-a", hostName: "Laptop", hostAppearance: { symbol: "laptop" } },
		]);
	});
});

describe("relativeTime", () => {
	const now = Date.parse("2026-07-30T12:00:00Z");
	const ago = (ms: number) => new Date(now - ms).toISOString();
	const SEC = 1000;
	const MIN = 60 * SEC;
	const HOUR = 60 * MIN;
	const DAY = 24 * HOUR;

	it("collapses anything under a minute to now", () => {
		expect(relativeTime(ago(5 * SEC), now)).toBe("now");
	});

	it("steps through minutes, hours, days and weeks", () => {
		expect(relativeTime(ago(3 * MIN), now)).toBe("3m");
		expect(relativeTime(ago(4 * HOUR), now)).toBe("4h");
		expect(relativeTime(ago(2 * DAY), now)).toBe("2d");
		expect(relativeTime(ago(20 * DAY), now)).toBe("2w");
	});

	// Clock skew between phone and daemon can put a timestamp slightly ahead.
	it("does not render a negative age", () => {
		expect(relativeTime(ago(-30 * SEC), now)).toBe("now");
	});

	it("returns nothing for an unparseable timestamp", () => {
		expect(relativeTime("not-a-date", now)).toBe("");
	});
});

describe("notificationAction", () => {
	const ready = { terminated: false, sessionsReady: true };

	it("opens the session behind a live notification", () => {
		expect(notificationAction({ type: "needs_input", sessionId: "s1" }, ready)).toEqual({ kind: "open", sessionId: "s1" });
		expect(notificationAction({ type: "ready_to_merge", sessionId: "s1" }, ready)).toEqual({ kind: "open", sessionId: "s1" });
	});

	// The renderer's rule: a paused agent is the only thing a terminated
	// needs_input row can act on, so it offers restore rather than navigating to
	// a session with nothing to show.
	it("offers restore instead of opening a terminated session that wants input", () => {
		expect(notificationAction({ type: "needs_input", sessionId: "s1" }, { terminated: true, sessionsReady: true }))
			.toEqual({ kind: "restore", sessionId: "s1" });
	});

	// ...but a finished PR is still worth reading, so termination does not gate it.
	it("still opens a terminated session behind a PR outcome", () => {
		for (const type of ["ready_to_merge", "pr_merged", "pr_closed_unmerged"]) {
			expect(notificationAction({ type, sessionId: "s1" }, { terminated: true, sessionsReady: true }))
				.toEqual({ kind: "open", sessionId: "s1" });
		}
	});

	it("waits rather than guessing before the board has loaded", () => {
		expect(notificationAction({ type: "needs_input", sessionId: "s1" }, { terminated: false, sessionsReady: false }))
			.toEqual({ kind: "none" });
	});

	it("sends a session-less PR notification to the PR list", () => {
		expect(notificationAction({ type: "ready_to_merge" }, ready)).toEqual({ kind: "prs" });
		expect(notificationAction({ type: "needs_input" }, ready)).toEqual({ kind: "none" });
	});

	it("opens review results directly without waiting for the session board", () => {
		const notification = { type: "review_completed", sessionId: "s1", prUrl: "https://github.com/acme/repo/pull/42" };
		expect(notificationAction(notification, { terminated: false, sessionsReady: false })).toEqual({
			kind: "review",
			sessionId: "s1",
			prUrl: notification.prUrl,
		});
	});

	it("sends incomplete review result notifications to the PR list", () => {
		expect(notificationAction({ type: "review_completed", sessionId: "s1" }, ready)).toEqual({ kind: "prs" });
		expect(notificationAction({ type: "review_changes_requested", prUrl: "https://github.com/acme/repo/pull/42" }, ready))
			.toEqual({ kind: "prs" });
	});
});

describe("decision notifications", () => {
	const decision = (id: string, overrides: Record<string, unknown> = {}) => ({
		id, type: "decision_needed", sessionId: "", projectId: "", prUrl: "",
		title: "Decision needed · Shop", body: "Ship it?", status: "read", createdAt: "2026-11-18T10:00:00Z",
		target: { kind: "decision", sessionId: "", callId: "dec_1", originHostId: "host-a" },
		...overrides,
	});

	it("is an amber Decision needed alert", () => {
		const visual = notificationVisual(darkTheme, "decision_needed");
		expect(visual).toEqual({ icon: "bell", color: darkTheme.amber, label: "Decision needed" });
	});

	it("groups one call by its call id and every other row by host and id", () => {
		expect(notificationGroupKey("host-a", decision("n1"))).toBe("decision:dec_1");
		expect(notificationGroupKey("host-b", decision("n9"))).toBe("decision:dec_1");
		expect(notificationGroupKey("host-a", { id: "n1", type: "needs_input" })).toBe("host-a:n1");
		// A malformed or old decision row never shares one malformed key.
		expect(notificationGroupKey("host-a", decision("n2", { target: { kind: "session", sessionId: "" } }))).toBe("host-a:n2");
	});

	it("shows one row for a call two machines announce, unread if either is, led by the origin", () => {
		const rows = notificationsAcrossHosts([
			{ hostId: "host-b", hostName: "B", items: [decision("b-1", { status: "unread", createdAt: "2026-11-18T10:00:05Z" })] },
			{ hostId: "host-a", hostName: "A", items: [decision("a-1")] },
		]);
		expect(rows).toHaveLength(1);
		expect(rows[0]).toMatchObject({ id: "a-1", hostId: "host-a", status: "unread" });
		expect(rows[0].sources).toEqual([{ hostId: "host-a", id: "a-1" }, { hostId: "host-b", id: "b-1" }]);
	});

	it("keeps same-id rows from different machines apart for every other type", () => {
		const row = { id: "n1", type: "needs_input", status: "unread", createdAt: "2026-11-18T10:00:00Z" };
		const rows = notificationsAcrossHosts([
			{ hostId: "host-a", hostName: "A", items: [row] },
			{ hostId: "host-b", hostName: "B", items: [row] },
		]);
		expect(rows.map((r) => r.hostId)).toEqual(["host-a", "host-b"]);
		expect(notificationSources(rows[0])).toEqual([{ hostId: "host-a", id: "n1" }]);
	});

	it("never routes a decision to a session or the PR list", () => {
		expect(notificationAction(decision("n1"), { terminated: false, sessionsReady: true })).toEqual({ kind: "decision", callId: "dec_1" });
		expect(notificationAction(decision("n1", { target: undefined }), { terminated: false, sessionsReady: true })).toEqual({ kind: "none" });
	});

	it("opens a decision push in the notification feed for its call", () => {
		expect(notificationTarget({ type: "decision_needed", decisionCallId: "dec 1", hostId: "host-a" }, "host-a"))
			.toBe("/notifications?decisionCallId=dec%201&hostId=host-a");
		expect(notificationTarget({ type: "decision_needed", decisionCallId: "dec_1", hostId: "forgotten" }, new Set(["host-a"])))
			.toBe("/notifications?decisionCallId=dec_1&hostUnavailable=1");
		expect(notificationTarget({ type: "decision_needed", hostId: "host-a" }, "host-a")).toBe("/");
	});
});

describe("notificationOpenKind", () => {
	it("names where a push tap lands so the tray and history agree", () => {
		expect(notificationOpenKind("/review/s?prUrl=x&hostId=h")).toBe("review");
		expect(notificationOpenKind("/session/s?hostId=h")).toBe("session");
		expect(notificationOpenKind("/notifications?decisionCallId=dec_1&hostId=h")).toBe("notifications");
		expect(notificationOpenKind("/prs?hostId=h")).toBe("prs");
	});
});
