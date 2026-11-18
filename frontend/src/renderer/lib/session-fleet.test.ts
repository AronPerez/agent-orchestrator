import { describe, expect, it } from "vitest";
import { fleetRows } from "./session-fleet";
import type { WorkspaceSession } from "../types/workspace";

const session = (id: string, parentSessionId?: string, extra: Partial<WorkspaceSession> = {}): WorkspaceSession => ({
	id,
	parentSessionId,
	title: id,
	workspaceId: "p",
	updatedAt: "2026-11-03T00:00:00Z",
	prs: [],
	workspaceName: "Project",
	provider: "codex",
	status: "idle",
	...extra,
});

describe("fleetRows", () => {
	it("shows recorded three-level hierarchy and derives manager role from children, never names", () => {
		const rows = fleetRows(
			[
				session("worker", "manager"),
				session("manager", "orch"),
				session("orch", undefined, { kind: "orchestrator" }),
				session("manager-in-name-only"),
			],
			new Set(),
		);
		expect(rows.map(({ session, depth, role }) => [session.id, depth, role])).toEqual([
			["orch", 0, "Orchestrator"],
			["manager", 1, "Manager"],
			["worker", 2, "Worker"],
			["manager-in-name-only", 0, "Worker"],
		]);
	});
	it("collapses descendants without losing them as roots", () => {
		expect(
			fleetRows(
				[session("parent"), session("child", "parent"), session("grandchild", "child")],
				new Set(["parent"]),
			).map((row) => row.session.id),
		).toEqual(["parent"]);
	});
	it("keeps archived ancestors but hides unrelated archived sessions", () => {
		const sessions = [
			session("parent", undefined, { isTerminated: true }),
			session("child", "parent"),
			session("old", undefined, { isTerminated: true }),
		];
		expect(fleetRows(sessions, new Set()).map((row) => row.session.id)).toEqual(["parent", "child"]);
		expect(fleetRows(sessions, new Set(), true)).toHaveLength(3);
	});
	it("keeps orphans and cycles visible once without recursion", () => {
		const rows = fleetRows(
			[session("orphan", "missing"), session("self", "self"), session("a", "b"), session("b", "a")],
			new Set(),
		);
		expect(new Set(rows.map((row) => row.session.id)).size).toBe(4);
		expect(rows.filter((row) => row.parentUnavailable).map((row) => row.session.id)).toEqual(["orphan", "self", "a"]);
	});
	it("does not attach an external parent to a same-named local session", () => {
		const rows = fleetRows(
			[session("parent"), session("child", "parent", { parentHostId: "h_other" })],
			new Set(),
			false,
			"h_local",
		);
		expect(rows.map(({ session, depth, parentUnavailable }) => [session.id, depth, parentUnavailable])).toEqual([
			["parent", 0, false],
			["child", 0, true],
		]);
		expect(rows.find((row) => row.session.id === "child")?.parentAddress).toEqual({ machineId: "h_other", sessionId: "parent" });
	});
	it("uses the daemon host ID for local parent addresses", () => {
		const rows = fleetRows([session("parent"), session("child", "parent")], new Set(), false, "h_local");
		expect(rows.find((row) => row.session.id === "child")?.parentAddress).toEqual({ machineId: "h_local", sessionId: "parent" });
	});
});
