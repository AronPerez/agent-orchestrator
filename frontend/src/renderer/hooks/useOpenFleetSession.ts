import { useNavigate } from "@tanstack/react-router";
import { useCallback } from "react";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { recordManualWorkerOpen } from "../lib/session-management-telemetry";
import type { WorkspaceSession } from "../types/workspace";

/** Open a fleet row's session on the machine it runs on (undefined: this computer). */
export function useOpenFleetSession(): (session: WorkspaceSession, hostId?: string) => void {
	const navigate = useNavigate();
	return useCallback((session: WorkspaceSession, hostId?: string) => {
		if (session.kind === "worker") recordManualWorkerOpen(session.id, hostId);
		void navigate(sessionNavigateTarget(session.workspaceId, session.id, hostId));
	}, [navigate]);
}
