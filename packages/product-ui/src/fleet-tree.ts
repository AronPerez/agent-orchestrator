import type { SessionStatus } from "./session-models";
import { attentionZone, type AttentionZone } from "./session-presentation";

export type FleetTreeSession = {
	id: string;
	parentSessionId?: string;
	kind?: "orchestrator" | "worker";
	status: SessionStatus;
	/** Delivery/SCM state to aggregate while `status` keeps the live agent state. */
	rollupStatus?: SessionStatus;
	isTerminated?: boolean;
};

export type FleetTreeScope<Session extends FleetTreeSession> = {
	project: { id: string; name: string };
	machine: { id: string; name: string; reachable: boolean };
	/** Trusted cross-host association. Omit for host-local project identities. */
	projectLinkId?: string;
	sessions: readonly Session[];
};

export type FleetRollup = Record<AttentionZone, number>;
export type FleetSessionRole = "orchestrator" | "manager" | "worker";

export type FleetSessionNode<Session extends FleetTreeSession> = {
	type: "session";
	key: string;
	projectId: string;
	machineId: string;
	session: Session;
	role: FleetSessionRole;
	parentUnavailable: boolean;
	zone: AttentionZone;
	/** Descendants only; the session's own state is in `zone`. */
	rollup: FleetRollup;
	children: FleetSessionNode<Session>[];
};

export type FleetMachineNode<Session extends FleetTreeSession> = {
	type: "machine";
	key: string;
	projectId: string;
	machine: FleetTreeScope<Session>["machine"];
	/** Every included session on this machine. */
	rollup: FleetRollup;
	sessions: FleetSessionNode<Session>[];
};

export type FleetProjectNode<Session extends FleetTreeSession> = {
	type: "project";
	key: string;
	project: FleetTreeScope<Session>["project"];
	projectLinkId?: string;
	/** Every included session in this project, across machines. */
	rollup: FleetRollup;
	machines: FleetMachineNode<Session>[];
};

export type FlatFleetSession<Session extends FleetTreeSession> = {
	node: FleetSessionNode<Session>;
	depth: number;
};

/** A person's explicit choice for a qualified branch. Absent keys use the size default. */
export type FleetCollapsePreferences = Readonly<Record<string, boolean>>;

/** Eight visible sessions already fill most of a compact laptop or phone list. */
export const DEFAULT_FLEET_COLLAPSE_SIZE = 8;

const MAX_FLEET_COLLAPSE_PREFERENCES = 2_000;
const MAX_FLEET_COLLAPSE_KEY_LENGTH = 1_024;

/** Keep the newest valid preferences within the storage contract's hard limit. */
export function limitFleetCollapsePreferences(
	preferences: Readonly<Record<string, unknown>>,
): Record<string, boolean> {
	const valid = Object.entries(preferences).filter(([key, value]) =>
		key.length <= MAX_FLEET_COLLAPSE_KEY_LENGTH && typeof value === "boolean");
	return Object.fromEntries(valid.slice(-MAX_FLEET_COLLAPSE_PREFERENCES)) as Record<string, boolean>;
}

/** Remember one choice as the newest entry, evicting the oldest choice when full. */
export function rememberFleetCollapsePreference(
	preferences: FleetCollapsePreferences,
	key: string,
	collapsed: boolean,
): Record<string, boolean> {
	const next = { ...preferences };
	delete next[key];
	next[key] = collapsed;
	return limitFleetCollapsePreferences(next);
}

export function emptyFleetRollup(): FleetRollup {
	return { merge: 0, action: 0, pending: 0, working: 0, done: 0 };
}

function addRollup(target: FleetRollup, source: FleetRollup): void {
	for (const zone of Object.keys(target) as AttentionZone[]) target[zone] += source[zone];
}

function addSession(target: FleetRollup, node: FleetSessionNode<FleetTreeSession>): void {
	target[node.zone] += 1;
	addRollup(target, node.rollup);
}

// JSON keys keep arbitrary host/project/session IDs unambiguous and give the
// later cross-machine edge work a stable target without defining its protocol.
export const fleetProjectKey = (projectId: string, machineId: string): string =>
	JSON.stringify(["project", machineId, projectId]);
export const fleetLinkedProjectKey = (projectLinkId: string): string =>
	JSON.stringify(["linked-project", projectLinkId]);
export const fleetMachineKey = (projectId: string, machineId: string): string =>
	JSON.stringify(["machine", projectId, machineId]);
export const fleetSessionKey = (projectId: string, machineId: string, sessionId: string): string =>
	JSON.stringify(["session", projectId, machineId, sessionId]);

type MachineGroup<Session extends FleetTreeSession> = {
	projectId: string;
	machine: FleetTreeScope<Session>["machine"];
	sessions: Map<string, Session>;
};

type ProjectGroup<Session extends FleetTreeSession> = {
	key: string;
	project: FleetTreeScope<Session>["project"];
	projectLinkId?: string;
	machines: Map<string, MachineGroup<Session>>;
};

export function buildFleetTree<Session extends FleetTreeSession>(
	scopes: readonly FleetTreeScope<Session>[],
	options: { showArchived?: boolean } = {},
): FleetProjectNode<Session>[] {
	const projects = new Map<string, ProjectGroup<Session>>();
	for (const scope of scopes) {
		const projectKey = scope.projectLinkId
			? fleetLinkedProjectKey(scope.projectLinkId)
			: fleetProjectKey(scope.project.id, scope.machine.id);
		let project = projects.get(projectKey);
		if (!project) {
			project = {
				key: projectKey,
				project: scope.project,
				projectLinkId: scope.projectLinkId,
				machines: new Map(),
			};
			projects.set(projectKey, project);
		}
		const machineKey = fleetMachineKey(scope.project.id, scope.machine.id);
		let machine = project.machines.get(machineKey);
		if (!machine) {
			machine = { projectId: scope.project.id, machine: scope.machine, sessions: new Map() };
			project.machines.set(machineKey, machine);
		}
		for (const item of scope.sessions) machine.sessions.set(item.id, item);
	}

	return [...projects.values()].map(({ key, project, projectLinkId, machines }) => {
		const machineNodes = [...machines.values()].map(({ projectId, machine, sessions }) =>
			buildMachine(projectId, machine, [...sessions.values()], options.showArchived === true));
		const rollup = emptyFleetRollup();
		for (const node of machineNodes) addRollup(rollup, node.rollup);
		return {
			type: "project" as const,
			key,
			project,
			projectLinkId,
			rollup,
			machines: machineNodes,
		};
	});
}

function buildMachine<Session extends FleetTreeSession>(
	projectId: string,
	machine: FleetTreeScope<Session>["machine"],
	sessions: Session[],
	showArchived: boolean,
): FleetMachineNode<Session> {
	const byId = new Map(sessions.map((item) => [item.id, item]));
	const children = new Map<string, Session[]>();
	for (const item of sessions) {
		if (!item.parentSessionId || item.parentSessionId === item.id || !byId.has(item.parentSessionId)) continue;
		const siblings = children.get(item.parentSessionId) ?? [];
		siblings.push(item);
		children.set(item.parentSessionId, siblings);
	}

	const included = new Set<string>();
	for (const item of sessions) {
		if (!showArchived && (item.isTerminated || item.status === "terminated")) continue;
		let current: Session | undefined = item;
		while (current && !included.has(current.id)) {
			included.add(current.id);
			current = byId.get(current.parentSessionId ?? "");
		}
	}

	const visited = new Set<string>();
	const visit = (item: Session, fallbackRoot = false): FleetSessionNode<Session> | undefined => {
		if (visited.has(item.id) || !included.has(item.id)) return undefined;
		visited.add(item.id);
		const parentId = item.parentSessionId;
		const recordedChildren = children.get(item.id) ?? [];
		const directChildren = recordedChildren.filter((child) => included.has(child.id));
		const childNodes = directChildren.flatMap((child) => {
			const node = visit(child);
			return node ? [node] : [];
		});
		const rollup = emptyFleetRollup();
		for (const child of childNodes) addSession(rollup, child);
		return {
			type: "session",
			key: fleetSessionKey(projectId, machine.id, item.id),
			projectId,
			machineId: machine.id,
			session: item,
			role: item.kind === "orchestrator" ? "orchestrator" : recordedChildren.length ? "manager" : "worker",
			parentUnavailable: parentId !== undefined &&
				(fallbackRoot || parentId === item.id || !byId.has(parentId)),
			zone: item.isTerminated || item.status === "terminated" ? "done" : attentionZone(item.rollupStatus ?? item.status),
			rollup,
			children: childNodes,
		};
	};

	const roots = sessions.filter((item) =>
		included.has(item.id) &&
		(!item.parentSessionId || !byId.has(item.parentSessionId) || item.parentSessionId === item.id));
	const rootNodes = [
		...roots.filter((item) => item.kind === "orchestrator"),
		...roots.filter((item) => item.kind !== "orchestrator"),
	].flatMap((item) => {
		const node = visit(item);
		return node ? [node] : [];
	});
	// Imported cycles have no root. Start at the first unvisited member and mark
	// that broken link unavailable so every session remains visible exactly once.
	for (const item of sessions) {
		const node = visit(item, true);
		if (node) rootNodes.push(node);
	}

	const rollup = emptyFleetRollup();
	for (const node of rootNodes) addSession(rollup, node);
	return {
		type: "machine",
		key: fleetMachineKey(projectId, machine.id),
		projectId,
		machine,
		rollup,
		sessions: rootNodes,
	};
}

export function flattenFleetSessions<Session extends FleetTreeSession>(
	roots: readonly FleetSessionNode<Session>[],
	collapsed: ReadonlySet<string>,
): FlatFleetSession<Session>[] {
	const rows: FlatFleetSession<Session>[] = [];
	const stack = [...roots].reverse().map((node) => ({ node, depth: 0 }));
	while (stack.length) {
		const row = stack.pop()!;
		rows.push(row);
		if (collapsed.has(row.node.key)) continue;
		for (const child of [...row.node.children].reverse()) stack.push({ node: child, depth: row.depth + 1 });
	}
	return rows;
}

/**
 * Resolve the branches that should be folded now.
 *
 * Large branches start folded so their roll-up remains the useful first view.
 * A stored boolean is an override rather than merely a list of collapsed keys:
 * `false` remembers that someone deliberately opened a branch which defaults
 * to folded.
 */
export function resolveFleetCollapsedKeys<Session extends FleetTreeSession>(
	projects: readonly FleetProjectNode<Session>[],
	preferences: FleetCollapsePreferences,
	defaultSize = DEFAULT_FLEET_COLLAPSE_SIZE,
): Set<string> {
	const collapsed = new Set<string>();
	const resolve = (key: string, sessionCount: number) => {
		const remembered = preferences[key];
		if (remembered ?? sessionCount >= defaultSize) collapsed.add(key);
	};
	const visitSession = (node: FleetSessionNode<Session>): number => {
		const count = 1 + node.children.reduce((total, child) => total + visitSession(child), 0);
		if (node.children.length) resolve(node.key, count);
		return count;
	};
	for (const project of projects) {
		let projectCount = 0;
		for (const machine of project.machines) {
			const machineCount = machine.sessions.reduce((total, session) => total + visitSession(session), 0);
			projectCount += machineCount;
			if (machine.sessions.length) resolve(machine.key, machineCount);
		}
		if (project.machines.length) resolve(project.key, projectCount);
	}
	return collapsed;
}

/** Parse untrusted client storage without allowing it to grow the view model unboundedly. */
export function parseFleetCollapsePreferences(raw: string | null | undefined): Record<string, boolean> {
	if (!raw) return {};
	try {
		const parsed: unknown = JSON.parse(raw);
		if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
		return limitFleetCollapsePreferences(parsed as Record<string, unknown>);
	} catch {
		return {};
	}
}
