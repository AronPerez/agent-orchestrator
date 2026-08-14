import { aoBridge } from "./bridge";
import { setApiDaemonStatus } from "./api-client";
import { applyDaemonBaseUrl } from "./active-host";
import { LOCAL_HOST } from "./hosts";

export type DaemonStatus = Awaited<ReturnType<typeof aoBridge.daemon.getStatus>>;

// The base URL goes through the host gate rather than straight to the API
// client so local lifecycle reports can never overwrite a remote proxy base.
export function applyDaemonStatus(nextStatus: DaemonStatus): void {
	setApiDaemonStatus(nextStatus);
	if (nextStatus.state === "ready" && nextStatus.port) {
		applyDaemonBaseUrl(LOCAL_HOST, `http://127.0.0.1:${nextStatus.port}`);
	} else {
		applyDaemonBaseUrl(LOCAL_HOST, null);
	}
}

export async function refreshDaemonStatus(): Promise<DaemonStatus> {
	const nextStatus = await readDaemonStatus();
	applyDaemonStatus(nextStatus);
	return nextStatus;
}

export function readDaemonStatus(): Promise<DaemonStatus> {
	return aoBridge.daemon.getStatus();
}
