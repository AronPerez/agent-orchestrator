import { aoBridge } from "./bridge";
import { setApiDaemonStatus } from "./api-client";
import { applyDaemonBaseUrl } from "./active-host";

export type DaemonStatus = Awaited<ReturnType<typeof aoBridge.daemon.getStatus>>;

// The base URL goes through active-host's gate rather than straight to the api
// client: while a remote host is active, the local daemon's ready/stopped
// reports must not repoint the app back at this machine mid-session.
export function applyDaemonStatus(nextStatus: DaemonStatus): void {
	setApiDaemonStatus(nextStatus);
	if (nextStatus.state === "ready" && nextStatus.port) {
		applyDaemonBaseUrl(`http://127.0.0.1:${nextStatus.port}`);
	} else {
		applyDaemonBaseUrl(null);
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
