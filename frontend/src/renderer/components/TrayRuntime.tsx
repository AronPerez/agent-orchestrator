import { useEffect, useMemo, useRef } from "react";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import {
	attentionZone,
	flattenHostSections,
	workerSessions,
} from "../types/workspace";
import { aoBridge } from "../lib/bridge";
import { useNavigateToSession } from "../lib/navigate-to-session";
import type { TraySessionEntry } from "../../shared/tray";

export function TrayRuntime() {
	const workspaceSections = useWorkspaceQuery().data;
	const workspaces = useMemo(() => flattenHostSections(workspaceSections), [workspaceSections]);
	const navigateToSession = useNavigateToSession();

	const sessions = useMemo<TraySessionEntry[]>(() => {
		const entries: TraySessionEntry[] = [];
		for (const workspace of workspaces) {
			for (const session of workerSessions(workspace.sessions)) {
				const zone = attentionZone(session);
				if (zone === "merge" && session.status === "merged") continue;
				if (zone !== "action" && zone !== "merge") continue;
				entries.push({
					host: session.host,
					projectId: workspace.id,
					projectName: workspace.name,
					sessionId: session.id,
					title: session.title,
					zone,
				});
			}
		}
		return entries;
	}, [workspaces]);

	const lastPushed = useRef<string | null>(null);
	const serialized = JSON.stringify(sessions);
	useEffect(() => {
		if (lastPushed.current === serialized) return;
		lastPushed.current = serialized;
		aoBridge.tray.setAttentionState({ sessions });
	}, [serialized, sessions]);

	useEffect(() => {
		return aoBridge.tray.onOpenSession((target) => {
			navigateToSession({ host: target.host, id: target.sessionId });
		});
	}, [navigateToSession]);

	return null;
}
