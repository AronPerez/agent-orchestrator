import type { components } from "../../api/schema";

export type UpdateInstallRisk = { count: number; unknown: boolean };

// Read raw local session facts, not derived board status or a harness allowlist.
export function updateInstallRisk(body: unknown): UpdateInstallRisk {
	const risk = { count: 0, unknown: false };
	if (!body || typeof body !== "object" || !("sessions" in body) || !Array.isArray(body.sessions)) {
		return { ...risk, unknown: true };
	}
	if ("nextPageToken" in body && body.nextPageToken) risk.unknown = true;
	for (const value of body.sessions) {
		if (!value || typeof value !== "object") {
			risk.unknown = true;
			continue;
		}
		const session = value as Partial<components["schemas"]["ControllersSessionView"]>;
		if (session.isTerminated === true || session.mode === "tui") continue;
		if (session.mode !== "chat") {
			risk.unknown = true;
			continue;
		}
		if (session.chatProviderPreserved === true) continue;
		const activity = session.activity?.state;
		if (activity === "idle" || activity === "exited") continue;
		risk.count++;
		if (session.chatProviderPreserved !== false || !["active", "waiting_input", "blocked"].includes(activity ?? "")) {
			risk.unknown = true;
		}
	}
	return risk;
}
