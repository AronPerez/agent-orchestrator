import {
	emptyFleetRollup,
	fleetMachineKey,
	fleetProjectKey,
	type FleetMachineNode,
	type FleetProjectNode,
	type FleetRollup,
	type FleetSessionNode,
	type FleetTreeScope,
	type FleetTreeSession,
} from "@aoagents/product-ui";

export type FleetScopeTarget<Session extends FleetTreeSession> = Pick<FleetTreeScope<Session>, "project" | "machine">;

export type ScopedFleet<Session extends FleetTreeSession> = {
	/** Exactly one project with exactly one machine: the scope. */
	projects: FleetProjectNode<Session>[];
	/**
	 * Sessions of the scope that the whole-fleet tree drew under a parent in
	 * another project or on another machine, keyed by session key, with that parent.
	 * The scoped tree shows them as its own roots.
	 */
	adoptedParents: Map<string, FleetSessionNode<Session>>;
};

function addSession(target: FleetRollup, node: FleetSessionNode<FleetTreeSession>): void {
	target[node.zone] += 1;
	for (const zone of Object.keys(target) as (keyof FleetRollup)[]) target[zone] += node.rollup[zone];
}

/**
 * Narrow a whole-fleet tree to one project on one machine, as a project's own
 * Fleet tab shows it.
 *
 * Identity is the exact (project ID, machine ID) pair, so the same project name
 * or ID on another machine stays out. The tree must be built from the whole
 * fleet first: a child this project spawned on another machine then stays
 * nested under its parent here, and a session of this project whose parent
 * lives elsewhere is not lost: it becomes a root of this tree, recorded in
 * `adoptedParents` so the row can say who spawned it.
 */
export function scopeFleetTree<Session extends FleetTreeSession>(
	projects: readonly FleetProjectNode<Session>[],
	target: FleetScopeTarget<Session>,
): ScopedFleet<Session> {
	const projectId = target.project.id;
	const machineId = target.machine.id;
	const projectKey = fleetProjectKey(projectId, machineId);
	const machineKey = fleetMachineKey(projectId, machineId);
	const inScope = (node: FleetSessionNode<Session>) => node.projectId === projectId && node.machineId === machineId;
	const adoptedParents = new Map<string, FleetSessionNode<Session>>();
	const adopted: FleetSessionNode<Session>[] = [];
	const visit = (node: FleetSessionNode<Session>) => {
		for (const child of node.children) {
			if (inScope(child) && !inScope(node)) {
				// Its own descendants, wherever they run, come along under it.
				adopted.push(child);
				adoptedParents.set(child.key, node);
			} else {
				visit(child);
			}
		}
	};
	const own = projects.find((project) => project.key === projectKey);
	for (const project of projects) {
		if (project === own) continue;
		for (const machine of project.machines) for (const session of machine.sessions) visit(session);
	}

	const ownMachine = own?.machines.find((machine) => machine.key === machineKey);
	const sessions = [...(ownMachine?.sessions ?? []), ...adopted];
	const rollup = emptyFleetRollup();
	for (const session of sessions) addSession(rollup, session);
	const machine: FleetMachineNode<Session> = {
		type: "machine",
		key: machineKey,
		projectId,
		machine: ownMachine?.machine ?? target.machine,
		rollup,
		sessions,
	};
	return {
		projects: [{
			type: "project",
			key: projectKey,
			project: own?.project ?? target.project,
			projectLinkId: own?.projectLinkId,
			rollup: { ...rollup },
			machines: [machine],
		}],
		adoptedParents,
	};
}
