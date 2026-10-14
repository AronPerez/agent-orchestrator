import { describe, expect, it } from "vitest";
import type { DashboardSession } from "../api";
import type { ServerConfig } from "../config";
import { buildWatchSnapshot, type WatchSource } from "./snapshot";

const now = 1_800_000_000_000;
const session = (id: string, over: Partial<DashboardSession> = {}) => ({ id, displayName: `Worker ${id}`, status: "needs_input", mode: "chat", ...over }) as DashboardSession;
function source(hostId = "alpha", sessions = [session("same-id")]): WatchSource {
	const config = { hostId, password: "SYNTHETIC_SECRET", host: "example.invalid" } as ServerConfig;
	return { config, connection: "open", error: null, poll: { config, capturedAt: now, sessions } };
}

describe("Watch snapshot", () => {
	it("matches the fork's Needs-you zone, including pinned and review attention, excluding archived workers", () => {
		const result = buildWatchSnapshot(source("alpha", [
			session("pinned", { isPinned: true }), session("busy", { status: "working" }),
			session("ci", { status: "ci_failed" }), session("archived", { isTerminated: true }),
			session("review", { status: "changes_requested" }), session("blocked", { status: "stuck" }),
		]), now);
		expect(result.hosts[0].items.map((item) => item.sessionId)).toEqual(["pinned", "ci", "review", "blocked"]);
		expect(result.hosts[0].count).toBe(4);
	});
	it("sends only display data, never credentials or addresses", () => {
		const result = buildWatchSnapshot(source(), now);
		expect(result.hosts[0]).toMatchObject({ hostId: "alpha", name: "Active host" });
		expect(JSON.stringify(result)).not.toMatch(/SYNTHETIC_SECRET|example.invalid|password|config|endpoints/);
	});
	it("keeps the original successful poll timestamp across bridge deliveries", () => {
		expect(buildWatchSnapshot(source(), now + 60_000).hosts[0].capturedAt).toBe(now);
	});
	it("does not turn an offline or never-loaded host into zero", () => {
		const live = source();
		const previous = buildWatchSnapshot(live, now);
		const result = buildWatchSnapshot({ ...live, connection: "closed", poll: null }, now + 60_000, previous);
		expect(result.hosts[0]).toMatchObject({ capturedAt: now, available: false, count: 1 });
		expect(buildWatchSnapshot({ ...source("beta"), poll: null }, now).hosts[0]).toMatchObject({ capturedAt: null, available: false, count: null, items: [] });
	});
	it("removes old or forgotten hosts and rejects old in-flight poll data after switching hosts", () => {
		const alpha = source(), beta = source("beta");
		const previous = buildWatchSnapshot(alpha, now);
		const result = buildWatchSnapshot({ ...beta, poll: alpha.poll }, now, previous);
		expect(result.hosts).toHaveLength(1);
		expect(result.hosts[0]).toMatchObject({ hostId: "beta", available: false, count: null, items: [] });
		expect(buildWatchSnapshot({ ...alpha, config: null }, now, previous).hosts).toEqual([]);
		expect(buildWatchSnapshot(source("beta", []), now, previous).hosts[0]).toMatchObject({ count: 0, available: true });
	});
	it("bounds rows and Unicode titles without undercounting", () => {
		const result = buildWatchSnapshot(source("alpha", Array.from({ length: 100 }, (_, j) => session(`session-${j}`, { displayName: "🚀".repeat(400) }))), now);
		expect(result.hosts[0].count).toBe(100);
		expect(result.hosts[0].items).toHaveLength(8);
		expect(Array.from(result.hosts[0].items[0].title)).toHaveLength(80);
		expect(Buffer.byteLength(JSON.stringify(result))).toBeLessThan(48_000);
	});
	it("rejects unverified identities, errors, future timestamps and changed credentials/endpoints", () => {
		const live = source();
		for (const over of [
			{ config: { ...live.config!, hostId: "" } }, { error: "unauthorized" },
			{ poll: { ...live.poll!, capturedAt: now + 1000 } },
			{ config: { ...live.config!, password: "ROTATED" } },
		]) {
			expect(buildWatchSnapshot({ ...live, ...over }, now).hosts.every((host) => !host.available)).toBe(true);
		}
	});
	it("orders attention by the fork's original activity timestamp", () => {
		const result = buildWatchSnapshot(source("alpha", [
			session("new", { lastActivityAt: "2026-10-10T00:00:00Z" }),
			session("old", { lastActivityAt: "2026-10-09T00:00:00Z" }),
		]), now);
		expect(result.hosts[0].items.map((item) => item.sessionId)).toEqual(["old", "new"]);
	});
});
