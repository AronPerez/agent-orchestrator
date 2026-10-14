import { useEffect, useRef, useState } from "react";
import type { UpdateStatus } from "../../main/update-settings";
import { aoBridge } from "../lib/bridge";

/**
 * Live desktop update status: seeded from updates.getStatus, then streamed via
 * the updates:status push channel. Used by the sidebar restart-to-update row
 * and the Global Settings Updates section.
 */
export function useUpdateStatus(onStatusEvent?: (status: UpdateStatus) => void): UpdateStatus {
	const [status, setStatus] = useState<UpdateStatus>({ state: "idle" });
	const onStatusEventRef = useRef(onStatusEvent);
	onStatusEventRef.current = onStatusEvent;
	useEffect(() => {
		let live = true;
		void aoBridge.updates.getStatus().then((s) => {
			if (!live) return;
			onStatusEventRef.current?.(s);
			setStatus(s);
		});
		const off = aoBridge.updates.onStatus((next) => {
			onStatusEventRef.current?.(next);
			setStatus(next);
		});
		return () => {
			live = false;
			off?.();
		};
	}, []);
	return status;
}
