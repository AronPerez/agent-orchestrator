import { describe, expect, it } from "vitest";
import { flattenFleetSessions } from "../../product-ui/src/fleet-tree";
import type { DashboardSession, OrchestratorLink } from "./api";
import { buildMobileFleetTree } from "./fleet-tree-model";
import type { HostedOrchestrator, HostedProject, HostedSession } from "./hostedRows";

function worker(id: string, hostId: string, projectId: string, over: Partial<DashboardSession> = {}): HostedSession {
	return {
		id,
		projectId,
		hostId,
		hostName: hostId === "mac" ? "Mac" : "Build box",
		status: "working",
		mode: "chat",
		branch: null,
		issueId: null,
		issueTitle: null,
		userPrompt: null,
		displayName: id,
		summary: null,
		createdAt: "2026-11-17T12:00:00Z",
		lastActivityAt: "2026-11-17T12:00:00Z",
		...over,
	};
}

function orchestrator(
	id: string,
	hostId: string,
	projectId: string,
	over: Partial<OrchestratorLink> = {},
): HostedOrchestrator {
	return {
		id,
		projectId,
		projectName: "Project",
		hostId,
		hostName: hostId === "mac" ? "Mac" : "Build box",
		status: "working",
		mode: "chat",
		...over,
	} satisfies OrchestratorLink & { hostId: string; hostName: string };
}

function project(hostId: string): HostedProject {
	return { id: "same", name: "Project", hostId, hostName: hostId === "mac" ? "Mac" : "Build box" };
}

describe("buildMobileFleetTree", () => {
	it("builds host-local project, machine, orchestrator, manager and worker branches", () => {
		const tree = buildMobileFleetTree({
			projects: [project("mac"), project("box")],
			sessions: [
				worker("manager", "mac", "same", { parentSessionId: "orch" }),
				worker("child", "mac", "same", { parentSessionId: "manager", status: "needs_input" }),
				worker("orphan", "box", "same", { parentSessionId: "deleted", status: "future_status" }),
			],
			orchestrators: [orchestrator("orch", "mac", "same")],
			hosts: [
				{ hostId: "mac", name: "Mac", connection: "open" },
				{ hostId: "box", name: "Build box", connection: "closed" },
			],
		});

		expect(tree).toHaveLength(2);
		expect(tree[0]!.key).not.toBe(tree[1]!.key);
		const local = tree.find((item) => item.machines[0]?.machine.id === "mac")!.machines[0]!;
		const remote = tree.find((item) => item.machines[0]?.machine.id === "box")!.machines[0]!;
		expect(local.machine.reachable).toBe(true);
		expect(remote.machine.reachable).toBe(false);
		expect(flattenFleetSessions(local.sessions, new Set()).map(({ node, depth }) => [node.session.id, node.role, depth]))
			.toEqual([
				["orch", "orchestrator", 0],
				["manager", "manager", 1],
				["child", "worker", 2],
			]);
		expect(local.rollup).toMatchObject({ action: 1, working: 2 });
		expect(remote.sessions[0]).toMatchObject({ parentUnavailable: true });
		expect(remote.sessions[0]!.session.status).toBe("unknown");
	});

	it("raises folded rollups for failing checks and requested changes", () => {
		const tree = buildMobileFleetTree({
			projects: [project("mac")],
			sessions: [
				worker("failing", "mac", "same", {
					prs: [{ number: 1, url: "u", state: "open", ciStatus: "failing" }],
				}),
				worker("changes", "mac", "same", {
					prs: [{ number: 2, url: "u", state: "open", reviewDecision: "changes_requested" }],
				}),
			],
			orchestrators: [],
			hosts: [{ hostId: "mac", name: "Mac", connection: "open" }],
		});

		expect(tree[0]!.rollup).toMatchObject({ action: 2, working: 0 });
	});

	it("does not join a cross-host parent to a colliding local session", () => {
		const tree = buildMobileFleetTree({
			projects: [project("box")],
			sessions: [
				worker("parent", "box", "same"),
				worker("child", "box", "same", { parentHostId: "mac", parentSessionId: "parent" }),
			],
			orchestrators: [],
			hosts: [{ hostId: "box", name: "Build box", connection: "open" }],
		});

		const rows = flattenFleetSessions(tree[0]!.machines[0]!.sessions, new Set());
		expect(rows.map(({ node, depth }) => [node.session.id, depth])).toEqual([
			["parent", 0],
			["child", 0],
		]);
		expect(rows[1]!.node).toMatchObject({
			parentUnavailable: true,
			parentAddress: { machineId: "mac", sessionId: "parent" },
		});
	});

	it("preserves a cross-host parent address for an orchestrator child", () => {
		const tree = buildMobileFleetTree({
			projects: [project("box")],
			sessions: [worker("parent", "box", "same")],
			orchestrators: [orchestrator("child", "box", "same", {
				parentHostId: "mac",
				parentSessionId: "parent",
			})],
			hosts: [{ hostId: "box", name: "Build box", connection: "open" }],
		});

		const child = flattenFleetSessions(tree[0]!.machines[0]!.sessions, new Set())
			.find(({ node }) => node.session.id === "child")!.node;
		expect(child).toMatchObject({
			parentUnavailable: true,
			parentAddress: { machineId: "mac", sessionId: "parent" },
			role: "orchestrator",
		});
	});

	it("draws a cross-host worker under its parent and drops the emptied branch", () => {
		const tree = buildMobileFleetTree({
			projects: [project("mac"), project("box")],
			sessions: [
				worker("child", "box", "same", { parentHostId: "mac", parentSessionId: "parent" }),
			],
			orchestrators: [orchestrator("parent", "mac", "same")],
			hosts: [
				{ hostId: "mac", name: "Mac", connection: "open" },
				{ hostId: "box", name: "Build box", connection: "open" },
			],
		});

		expect(tree.map((item) => item.machines.map((machine) => machine.machine.id))).toEqual([["mac"]]);
		const rows = flattenFleetSessions(tree[0]!.machines[0]!.sessions, new Set());
		expect(rows.map(({ node, depth }) => [node.session.id, node.machineId, depth])).toEqual([
			["parent", "mac", 0],
			["child", "box", 1],
		]);
	});

	it("reports an offline parent host even when it contributes no cached rows", () => {
		const tree = buildMobileFleetTree({
			projects: [project("box")],
			sessions: [worker("child", "box", "same", { parentHostId: "mac", parentSessionId: "parent" })],
			orchestrators: [],
			hosts: [
				{ hostId: "mac", name: "Mac", connection: "closed" },
				{ hostId: "box", name: "Build box", connection: "open" },
			],
		});

		expect(tree[0]!.machines[0]!.sessions[0]).toMatchObject({
			parentUnavailable: true,
			parentUnavailableReason: "machine-offline",
		});
	});

	it("omits mobile scopes containing only hidden archived sessions", () => {
		const tree = buildMobileFleetTree({
			projects: [project("mac")],
			sessions: [worker("archived", "mac", "same", { status: "terminated", isTerminated: true })],
			orchestrators: [],
			hosts: [{ hostId: "mac", name: "Mac", connection: "open" }],
		});

		expect(tree).toEqual([]);
	});

	it("keeps a disconnected but non-terminal cloud orchestrator live", () => {
		const tree = buildMobileFleetTree({
			projects: [project("mac")],
			sessions: [],
			orchestrators: [orchestrator("orch", "mac", "same", { hasRuntime: false, isTerminal: false })],
			hosts: [{ hostId: "mac", name: "Mac", connection: "connecting" }],
		});

		expect(tree[0]!.machines[0]!.sessions[0]!.session).toMatchObject({ id: "orch", isTerminated: false });
		expect(tree[0]!.rollup).toMatchObject({ working: 1, done: 0 });
	});
});
