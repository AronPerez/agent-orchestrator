import { LOCAL_HOST, type HostId } from "./hosts";

export type DecisionWorkspaceSource = {
	status: "loading" | "error" | "ready";
	workspaces: ReadonlyArray<{ id: string; sessions: ReadonlyArray<{ id: string; isTerminated?: boolean; status?: string }> }>;
};

export type DecisionSessionTarget =
	| { kind: "open"; host: HostId; projectId: string; sessionId: string; terminated: boolean }
	| { kind: "checking" }
	| { kind: "offline"; machine: string }
	| { kind: "missing" }
	| { kind: "error" };

/**
 * Where "Open session" may go for a call. The owner is looked up only on the
 * machine that raised the call: this computer when its daemon served the
 * board, otherwise that machine's own connection. A session with the same id
 * on another machine is never a match.
 */
export function resolveDecisionSessionTarget(input: {
	call: { hostId: string; machineLabel?: string; ownerSessionId: string };
	/** The board's servingHostId; undefined until the board has loaded. */
	servingHostId: string | undefined;
	connectedHosts: readonly HostId[];
	local: DecisionWorkspaceSource;
	/** Workspaces of the connection named by the call's origin, if any. */
	remote: DecisionWorkspaceSource;
}): DecisionSessionTarget {
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
	for (const workspace of source.workspaces) {
		const session = workspace.sessions.find((candidate) => candidate.id === call.ownerSessionId);
		if (session) {
			return {
				kind: "open",
				host,
				projectId: workspace.id,
				sessionId: session.id,
				terminated: session.isTerminated === true || session.status === "terminated",
			};
		}
	}
	return { kind: "missing" };
}
