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

function orchestrator(id: string, hostId: string, projectId: string): HostedOrchestrator {
	return {
		id,
		projectId,
		projectName: "Project",
		hostId,
		hostName: hostId === "mac" ? "Mac" : "Build box",
		status: "working",
		mode: "chat",
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
});
