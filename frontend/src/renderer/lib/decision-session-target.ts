import { LOCAL_HOST, type HostId } from "./hosts";

/** What the decision view reads about one session on the call's machine. */
export type DecisionSourceSession = {
	id: string;
	isTerminated?: boolean;
	status?: string;
	title?: string;
	kind?: string;
	parentSessionId?: string;
	/** Set when the parent lives on another installation; such a parent is never looked up here. */
	parentHostId?: string;
	artifactFiles?: Array<{ path: string; previousPaths?: string[] }>;
};

export type DecisionWorkspaceSource = {
	status: "loading" | "error" | "ready";
	workspaces: ReadonlyArray<{ id: string; sessions: ReadonlyArray<DecisionSourceSession> }>;
};

export type DecisionSessionTarget =
	| { kind: "open"; host: HostId; projectId: string; sessionId: string; terminated: boolean }
	| { kind: "checking" }
	| { kind: "offline"; machine: string }
	| { kind: "missing" }
	| { kind: "error" };

/**
 * The machine that holds a call's sessions, as this app can see it: this
 * computer's own workspaces when its daemon served the board, a connected
 * host's workspaces when the call came from there, otherwise nothing.
 */
export type DecisionMachine =
	| { kind: "checking" }
	| { kind: "offline"; machine: string }
	| { kind: "error" }
	| { kind: "ready"; host: HostId; workspaces: DecisionWorkspaceSource["workspaces"] };

type MachineInput = {
	call: { hostId: string; machineLabel?: string };
	/** The board's servingHostId; undefined until the board has loaded. */
	servingHostId: string | undefined;
	connectedHosts: readonly HostId[];
	local: DecisionWorkspaceSource;
	/** Workspaces of the connection named by the call's origin, if any. */
	remote: DecisionWorkspaceSource;
};

/** Which machine's sessions a call's session ids refer to. A same-id session elsewhere is never a match. */
export function resolveDecisionMachine(input: MachineInput): DecisionMachine {
	const { call } = input;
	if (input.servingHostId === undefined) return { kind: "checking" };
	let host: HostId;
	let source: DecisionWorkspaceSource;
	if (call.hostId === input.servingHostId) {
		host = LOCAL_HOST;
		source = input.local;
	} else if (input.connectedHosts.includes(call.hostId)) {
		host = call.hostId;
		source = input.remote;
	} else {
		return { kind: "offline", machine: call.machineLabel || call.hostId };
	}
	if (source.status === "loading") return { kind: "checking" };
	if (source.status === "error") return { kind: "error" };
	return { kind: "ready", host, workspaces: source.workspaces };
}

/** One session on the call's machine and the project that lists it. */
export function findDecisionSession(machine: DecisionMachine, sessionId: string): { projectId: string; session: DecisionSourceSession } | undefined {
	if (machine.kind !== "ready") return undefined;
	for (const workspace of machine.workspaces) {
		const session = workspace.sessions.find((candidate) => candidate.id === sessionId);
		if (session) return { projectId: workspace.id, session };
	}
	return undefined;
}

/** Where opening a session on the call's machine goes, or why it cannot. */
export function decisionSessionTarget(machine: DecisionMachine, sessionId: string): DecisionSessionTarget {
	if (machine.kind !== "ready") return machine;
	const found = findDecisionSession(machine, sessionId);
	if (!found) return { kind: "missing" };
	return {
		kind: "open",
		host: machine.host,
		projectId: found.projectId,
		sessionId: found.session.id,
		terminated: found.session.isTerminated === true || found.session.status === "terminated",
	};
}

/**
 * The sessions that spawned this one, from the top down, on the same machine.
 * A parent on another installation, or one this machine no longer lists, ends
 * the chain there.
 */
export function decisionSessionChain(machine: DecisionMachine, sessionId: string): DecisionSourceSession[] {
	const chain: DecisionSourceSession[] = [];
	const seen = new Set([sessionId]);
	let current = findDecisionSession(machine, sessionId)?.session;
	while (current?.parentSessionId && !current.parentHostId && !seen.has(current.parentSessionId)) {
		seen.add(current.parentSessionId);
		const parent = findDecisionSession(machine, current.parentSessionId)?.session;
		if (!parent) break;
		chain.unshift(parent);
		current = parent;
	}
	return chain;
}

/**
 * Where "Open session" may go for a call. The owner is looked up only on the
 * machine that raised the call: this computer when its daemon served the
 * board, otherwise that machine's own connection. A session with the same id
 * on another machine is never a match.
 */
export function resolveDecisionSessionTarget(input: MachineInput & { call: { ownerSessionId: string } }): DecisionSessionTarget {
	return decisionSessionTarget(resolveDecisionMachine(input), input.call.ownerSessionId);
}
