import { create } from "zustand";
import { aoBridge } from "../lib/bridge";
import { baseUrlFor, clientFor } from "../lib/host-clients";
import { LOCAL_HOST } from "../lib/hosts";
import { updateInstallRisk, type UpdateInstallRisk } from "../lib/update-install-risk";

const UNKNOWN_RISK: UpdateInstallRisk = { count: 0, unknown: true };
const SNAPSHOT_TIMEOUT_MS = 5_000;

type InstallState = {
	phase: "idle" | "checking" | "confirm" | "installing";
	promptOpen: boolean;
	returnFocusTo?: HTMLElement;
	risk: UpdateInstallRisk;
	failed: boolean;
};

// Shared across sidebar, settings and automatic feature-build progression.
export const useUpdateInstallStore = create<InstallState>(() => ({
	phase: "idle", promptOpen: false, risk: UNKNOWN_RISK, failed: false,
}));

async function freshLocalRisk(): Promise<UpdateInstallRisk> {
	const controller = new AbortController();
	let timer: ReturnType<typeof setTimeout> | undefined;
	try {
		const base = baseUrlFor(LOCAL_HOST);
		// Bypass the workspace cache and project grouping (which hides standalone
		// sessions). No pageSize means the daemon returns the complete active set.
		const result = await Promise.race([
			clientFor(LOCAL_HOST).GET("/api/v1/sessions", {
				params: { query: { active: true } },
				cache: "no-store",
				signal: controller.signal,
			}),
			new Promise<never>((_, reject) => {
				timer = setTimeout(() => {
					controller.abort();
					reject(new Error("Session refresh timed out"));
				}, SNAPSHOT_TIMEOUT_MS);
			}),
		]);
		if (result.error || base !== baseUrlFor(LOCAL_HOST)) return UNKNOWN_RISK;
		return updateInstallRisk(result.data);
	} catch {
		return UNKNOWN_RISK;
	} finally {
		clearTimeout(timer);
	}
}

async function checkAndInstall(confirmed: boolean): Promise<void> {
	useUpdateInstallStore.setState({ phase: "checking", promptOpen: confirmed, failed: false });
	const risk = await freshLocalRisk();
	if (!confirmed && (risk.count > 0 || risk.unknown)) {
		useUpdateInstallStore.setState({ phase: "confirm", promptOpen: true, risk });
		return;
	}
	useUpdateInstallStore.setState({ phase: "installing", risk });
	try {
		await aoBridge.updates.install();
		useUpdateInstallStore.setState({ phase: "idle", promptOpen: false });
	} catch {
		useUpdateInstallStore.setState({ phase: "confirm", promptOpen: true, risk, failed: true });
	}
}

export function requestUpdateInstall(): void {
	if (useUpdateInstallStore.getState().phase !== "idle") return;
	useUpdateInstallStore.setState({
		returnFocusTo: document.activeElement instanceof HTMLElement ? document.activeElement : undefined,
	});
	void checkAndInstall(false);
}

export function confirmUpdateInstall(): void {
	if (useUpdateInstallStore.getState().phase !== "confirm") return;
	// Confirmation acknowledges local-turn risk, including unknown state. Still
	// refresh at this click: the original prompt may have been open a long time.
	void checkAndInstall(true);
}

export function cancelUpdateInstall(): void {
	if (useUpdateInstallStore.getState().phase !== "confirm") return;
	useUpdateInstallStore.setState({ phase: "idle", promptOpen: false, failed: false });
}

export function useRequestUpdateInstall() {
	const busy = useUpdateInstallStore((state) => state.phase !== "idle");
	return { requestInstall: requestUpdateInstall, busy };
}
