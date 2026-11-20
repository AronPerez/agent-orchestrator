import { describe, expect, it } from "vitest";
import { footerParts, originFreshness, type DecisionSyncStatus } from "./decision-sync";

const now = new Date("2026-11-19T12:00:00Z");
const ago = (ms: number) => new Date(now.getTime() - ms).toISOString();

function status(peers: Partial<DecisionSyncStatus["peers"][number]>[] = [], origins: DecisionSyncStatus["origins"] = []): DecisionSyncStatus {
	return {
		hostId: "h_me", machineLabel: "laptop", conflicts: [], origins,
		peers: peers.map((p) => ({ hostId: "h_x", label: "x", direction: "outbound", inSync: false, clockAheadMs: 0, state: "linked_behind", ...p })),
	};
}

describe("footerParts", () => {
	it("says this computer only when nothing is linked", () => {
		expect(footerParts(status(), now)).toEqual([{ kind: "local-only" }]);
	});

	it("lists fresh peers together and names stale ones", () => {
		const parts = footerParts(status([
			{ hostId: "h_a", label: "mac-mini", state: "linked_in_sync", inSync: true, lastSuccessAt: ago(30_000) },
			{ hostId: "h_b", label: "vm-2", state: "linked_in_sync", inSync: true, lastSuccessAt: ago(3 * 3_600_000) },
			{ hostId: "h_c", label: "desk", state: "inbound", lastSuccessAt: ago(60_000) },
		]), now);
		expect(parts).toEqual([
			{ kind: "in-sync", machines: ["mac-mini", "desk"] },
			{ kind: "behind", machine: "vm-2", lastSuccessAt: ago(3 * 3_600_000) },
		]);
	});

	it("puts problems first, each naming the machine", () => {
		const parts = footerParts(status([
			{ hostId: "h_a", label: "mac-mini", state: "linked_in_sync", lastSuccessAt: ago(1_000) },
			{ hostId: "h_b", label: "vm-2", state: "revoked" },
			{ hostId: "h_c", label: "old", state: "incompatible" },
			{ hostId: "h_d", label: "away", state: "unreachable", lastSuccessAt: ago(7_200_000) },
			{ hostId: "h_e", label: "copy", state: "identity_conflict" },
			{ hostId: "h_f", label: "fast", state: "linked_in_sync", clockAheadMs: 420_000, lastSuccessAt: ago(1_000) },
		]), now);
		expect(parts.map((p) => p.kind === "problem" ? `${p.reason}:${p.machine}` : p.kind)).toEqual([
			"revoked:vm-2", "incompatible:old", "unreachable:away", "identity_conflict:copy", "clock_ahead:fast", "in-sync",
		]);
	});
});

describe("originFreshness", () => {
	const s = status([{ hostId: "h_a", label: "mini (desk)" }], [
		{ hostId: "h_a", machineLabel: "mac-mini", lastHeardAt: ago(60_000) },
		{ hostId: "h_far", machineLabel: "vm-2", lastHeardAt: ago(2 * 60_000 + 1) },
	]);

	it("is fresh within two minutes and prefers the person's name for a peer", () => {
		expect(originFreshness("h_a", s, now)).toEqual({ machine: "mini (desk)", lastHeardAt: ago(60_000), stale: false });
	});

	it("is stale past two minutes, or never heard", () => {
		expect(originFreshness("h_far", s, now).stale).toBe(true);
		expect(originFreshness("h_unknown", s, now)).toEqual({ machine: "h_unknown", lastHeardAt: undefined, stale: true });
		expect(originFreshness("h_a", undefined, now).stale).toBe(true);
	});
});
