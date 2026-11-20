import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { decisionStatusText, pendingRemoteStatusText } from "./decision-card-text";
import type { DecisionSyncStatus } from "./decision-sync";

const t = ((key: string, values?: Record<string, unknown>) => `${key} ${JSON.stringify(values ?? {})}`) as unknown as TFunction;
const now = new Date("2026-11-19T12:00:00Z").getTime();
const sync = (lastHeardAt: string): DecisionSyncStatus => ({
	hostId: "h_me", machineLabel: "laptop", peers: [], conflicts: [],
	origins: [{ hostId: "h_mini", machineLabel: "mac-mini", lastHeardAt }],
});

describe("pendingRemoteStatusText", () => {
	it("waits for a machine heard from within two minutes", () => {
		expect(pendingRemoteStatusText("h_mini", sync(new Date(now - 60_000).toISOString()), now, "en", t))
			.toBe('decisions.card.notDeliveredWaiting {"machine":"mac-mini"}');
	});

	it("says when a stale machine was last heard from", () => {
		expect(pendingRemoteStatusText("h_mini", sync(new Date(now - 2 * 3_600_000).toISOString()), now, "en", t))
			.toBe('decisions.card.notDeliveredStale {"machine":"mac-mini","time":"2 hours ago"}');
	});

	it("leaves this computer's pending calls as they were", () => {
		expect(decisionStatusText({ kind: "pending" }, now, t)).toBe("decisions.notDelivered {}");
		expect(decisionStatusText({ kind: "pending", originHostId: "h_mini" }, now, t, { sync: sync(new Date(now).toISOString()), locale: "en" }))
			.toBe('decisions.card.notDeliveredWaiting {"machine":"mac-mini"}');
	});
});
