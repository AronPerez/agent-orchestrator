import { describe, expect, it } from "vitest";
import {
	buildFleetTree,
	emptyFleetRollup,
	flattenFleetSessions,
	fleetSessionKey,
	type FleetTreeScope,
} from "./fleet-tree";
import type { SessionStatus } from "./session-models";

type Session = {
	id: string;
	title: string;
	parentSessionId?: string;
	kind?: "orchestrator" | "worker";
	status: SessionStatus;
	isTerminated?: boolean;
};

const session = (id: string, status: SessionStatus, extra: Partial<Session> = {}): Session => ({
	id,
	title: id,
	status,
	...extra,
});

const scope = (
	projectId: string,
	machineId: string,
	sessions: Session[],
	extra: Partial<FleetTreeScope<Session>> = {},
): FleetTreeScope<Session> => ({
	project: { id: projectId, name: projectId.toUpperCase() },
	machine: { id: machineId, name: machineId.toUpperCase(), reachable: true },
	sessions,
	...extra,
});

describe("buildFleetTree", () => {
	it("groups equal project IDs across machines while keeping equal session IDs distinct", () => {
		const tree = buildFleetTree([
			scope("ao", "laptop", [session("worker", "working")]),
			scope("ao", "server", [session("worker", "needs_input")]),
			scope("docs", "laptop", [session("worker", "idle")]),
		]);

		expect(tree.map((project) => project.project.id)).toEqual(["ao", "docs"]);
		expect(tree[0]?.machines.map((machine) => machine.machine.id)).toEqual(["laptop", "server"]);
		const [local, remote] = tree[0]!.machines.map((machine) => machine.sessions[0]!);
		expect(local.key).toBe(fleetSessionKey("ao", "laptop", "worker"));
		expect(remote.key).toBe(fleetSessionKey("ao", "server", "worker"));
		expect(local.key).not.toBe(remote.key);
		expect(tree[0]?.rollup).toEqual({ ...emptyFleetRollup(), action: 1, working: 1 });
	});

	it("derives roles and descendant-only roll-ups from the shared attention zones", () => {
		const tree = buildFleetTree([
			scope("ao", "laptop", [
				session("orch", "working", { kind: "orchestrator" }),
				session("manager", "idle", { parentSessionId: "orch" }),
				session("blocked", "needs_input", { parentSessionId: "manager" }),
				session("review", "review_pending", { parentSessionId: "manager" }),
				session("ready", "approved", { parentSessionId: "orch" }),
			]),
		]);

		const machine = tree[0]!.machines[0]!;
		const orchestrator = machine.sessions[0]!;
		const manager = orchestrator.children[0]!;

		expect(orchestrator.role).toBe("orchestrator");
		expect(manager.role).toBe("manager");
		expect(manager.rollup).toEqual({ ...emptyFleetRollup(), action: 1, pending: 1 });
		expect(orchestrator.rollup).toEqual({ action: 1, done: 0, merge: 1, pending: 1, working: 1 });
		expect(machine.rollup).toEqual({ action: 1, done: 0, merge: 1, pending: 1, working: 2 });
	});

	it("keeps a terminated ancestor needed by a live descendant but hides unrelated archives", () => {
		const tree = buildFleetTree([
			scope("ao", "laptop", [
				session("old-orch", "terminated", { kind: "orchestrator", isTerminated: true }),
				session("live", "working", { parentSessionId: "old-orch" }),
				session("old-worker", "terminated", { isTerminated: true }),
			]),
		]);

		expect(flattenFleetSessions(tree[0]!.machines[0]!.sessions, new Set()).map(({ node }) => node.session.id))
			.toEqual(["old-orch", "live"]);
		expect(buildFleetTree([
			scope("ao", "laptop", [
				session("old-orch", "terminated", { kind: "orchestrator", isTerminated: true }),
				session("live", "working", { parentSessionId: "old-orch" }),
				session("old-worker", "terminated", { isTerminated: true }),
			]),
		], { showArchived: true })[0]?.machines[0]?.sessions).toHaveLength(2);
	});

	it("shows missing parents and corrupt cycles once instead of losing sessions", () => {
		const tree = buildFleetTree([
			scope("ao", "laptop", [
				session("orphan", "working", { parentSessionId: "gone" }),
				session("a", "idle", { parentSessionId: "b" }),
				session("b", "idle", { parentSessionId: "a" }),
				session("self", "idle", { parentSessionId: "self" }),
			]),
		]);
		const rows = flattenFleetSessions(tree[0]!.machines[0]!.sessions, new Set());

		expect(rows.map(({ node }) => node.session.id).sort()).toEqual(["a", "b", "orphan", "self"]);
		expect(rows.find(({ node }) => node.session.id === "orphan")?.node.parentUnavailable).toBe(true);
		expect(rows.find(({ node }) => node.session.id === "a")?.node.parentUnavailable).toBe(true);
		expect(rows.find(({ node }) => node.session.id === "self")?.node.parentUnavailable).toBe(true);
	});

	it("flattens collapse state by qualified node key", () => {
		const machine = buildFleetTree([
			scope("ao", "laptop", [
				session("manager", "working"),
				session("worker", "working", { parentSessionId: "manager" }),
			]),
		])[0]!.machines[0]!;
		const manager = machine.sessions[0]!;

		expect(flattenFleetSessions(machine.sessions, new Set([manager.key])).map(({ node, depth }) => [node.session.id, depth]))
			.toEqual([["manager", 0]]);
		expect(flattenFleetSessions(machine.sessions, new Set()).map(({ node, depth }) => [node.session.id, depth]))
			.toEqual([["manager", 0], ["worker", 1]]);
	});
});
