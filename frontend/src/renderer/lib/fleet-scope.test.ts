import { buildFleetTree, type FleetTreeScope, type FleetTreeSession } from "@aoagents/product-ui";
import { describe, expect, it } from "vitest";
import { scopeFleetTree } from "./fleet-scope";

type Session = FleetTreeSession & { title: string };

const session = (id: string, overrides: Partial<Session> = {}): Session => ({
	id,
	title: id,
	status: "working",
	kind: "worker",
	...overrides,
});
const scope = (projectId: string, machineId: string, sessions: Session[], reachable = true): FleetTreeScope<Session> => ({
	project: { id: projectId, name: projectId.toUpperCase() },
	machine: { id: machineId, name: machineId, reachable },
	sessions,
});
const titles = (nodes: { session: Session; children: unknown[] }[]): unknown[] =>
	nodes.map((node) => node.children.length
		? [node.session.title, titles(node.children as { session: Session; children: unknown[] }[])]
		: node.session.title);

describe("scopeFleetTree", () => {
	it("keeps exactly one project on one machine, by ID and machine, never by name", () => {
		const scopes = [
			scope("alpha", "local", [session("orch", { kind: "orchestrator" }), session("w1", { parentSessionId: "orch" })]),
			scope("alpha", "m4", [session("orch", { kind: "orchestrator", title: "M4 orchestrator" })]),
			{ ...scope("beta", "local", [session("other")]), project: { id: "beta", name: "ALPHA" } },
		];
		const { projects, adoptedParents } = scopeFleetTree(buildFleetTree(scopes), scopes[0]!);

		expect(projects).toHaveLength(1);
		expect(projects[0]!.project).toEqual({ id: "alpha", name: "ALPHA" });
		expect(projects[0]!.machines.map((machine) => machine.machine.id)).toEqual(["local"]);
		expect(titles(projects[0]!.machines[0]!.sessions)).toEqual([["orch", ["w1"]]]);
		expect(projects[0]!.rollup.working).toBe(2);
		expect(adoptedParents.size).toBe(0);

		const remote = scopeFleetTree(buildFleetTree(scopes), scopes[1]!);
		expect(titles(remote.projects[0]!.machines[0]!.sessions)).toEqual(["M4 orchestrator"]);
	});

	it("keeps a child this project spawned on another machine nested under its parent", () => {
		const scopes = [
			scope("alpha", "local", [session("orch", { kind: "orchestrator" })]),
			scope("alpha-m4", "m4", [session("remote-worker", { parentHostId: "local", parentSessionId: "orch", status: "needs_input" })]),
		];
		const { projects } = scopeFleetTree(buildFleetTree(scopes), scopes[0]!);
		const [orchestrator] = projects[0]!.machines[0]!.sessions;

		expect(titles([orchestrator!])).toEqual([["orch", ["remote-worker"]]]);
		expect(orchestrator!.children[0]!.machineId).toBe("m4");
		expect(projects[0]!.rollup.action).toBe(1);
	});

	it("adopts this project's sessions whose parent is drawn on another machine, with their subtrees", () => {
		const scopes = [
			scope("alpha", "m6", [session("orch", { kind: "orchestrator" })]),
			scope("alpha", "m4", [
				session("aide", { parentHostId: "m6", parentSessionId: "orch" }),
				session("aide-child", { parentSessionId: "aide", status: "review_pending" }),
				session("local-root"),
			]),
		];
		const { projects, adoptedParents } = scopeFleetTree(buildFleetTree(scopes), scopes[1]!);
		const machine = projects[0]!.machines[0]!;

		expect(machine.key).toBe(JSON.stringify(["machine", "alpha", "m4"]));
		expect(titles(machine.sessions)).toEqual(["local-root", ["aide", ["aide-child"]]]);
		expect(adoptedParents.get(machine.sessions[1]!.key)?.session.title).toBe("orch");
		expect(machine.rollup).toMatchObject({ working: 2, pending: 1 });
		expect(projects[0]!.rollup).toEqual(machine.rollup);
	});

	it("still draws a project whose every session hangs under a parent elsewhere", () => {
		const scopes = [
			scope("alpha", "m6", [session("orch", { kind: "orchestrator" })]),
			scope("alpha", "m4", [session("aide", { parentHostId: "m6", parentSessionId: "orch" })]),
		];
		const tree = buildFleetTree(scopes);
		// The whole-fleet tree has no branch of its own for alpha on m4.
		expect(tree.some((project) => project.machines.some((machine) => machine.machine.id === "m4"))).toBe(false);

		const { projects, adoptedParents } = scopeFleetTree(tree, scopes[1]!);
		expect(projects[0]!.machines[0]!.machine).toEqual({ id: "m4", name: "m4", reachable: true });
		expect(titles(projects[0]!.machines[0]!.sessions)).toEqual(["aide"]);
		expect(adoptedParents.size).toBe(1);
	});

	it("gives an empty or unloaded project one machine with no sessions", () => {
		const empty = scope("alpha", "local", []);
		const loaded = scopeFleetTree(buildFleetTree([empty, scope("beta", "local", [session("x")])]), empty);
		expect(loaded.projects[0]!.machines[0]!.sessions).toEqual([]);
		expect(loaded.projects[0]!.rollup).toEqual({ merge: 0, action: 0, pending: 0, working: 0, done: 0 });

		const missing = scopeFleetTree(buildFleetTree([scope("beta", "local", [session("x")])]), empty);
		expect(missing.projects).toHaveLength(1);
		expect(missing.projects[0]!.machines[0]!.sessions).toEqual([]);
	});

	it("does not adopt across an offline machine, where the tree keeps the child as a marked root", () => {
		const scopes = [
			scope("alpha", "m6", [session("orch", { kind: "orchestrator" })], false),
			scope("alpha", "m4", [session("aide", { parentHostId: "m6", parentSessionId: "orch" })]),
		];
		const { projects, adoptedParents } = scopeFleetTree(buildFleetTree(scopes), scopes[1]!);
		const [aide] = projects[0]!.machines[0]!.sessions;
		expect(aide!.parentUnavailableReason).toBe("machine-offline");
		expect(adoptedParents.size).toBe(0);
	});
});
