import { describe, expect, it } from "vitest";
import { fleetRows } from "./session-fleet";
import type { WorkspaceSession } from "../types/workspace";

const session = (id: string, parentSessionId?: string, extra: Partial<WorkspaceSession> = {}): WorkspaceSession => ({
	id,
	parentSessionId,
	title: id,
	workspaceId: "p",
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
});
