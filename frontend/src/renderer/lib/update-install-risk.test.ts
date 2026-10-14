import { describe, expect, it } from "vitest";
import { updateInstallRisk } from "./update-install-risk";

const session = {
	id: "same-id",
	mode: "chat",
	isTerminated: false,
	chatProviderPreserved: false,
	activity: { state: "active" },
};
const risk = (...sessions: unknown[]) => updateInstallRisk({ sessions });

describe("update install risk", () => {
	it.each(["active", "waiting_input", "blocked"])("warns for %s Chat regardless of derived board status or harness", (state) => {
		expect(risk({ ...session, harness: "codex", status: "mergeable", activity: { state } })).toEqual({ count: 1, unknown: false });
	});
	it.each([
		{ chatProviderPreserved: true },
		{ mode: "tui" },
		{ isTerminated: true },
		{ activity: { state: "idle" } },
		{ activity: { state: "exited" } },
	])("does not warn for known safe state %j", (safe) => {
		expect(risk({ ...session, ...safe })).toEqual({ count: 0, unknown: false });
	});
	it.each([
		{ activity: undefined }, { activity: { state: "unknown" } },
		{ activity: { state: "future-state" } }, { mode: undefined },
		{ chatProviderPreserved: undefined },
	])("fails conservatively for missing or unknown facts %j", (unknown) => {
		expect(risk({ ...session, ...unknown }).unknown).toBe(true);
	});
	it.each([undefined, {}, { sessions: null }, { sessions: [null] }, { sessions: ["bad"] }, { sessions: [], nextPageToken: "more" }])("does not treat malformed/incomplete snapshots as empty: %j", (body) => {
		expect(updateInstallRisk(body).unknown).toBe(true);
	});
	it("includes standalone and orchestrator sessions, and accepts an empty complete snapshot", () => {
		expect(risk(session, { ...session, kind: "orchestrator" }).count).toBe(2);
		expect(risk()).toEqual({ count: 0, unknown: false });
	});
});
