/**
 * Dev-only local (email/password) cloud sign-in for the renderer.
 *
 * Whether the surface is offered is decided authoritatively in the main process
 * (unpackaged/dev build AND a loopback control plane); this hook only mirrors
 * that decision for UI visibility and forwards the credentials over the bridge.
 * On success the main process pushes the new session over cloud:sessionChanged,
 * so useCloudSession flips to "authenticated" on its own — no manual refresh.
 */

import { useEffect, useState } from "react";
import type { CloudAccount } from "../../shared/cloud-account";
import { aoBridge } from "../lib/bridge";
import { useSettings } from "./useSettings";

export interface LocalRegisterFields {
	email: string;
	displayName: string;
	password: string;
	orgSlug: string;
	orgName: string;
}

export interface LocalLoginFields {
	email: string;
	password: string;
}

export interface UseCloudLocalAuthResult {
	/** Whether the dev-only local sign-in surface should be shown. */
	available: boolean;
	/**
	 * Whether `available` is the real answer yet: settings have loaded and main
	 * has answered for this control plane. Until then `available` reads false,
	 * so a sign-in button that picks between the two flows waits for this.
	 */
	checked: boolean;
	/** Resolved loopback control-plane base URL the credentials are sent to. */
	cpUrl: string;
	register: (fields: LocalRegisterFields) => Promise<CloudAccount>;
	login: (fields: LocalLoginFields) => Promise<CloudAccount>;
}

// Main's answer per control-plane URL. It depends only on the build and the
// URL, so it never changes while the app runs: a later mount (opening
// Settings again) reads it at once instead of waiting on the IPC round trip.
const answers = new Map<string, boolean>();

/** Forgets every answer; tests only. */
export function resetCloudLocalAuthAnswers(): void {
	answers.clear();
}

export function useCloudLocalAuth(): UseCloudLocalAuthResult {
	const { settings, isLoading } = useSettings();
	const cpUrl = settings?.cloudControlPlaneUrl ?? "";
	// Re-renders once main answers; the answer itself lives in `answers`.
	const [, setAnswered] = useState(0);
	// A failed ask is not cached (the next mount asks again), but it ends this
	// mount's wait: the WorkOS flow is the safe fallback.
	const [failedUrl, setFailedUrl] = useState<string | null>(null);

	useEffect(() => {
		if (cpUrl === "" || answers.has(cpUrl)) return;
		let active = true;
		aoBridge.cloud
			.localAuthAvailable(cpUrl)
			.then((ok) => {
				answers.set(cpUrl, ok);
				if (active) setAnswered((n) => n + 1);
			})
			.catch(() => {
				if (active) setFailedUrl(cpUrl);
			});
		return () => {
			active = false;
		};
	}, [cpUrl]);

	// Without a control plane there is nothing to ask once settings are known
	// (a failed settings read counts as known: there is no URL to ask about).
	const settingsKnown = settings !== undefined || !isLoading;
	const checked = cpUrl === "" ? settingsKnown : answers.has(cpUrl) || failedUrl === cpUrl;
	const available = cpUrl !== "" && answers.get(cpUrl) === true;

	return {
		available,
		checked,
		cpUrl,
		register: (fields) => aoBridge.cloud.localRegister({ cpUrl, ...fields }),
		login: (fields) => aoBridge.cloud.localLogin({ cpUrl, ...fields }),
	};
}
