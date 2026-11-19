import { describe, expect, it } from "vitest";
import { resolveDecisionSessionTarget, type DecisionWorkspaceSource } from "./decision-session-target";

const ready = (workspaces: Array<{ id: string; sessions: Array<{ id: string; isTerminated?: boolean }> }>): DecisionWorkspaceSource => ({ status: "ready", workspaces });
const call = { hostId: "h_mac", machineLabel: "mac-mini", ownerSessionId: "s-1" };

describe("resolveDecisionSessionTarget", () => {
	it("opens a local owner on this computer", () => {
		expect(resolveDecisionSessionTarget({
			call, servingHostId: "h_mac", connectedHosts: [],
			local: ready([{ id: "shop", sessions: [{ id: "s-1" }] }]),
			remote: ready([]),
		})).toEqual({ kind: "open", host: "local", projectId: "shop", sessionId: "s-1", terminated: false });
	});

	it("opens a remote owner only through its own connected machine, even when this computer has the same session id", () => {
		expect(resolveDecisionSessionTarget({
			call: { ...call, hostId: "h_laptop", machineLabel: "laptop" }, servingHostId: "h_mac", connectedHosts: ["h_laptop"],
			local: ready([{ id: "shop", sessions: [{ id: "s-1" }] }]),
			remote: ready([{ id: "shop-remote", sessions: [{ id: "s-1" }] }]),
		})).toEqual({ kind: "open", host: "h_laptop", projectId: "shop-remote", sessionId: "s-1", terminated: false });
	});

	it("never guesses a same-id local session for a machine that is not connected", () => {
		expect(resolveDecisionSessionTarget({
			call: { ...call, hostId: "h_laptop", machineLabel: "laptop" }, servingHostId: "h_mac", connectedHosts: [],
			local: ready([{ id: "shop", sessions: [{ id: "s-1" }] }]),
			remote: { status: "loading", workspaces: [] },
		})).toEqual({ kind: "offline", machine: "laptop" });
	});

	it("waits while the board's host identity or the session list is loading", () => {
		expect(resolveDecisionSessionTarget({ call, servingHostId: undefined, connectedHosts: [], local: ready([]), remote: ready([]) })).toEqual({ kind: "checking" });
		expect(resolveDecisionSessionTarget({ call, servingHostId: "h_mac", connectedHosts: [], local: { status: "loading", workspaces: [] }, remote: ready([]) })).toEqual({ kind: "checking" });
	});

	it("says when the session is gone or could not be checked", () => {
		expect(resolveDecisionSessionTarget({ call, servingHostId: "h_mac", connectedHosts: [], local: ready([{ id: "shop", sessions: [{ id: "s-2" }] }]), remote: ready([]) })).toEqual({ kind: "missing" });
		expect(resolveDecisionSessionTarget({ call, servingHostId: "h_mac", connectedHosts: [], local: { status: "error", workspaces: [] }, remote: ready([]) })).toEqual({ kind: "error" });
	});

	it("opens a terminated session's existing view without restarting it, and a projectless owner", () => {
		expect(resolveDecisionSessionTarget({
			call, servingHostId: "h_mac", connectedHosts: [],
			local: ready([{ id: "__standalone__", sessions: [{ id: "s-1", isTerminated: true }] }]),
			remote: ready([]),
		})).toEqual({ kind: "open", host: "local", projectId: "__standalone__", sessionId: "s-1", terminated: true });
	});
});
