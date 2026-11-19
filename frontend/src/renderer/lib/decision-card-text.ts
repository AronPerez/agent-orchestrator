import type { TFunction } from "i18next";
import type { DecisionCardSecondary, DecisionCardStatus } from "./decision-board";
import { originFreshness, type DecisionSyncStatus } from "./decision-sync";
import { formatRelativeTime } from "./usage-format";

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** "Waiting 3h": how long a call has waited, in its largest whole unit. */
export function decisionWaitingText(since: string, now: number, t: TFunction): string {
	const elapsed = Math.max(0, now - Date.parse(since));
	if (!Number.isFinite(elapsed)) return "";
	if (elapsed >= DAY) return t("decisions.waitingDays", { n: Math.floor(elapsed / DAY) });
	if (elapsed >= HOUR) return t("decisions.waitingHours", { n: Math.floor(elapsed / HOUR) });
	return t("decisions.waitingMinutes", { n: Math.max(1, Math.floor(elapsed / MINUTE)) });
}

/** What the card says about a call another machine must still deliver. */
export function pendingRemoteStatusText(originHostId: string, sync: DecisionSyncStatus | undefined, now: number, locale: string, t: TFunction): string {
	const origin = originFreshness(originHostId, sync, new Date(now));
	if (origin.stale && origin.lastHeardAt) {
		return t("decisions.card.notDeliveredStale", { machine: origin.machine, time: formatRelativeTime(origin.lastHeardAt, new Date(now), locale) });
	}
	return t("decisions.card.notDeliveredWaiting", { machine: origin.machine });
}

export function decisionStatusText(status: DecisionCardStatus, now: number, t: TFunction, remote?: { sync?: DecisionSyncStatus; locale: string }): string {
	switch (status.kind) {
		case "waiting": return decisionWaitingText(status.since, now, t);
		case "sent": return t(status.fallback ? "decisions.sentToFallback" : "decisions.sentTo", { session: status.to });
		case "pending": return status.originHostId
			? pendingRemoteStatusText(status.originHostId, remote?.sync, now, remote?.locale ?? "en", t)
			: t("decisions.notDelivered");
		case "no_session": return t("decisions.noSession");
		case "outcome": return status.text;
		case "decided": return t(status.carriedOut ? "decisions.carriedOut" : "decisions.notCarriedOut");
	}
}

export function decisionSecondaryText(secondary: DecisionCardSecondary, t: TFunction): string {
	switch (secondary.kind) {
		case "who": return [secondary.machine, secondary.session].filter(Boolean).join(" · ");
		case "choice": return secondary.label;
		case "something_else": return t("decisions.somethingElse");
	}
}
