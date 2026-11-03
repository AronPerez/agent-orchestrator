import type { WorkspaceSession } from "../types/workspace";

export type FleetRow = {
	session: WorkspaceSession;
	depth: number;
	childCount: number;
	role: string;
	parentUnavailable: boolean;
};

/** Spawn lineage only: names, prompts and report recipients never establish a parent. */
export function fleetRows(
	sessions: WorkspaceSession[],
	collapsed: ReadonlySet<string>,
	showArchived = false,
): FleetRow[] {
	const byId = new Map(sessions.map((session) => [session.id, session]));
	const children = new Map<string, WorkspaceSession[]>();
	for (const session of sessions) {
		if (session.parentSessionId && session.parentSessionId !== session.id && byId.has(session.parentSessionId)) {
			const siblings = children.get(session.parentSessionId) ?? [];
			siblings.push(session);
			children.set(session.parentSessionId, siblings);
		}
	}
	const included = new Set<string>();
	for (const session of sessions) {
		if (!showArchived && (session.isTerminated || session.status === "terminated")) continue;
		let current: WorkspaceSession | undefined = session;
		while (current && !included.has(current.id)) {
			included.add(current.id);
			current = byId.get(current.parentSessionId ?? "");
		}
	}
	const rows: FleetRow[] = [];
	const visited = new Set<string>();
	function visit(root: WorkspaceSession) {
		const stack = [{ session: root, depth: 0, hidden: false }];
		while (stack.length) {
			const { session, depth, hidden } = stack.pop()!;
			if (visited.has(session.id) || !included.has(session.id)) continue;
			visited.add(session.id);
			const descendants = children.get(session.id) ?? [];
			if (!hidden)
				rows.push({
					session,
					depth,
					childCount: descendants.filter((child) => included.has(child.id)).length,
					role: session.kind === "orchestrator" ? "Orchestrator" : descendants.length ? "Manager" : "Worker",
					parentUnavailable: !!session.parentSessionId && (!byId.has(session.parentSessionId) || depth === 0),
				});
			for (const child of [...descendants].reverse())
				stack.push({ session: child, depth: depth + 1, hidden: hidden || collapsed.has(session.id) });
		}
	}
	const roots = sessions.filter(
		(session) =>
			!session.parentSessionId || !byId.has(session.parentSessionId) || session.parentSessionId === session.id,
	);
	roots.filter((session) => session.kind === "orchestrator").forEach(visit);
	roots.filter((session) => session.kind !== "orchestrator").forEach(visit);
	// Corrupt or imported cycles must not hide sessions or recurse forever.
	sessions.forEach(visit);
	return rows;
}
