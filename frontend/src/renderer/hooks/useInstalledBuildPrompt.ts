import { useEffect, useRef } from "react";
import { useUiStore } from "../stores/ui-store";
import type { UpdateStatus } from "../../main/update-settings";

export function useInstalledBuildPrompt({ installed }: UpdateStatus): void {
	const open = useUiStore((state) => state.openUpdateInstallPrompt);
	const prompted = useRef(new Set<string>());
	useEffect(() => {
		if (!installed?.idle || installed.restartWhenIdle || prompted.current.has(installed.identity)) return;
		prompted.current.add(installed.identity);
		open();
	}, [installed, open]);
}
