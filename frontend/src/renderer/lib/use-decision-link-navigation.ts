import { useNavigate } from "@tanstack/react-router";
import { useCallback } from "react";
import { parseDecisionLink } from "./decision-links";

/**
 * Opens `ao://decisions/<id>` as the selected call on the Decisions page and
 * answers true; any other link answers false for the next opener. A link
 * replaces the page's search, so the call opens over the board.
 */
export function useDecisionLinkNavigation(): (url: string) => boolean {
	const navigate = useNavigate();
	return useCallback((url: string) => {
		const target = parseDecisionLink(url);
		if (!target) return false;
		void navigate({ to: "/decisions", search: { call: target.callId } });
		return true;
	}, [navigate]);
}
