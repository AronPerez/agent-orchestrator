import { describe, expect, it } from "vitest";
import {
	buildFleetTree,
	emptyFleetRollup,
	flattenFleetSessions,
	limitFleetCollapsePreferences,
	parseFleetCollapsePreferences,
	rememberFleetCollapsePreference,
	resolveFleetCollapsedKeys,
	fleetSessionKey,
	type FleetTreeScope,
} from "./fleet-tree";
import type { SessionStatus } from "./session-models";

type Session = {
	id: string;
	title: string;
	parentHostId?: string;
	parentSessionId?: string;
	kind?: "orchestrator" | "worker";
	status: SessionStatus;
	rollupStatus?: SessionStatus;
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
	it("keeps equal host-local project IDs separate unless a trusted link groups them", () => {
		const tree = buildFleetTree([
			scope("ao", "laptop", [session("worker", "working")]),
			scope("ao", "server", [session("worker", "needs_input")]),
			scope("docs", "laptop", [session("worker", "idle")]),
		]);

		expect(tree.map((project) => project.project.id)).toEqual(["ao", "ao", "docs"]);
		expect(tree.map((project) => project.machines[0]?.machine.id)).toEqual(["laptop", "server", "laptop"]);
		expect(tree[0]?.key).not.toBe(tree[1]?.key);
		const [local, remote] = tree.slice(0, 2).map((project) => project.machines[0]!.sessions[0]!);
		expect(local.key).toBe(fleetSessionKey("ao", "laptop", "worker"));
		expect(remote.key).toBe(fleetSessionKey("ao", "server", "worker"));
		expect(local.key).not.toBe(remote.key);
		expect(tree[0]?.rollup).toEqual({ ...emptyFleetRollup(), working: 1 });
		expect(tree[1]?.rollup).toEqual({ ...emptyFleetRollup(), action: 1 });

		const linked = buildFleetTree([
			scope("ao", "laptop", [session("local", "working")], { projectLinkId: "repo-123" }),
			scope("ao-mirror", "server", [session("remote", "needs_input")], { projectLinkId: "repo-123" }),
		]);
		expect(linked).toHaveLength(1);
		expect(linked[0]?.machines.map((machine) => machine.machine.id)).toEqual(["laptop", "server"]);
		expect(linked[0]?.rollup).toEqual({ ...emptyFleetRollup(), action: 1, working: 1 });
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

	it("can roll up a delivery status while retaining the session's live agent status", () => {
		const tree = buildFleetTree([
			scope("ao", "laptop", [session("worker", "working", { rollupStatus: "ci_failed" })]),
		]);

		expect(tree[0]!.machines[0]!.sessions[0]!.session.status).toBe("working");
		expect(tree[0]!.machines[0]!.sessions[0]!.zone).toBe("action");
		expect(tree[0]!.rollup).toEqual({ ...emptyFleetRollup(), action: 1 });
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

	it("keeps recorded manager roles when archived children are hidden", () => {
		const manager = buildFleetTree([
			scope("ao", "laptop", [
				session("manager", "working"),
				session("archived", "terminated", { parentSessionId: "manager", isTerminated: true }),
			]),
		])[0]!.machines[0]!.sessions[0]!;

		expect(manager.children).toHaveLength(0);
		expect(manager.role).toBe("manager");
	});

	it("keeps registered scopes whose sessions are hidden by archive filtering", () => {
		const archived = scope("ao", "server", [
			session("archived", "terminated", { isTerminated: true }),
		]);

		const active = buildFleetTree([archived]);
		expect(active).toHaveLength(1);
		expect(active[0]!.machines[0]!.sessions).toEqual([]);
		expect(buildFleetTree([archived], { showArchived: true })).toHaveLength(1);
	});

	it("counts terminated merged sessions as done when retained or archives are shown", () => {
		const sessions = [
			session("retained", "merged", { kind: "orchestrator", isTerminated: true }),
			session("live", "working", { parentSessionId: "retained" }),
			session("archived", "merged", { isTerminated: true }),
		];
		const active = buildFleetTree([scope("ao", "laptop", sessions)])[0]!.machines[0]!;

		expect(active.sessions[0]?.zone).toBe("done");
		expect(active.rollup).toEqual({ ...emptyFleetRollup(), done: 1, working: 1 });

		const withArchives = buildFleetTree([scope("ao", "laptop", sessions)], { showArchived: true })[0]!
			.machines[0]!;
		expect(withArchives.rollup).toEqual({ ...emptyFleetRollup(), done: 2, working: 1 });
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

	it("keeps same-host qualified parents local and exposes cross-machine parents without attaching them", () => {
		const machine = buildFleetTree([
			scope("ao", "laptop", [
				session("parent", "working"),
				session("local-child", "idle", { parentHostId: "laptop", parentSessionId: "parent" }),
				session("remote-child", "idle", { parentHostId: "server", parentSessionId: "parent" }),
			]),
		])[0]!.machines[0]!;
		const rows = flattenFleetSessions(machine.sessions, new Set());
		const localChild = rows.find(({ node }) => node.session.id === "local-child")!;
		const remoteChild = rows.find(({ node }) => node.session.id === "remote-child")!;

		expect(localChild.depth).toBe(1);
		expect(localChild.node.parentUnavailable).toBe(false);
		expect(localChild.node.parentAddress).toEqual({ machineId: "laptop", sessionId: "parent" });
		expect(remoteChild.depth).toBe(0);
		expect(remoteChild.node.parentUnavailable).toBe(true);
		expect(remoteChild.node.parentUnavailableReason).toBe("machine-unknown");
		expect(remoteChild.node.parentAddress).toEqual({ machineId: "server", sessionId: "parent" });
		expect(localChild.node.parentUnavailableReason).toBeUndefined();
	});

	it("draws a worker on another machine under its orchestrator by exact address", () => {
		const tree = buildFleetTree([
			scope("ao", "laptop", [session("orch", "working", { kind: "orchestrator" })]),
			scope("ao-box", "server", [
				session("worker", "needs_input", { parentHostId: "laptop", parentSessionId: "orch" }),
			]),
		]);

		expect(tree.map((project) => project.project.id)).toEqual(["ao"]);
		const laptop = tree[0]!.machines[0]!;
		const rows = flattenFleetSessions(laptop.sessions, new Set());
		expect(rows.map(({ node, depth }) => [node.session.id, node.machineId, depth])).toEqual([
			["orch", "laptop", 0],
			["worker", "server", 1],
		]);
		const worker = rows[1]!.node;
		expect(worker.key).toBe(fleetSessionKey("ao-box", "server", "worker"));
		expect(worker.projectId).toBe("ao-box");
		expect(worker.parentUnavailable).toBe(false);
		expect(worker.parentUnavailableReason).toBeUndefined();
		expect(worker.parentAddress).toEqual({ machineId: "laptop", sessionId: "orch" });
		expect(rows[0]!.node.rollup).toEqual({ ...emptyFleetRollup(), action: 1 });
		expect(laptop.rollup).toEqual({ ...emptyFleetRollup(), working: 1, action: 1 });
		expect(tree[0]!.rollup).toEqual(laptop.rollup);
	});

	it("keeps the child's own machine branch when it still holds other sessions", () => {
		const tree = buildFleetTree([
			scope("ao", "laptop", [session("orch", "working", { kind: "orchestrator" })]),
			scope("ao", "server", [
				session("worker", "working", { parentHostId: "laptop", parentSessionId: "orch" }),
				session("local", "idle"),
			]),
		]);
		const server = tree.find((project) => project.machines[0]?.machine.id === "server")!.machines[0]!;
		expect(server.sessions.map((node) => node.session.id)).toEqual(["local"]);
		expect(server.rollup).toEqual({ ...emptyFleetRollup(), working: 1 });
	});

	it("never links a cross-machine parent by a bare session ID", () => {
		const tree = buildFleetTree([
			scope("ao", "laptop", [session("orch", "working")]),
			scope("ao", "server", [
				session("orch", "working"),
				session("qualified", "working", { parentHostId: "laptop", parentSessionId: "missing" }),
				session("unqualified", "working", { parentSessionId: "orch-elsewhere" }),
			]),
			scope("docs", "laptop", [session("orch-elsewhere", "working")]),
		]);
		const server = tree.find((project) => project.machines[0]?.machine.id === "server")!.machines[0]!;
		const rows = flattenFleetSessions(server.sessions, new Set());
		expect(rows.map(({ node, depth }) => [node.session.id, depth, node.parentUnavailableReason])).toEqual([
			["orch", 0, undefined],
			["qualified", 0, "missing"],
			["unqualified", 0, "missing"],
		]);
	});

	it("keeps a child as a marked root when its parent's machine is not connected", () => {
		const offline = buildFleetTree([
			{ ...scope("ao", "laptop", [session("orch", "working")]), machine: { id: "laptop", name: "Laptop", reachable: false } },
			scope("ao", "server", [session("worker", "working", { parentHostId: "laptop", parentSessionId: "orch" })]),
		]);
		const worker = offline.find((project) => project.machines[0]?.machine.id === "server")!.machines[0]!.sessions[0]!;
		expect(worker.session.id).toBe("worker");
		expect(worker.parentUnavailable).toBe(true);
		expect(worker.parentUnavailableReason).toBe("machine-offline");

		const known = buildFleetTree(
			[scope("ao", "server", [session("worker", "working", { parentHostId: "laptop", parentSessionId: "orch" })])],
			{ knownMachines: [{ id: "laptop", reachable: false }] },
		);
		expect(known[0]!.machines[0]!.sessions[0]!.parentUnavailableReason).toBe("machine-offline");

		const reachableButEmpty = buildFleetTree(
			[scope("ao", "server", [session("worker", "working", { parentHostId: "laptop", parentSessionId: "orch" })])],
			{ knownMachines: [{ id: "laptop", reachable: true }] },
		);
		expect(reachableButEmpty[0]!.machines[0]!.sessions[0]!.parentUnavailableReason).toBe("missing");
	});

	it("keeps an archived cross-machine ancestor needed by a live child", () => {
		const tree = buildFleetTree([
			scope("ao", "laptop", [session("orch", "terminated", { isTerminated: true })]),
			scope("ao", "server", [session("worker", "working", { parentHostId: "laptop", parentSessionId: "orch" })]),
		]);
		const rows = flattenFleetSessions(tree[0]!.machines[0]!.sessions, new Set());
		expect(rows.map(({ node, depth }) => [node.session.id, depth])).toEqual([["orch", 0], ["worker", 1]]);
	});

	it("shows a cross-machine lineage cycle once", () => {
		const tree = buildFleetTree([
			scope("ao", "laptop", [session("a", "idle", { parentHostId: "server", parentSessionId: "b" })]),
			scope("ao", "server", [session("b", "idle", { parentHostId: "laptop", parentSessionId: "a" })]),
		]);
		const rows = tree.flatMap((project) => project.machines.flatMap((machine) =>
			flattenFleetSessions(machine.sessions, new Set())));
		expect(rows.map(({ node }) => node.session.id).sort()).toEqual(["a", "b"]);
		expect(rows.filter(({ node }) => node.parentUnavailable)).toHaveLength(1);
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

	it("starts large qualified branches collapsed and lets remembered choices override the default", () => {
		const sessions = Array.from({ length: 8 }, (_, index) =>
			session(`worker-${index}`, "working"));
		const project = buildFleetTree([scope("ao", "laptop", sessions)])[0]!;
		const machine = project.machines[0]!;

		const defaults = resolveFleetCollapsedKeys([project], {});
		expect(defaults).toEqual(new Set([project.key, machine.key]));

		const remembered = resolveFleetCollapsedKeys([project], {
			[project.key]: false,
			[machine.key]: true,
		});
		expect(remembered.has(project.key)).toBe(false);
		expect(remembered.has(machine.key)).toBe(true);
	});

	it("defaults a large manager branch without conflating equal session ids on another machine", () => {
		const manager = session("manager", "working");
		const children = Array.from({ length: 8 }, (_, index) =>
			session(`child-${index}`, "working", { parentSessionId: manager.id }));
		const tree = buildFleetTree([
			scope("ao", "laptop", [manager, ...children]),
			scope("ao", "server", [session("manager", "working")]),
		]);
		const laptopManager = tree[0]!.machines[0]!.sessions[0]!;
		const serverManager = tree[1]!.machines[0]!.sessions[0]!;

		const collapsed = resolveFleetCollapsedKeys(tree, {});
		expect(collapsed.has(laptopManager.key)).toBe(true);
		expect(collapsed.has(serverManager.key)).toBe(false);
		expect(laptopManager.key).not.toBe(serverManager.key);
	});

	it("accepts only bounded boolean collapse preferences from storage", () => {
		expect(parseFleetCollapsePreferences(JSON.stringify({ a: true, b: false, ignored: "yes" })))
			.toEqual({ a: true, b: false });
		expect(parseFleetCollapsePreferences("not json")).toEqual({});
		const restored = parseFleetCollapsePreferences(JSON.stringify(Object.fromEntries(
			Array.from({ length: 2_001 }, (_, index) => [String(index), true]),
		)));
		expect(Object.keys(restored)).toHaveLength(2_000);
		expect(restored["0"]).toBeUndefined();
		expect(restored["2000"]).toBe(true);
	});

	it("evicts the oldest stored branch when remembering a new choice", () => {
		const full = Object.fromEntries(Array.from({ length: 2_000 }, (_, index) => [String(index), true]));
		const remembered = rememberFleetCollapsePreference(full, "newest", false);
		expect(Object.keys(remembered)).toHaveLength(2_000);
		expect(remembered["0"]).toBeUndefined();
		expect(remembered.newest).toBe(false);
		expect(limitFleetCollapsePreferences({ ...full, newest: false })).toEqual(remembered);
	});
});
