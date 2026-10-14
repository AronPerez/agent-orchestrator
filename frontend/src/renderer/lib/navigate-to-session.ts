import { useNavigate } from "@tanstack/react-router";
import { useCallback } from "react";
import type { Ref } from "./hosts";

export function useNavigateToSession(): (ref: Ref) => void {
	const navigate = useNavigate();
	return useCallback(
		(ref: Ref) => {
			if (!ref.id) return;
			void navigate({
				to: "/host/$hostId/session/$sessionId",
				params: { hostId: ref.host, sessionId: ref.id },
			});
		},
		[navigate],
	);
}
