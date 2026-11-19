import type { SessionStatus } from "./session-models";
import { attentionZone, type AttentionZone } from "./session-presentation";

export type FleetTreeSession = {
	id: string;
	/** Stable machine ID owning parentSessionId when lineage is host-qualified. */
	parentHostId?: string;
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
export type FleetSessionAddress = { machineId: string; sessionId: string };
/**
 * Why a recorded parent is not drawn above a session:
 * - `machine-unknown`: no scope or known machine has the parent's machine ID.
 * - `machine-offline`: the parent's machine is known but not connected.
 * - `missing`: no single drawable session has that exact address (absent,
 *   ambiguous, self-parented, or a broken lineage cycle).
 */
export type FleetParentUnavailableReason = "missing" | "machine-offline" | "machine-unknown";
/** A paired machine that may contribute no scope (for example, offline with no cache). */
export type FleetKnownMachine = { id: string; reachable: boolean };

export type FleetSessionNode<Session extends FleetTreeSession> = {
	type: "session";
	key: string;
	projectId: string;
	machineId: string;
	session: Session;
	/** Stable address for a local or cross-machine spawn parent. */
	parentAddress?: FleetSessionAddress;
	role: FleetSessionRole;
	parentUnavailable: boolean;
	/** Set exactly when `parentUnavailable` is true. */
	parentUnavailableReason?: FleetParentUnavailableReason;
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
	/**
	 * Every session drawn under this machine branch. A child whose parent is on
	 * another machine is drawn (and counted) under that parent instead.
	 */
	rollup: FleetRollup;
	sessions: FleetSessionNode<Session>[];
};

export type FleetProjectNode<Session extends FleetTreeSession> = {
	type: "project";
	key: string;
	project: FleetTreeScope<Session>["project"];
	projectLinkId?: string;
	/** Every session drawn under this project, across machines. */
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

// JSON keys keep arbitrary host/project/session IDs unambiguous. A session keeps
// its own qualified key even when it is drawn under a parent on another machine.
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

type SessionEntry<Session extends FleetTreeSession> = {
	group: MachineGroup<Session>;
	session: Session;
};

type ParentResolution<Session extends FleetTreeSession> = {
	parent?: SessionEntry<Session>;
	reason?: FleetParentUnavailableReason;
};

const fleetAddressKey = (machineId: string, sessionId: string): string => JSON.stringify([machineId, sessionId]);
const isArchived = (item: FleetTreeSession): boolean => Boolean(item.isTerminated) || item.status === "terminated";

export function buildFleetTree<Session extends FleetTreeSession>(
	scopes: readonly FleetTreeScope<Session>[],
	options: { showArchived?: boolean; knownMachines?: readonly FleetKnownMachine[] } = {},
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

	// Every edge resolves by its exact (machine, session) address across the
	// whole fleet. A bare session ID is only ever read as local to its own row.
	const groups = [...projects.values()].flatMap((project) => [...project.machines.values()]);
	const reachable = new Map((options.knownMachines ?? []).map((machine) => [machine.id, machine.reachable]));
	const byGroup = new Map<MachineGroup<Session>, Map<string, SessionEntry<Session>>>();
	const byAddress = new Map<string, SessionEntry<Session>[]>();
	for (const group of groups) {
		reachable.set(group.machine.id, group.machine.reachable);
		const entries = new Map<string, SessionEntry<Session>>();
		for (const item of group.sessions.values()) {
			const entry = { group, session: item };
			entries.set(item.id, entry);
			const address = fleetAddressKey(group.machine.id, item.id);
			byAddress.set(address, [...(byAddress.get(address) ?? []), entry]);
		}
		byGroup.set(group, entries);
	}
	const entries = groups.flatMap((group) => [...byGroup.get(group)!.values()]);

	const resolve = (entry: SessionEntry<Session>): ParentResolution<Session> => {
		const { session: item, group } = entry;
		if (item.parentSessionId === undefined) return {};
		const machineId = item.parentHostId || group.machine.id;
		if (machineId === group.machine.id) {
			const sibling = byGroup.get(group)!.get(item.parentSessionId);
			if (sibling) return sibling === entry ? { reason: "missing" } : { parent: sibling };
		} else if (!reachable.has(machineId)) {
			return { reason: "machine-unknown" };
		} else if (!reachable.get(machineId)) {
			// Cached rows from a disconnected machine may be stale; do not draw edges into them.
			return { reason: "machine-offline" };
		}
		const matches = (byAddress.get(fleetAddressKey(machineId, item.parentSessionId)) ?? [])
			.filter((candidate) => candidate !== entry);
		return matches.length === 1 ? { parent: matches[0] } : { reason: "missing" };
	};
	const resolved = new Map(entries.map((entry) => [entry, resolve(entry)]));
	const children = new Map<SessionEntry<Session>, SessionEntry<Session>[]>();
	for (const entry of entries) {
		const parent = resolved.get(entry)!.parent;
		if (parent) children.set(parent, [...(children.get(parent) ?? []), entry]);
	}

	const showArchived = options.showArchived === true;
	const included = new Set<SessionEntry<Session>>();
	for (const entry of entries) {
		if (!showArchived && isArchived(entry.session)) continue;
		let current: SessionEntry<Session> | undefined = entry;
		while (current && !included.has(current)) {
			included.add(current);
			current = resolved.get(current)!.parent;
		}
	}

	const visited = new Set<SessionEntry<Session>>();
	const visit = (entry: SessionEntry<Session>, fallbackRoot = false): FleetSessionNode<Session> | undefined => {
		if (visited.has(entry) || !included.has(entry)) return undefined;
		visited.add(entry);
		const { session: item, group } = entry;
		const { parent, reason } = resolved.get(entry)!;
		const recordedChildren = children.get(entry) ?? [];
		const childNodes = recordedChildren.flatMap((child) => {
			const node = visit(child);
			return node ? [node] : [];
		});
		const rollup = emptyFleetRollup();
		for (const child of childNodes) addSession(rollup, child);
		const parentUnavailable = item.parentSessionId !== undefined && (fallbackRoot || !parent);
		return {
			type: "session",
			key: fleetSessionKey(group.projectId, group.machine.id, item.id),
			projectId: group.projectId,
			machineId: group.machine.id,
			session: item,
			parentAddress: item.parentSessionId ? {
				machineId: item.parentHostId || group.machine.id,
				sessionId: item.parentSessionId,
			} : undefined,
			role: item.kind === "orchestrator" ? "orchestrator" : recordedChildren.length ? "manager" : "worker",
			parentUnavailable,
			parentUnavailableReason: parentUnavailable ? reason ?? "missing" : undefined,
			zone: isArchived(item) ? "done" : attentionZone(item.rollupStatus ?? item.status),
			rollup,
			children: childNodes,
		};
	};

	const rootsByGroup = new Map(groups.map((group) => {
		const roots = [...byGroup.get(group)!.values()]
			.filter((entry) => included.has(entry) && !resolved.get(entry)!.parent);
		const nodes = [
			...roots.filter((entry) => entry.session.kind === "orchestrator"),
			...roots.filter((entry) => entry.session.kind !== "orchestrator"),
		].flatMap((entry) => {
			const node = visit(entry);
			return node ? [node] : [];
		});
		return [group, nodes] as const;
	}));
	// Imported cycles have no root. Only after every real root has claimed its
	// descendants, start at the first unvisited member and mark that broken link
	// unavailable so every session remains visible exactly once.
	for (const group of groups) {
		for (const entry of byGroup.get(group)!.values()) {
			const node = visit(entry, true);
			if (node) rootsByGroup.get(group)!.push(node);
		}
	}

	return [...projects.values()].flatMap(({ key, project, projectLinkId, machines }) => {
		const machineNodes = [...machines.values()].flatMap((group) => {
			const sessions = rootsByGroup.get(group)!;
			// A branch whose every visible session is drawn under a parent on another
			// machine has nothing of its own left to show.
			const drawnElsewhere = !sessions.length &&
				[...byGroup.get(group)!.values()].some((entry) => included.has(entry));
			if (drawnElsewhere) return [];
			const rollup = emptyFleetRollup();
			for (const node of sessions) addSession(rollup, node);
			return [{
				type: "machine" as const,
				key: fleetMachineKey(group.projectId, group.machine.id),
				projectId: group.projectId,
				machine: group.machine,
				rollup,
				sessions,
			}];
		});
		if (machines.size && !machineNodes.length) return [];
		const rollup = emptyFleetRollup();
		for (const node of machineNodes) addRollup(rollup, node.rollup);
		return [{
			type: "project" as const,
			key,
			project,
			projectLinkId,
			rollup,
			machines: machineNodes,
		}];
	});
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
