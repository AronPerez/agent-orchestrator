import { useQuery } from "@tanstack/react-query";
import { Fragment } from "react";
import { useTranslation } from "react-i18next";
import { decisionSyncKey, fetchDecisionSyncStatus, footerParts, type FooterPart } from "../lib/decision-sync";
import { formatRelativeTime } from "../lib/usage-format";
import { useUiStore } from "../stores/ui-store";
import { Button } from "./ui/button";

/** The Decisions page footer: whose ledger this is and how fresh each linked machine is. */
export function DecisionSyncFooter({ now }: { now: number }) {
	const { t, i18n } = useTranslation();
	const openSettings = useUiStore((state) => state.openGlobalSettings);
	const status = useQuery({ queryKey: decisionSyncKey(), queryFn: ({ signal }) => fetchDecisionSyncStatus(signal) });
	// An older daemon has no sync status: it is this computer's ledger alone.
	if (!Array.isArray(status.data?.peers)) return <>{t("decisions.footerLocal")}</>;
	const at = new Date(now);
	const when = (iso?: string) => (iso ? formatRelativeTime(iso, at, i18n.language) : "");
	const minutes = (ms: string | undefined) =>
		new Intl.NumberFormat(i18n.language, { style: "unit", unit: "minute", unitDisplay: "short" }).format(Math.round(Number(ms ?? 0) / 60_000));
	const text = (part: FooterPart): string => {
		switch (part.kind) {
			case "local-only": return t("decisions.sync.localOnly");
			case "in-sync": return t("decisions.sync.inSync", { machines: new Intl.ListFormat(i18n.language).format(part.machines) });
			case "behind": return part.lastSuccessAt
				? t("decisions.sync.behind", { machine: part.machine, time: when(part.lastSuccessAt) })
				: t("decisions.sync.notSyncedYet", { machine: part.machine });
			case "problem":
				switch (part.reason) {
					case "revoked": return t("decisions.sync.revoked", { machine: part.machine });
					case "incompatible": return t("decisions.sync.incompatible", { machine: part.machine });
					case "identity_conflict": return t("decisions.sync.identityConflict", { machine: part.machine });
					case "clock_ahead": return t("decisions.sync.clockAhead", { machine: part.machine, duration: minutes(part.detail) });
					case "unreachable": return part.detail
						? t("decisions.sync.unreachable", { machine: part.machine, time: when(part.detail) })
						: t("decisions.sync.unreachableNever", { machine: part.machine });
				}
		}
	};
	const parts = footerParts(status.data, at);
	return (
		<span data-testid="decision-sync-footer">
			{parts.map((part, index) => (
				<Fragment key={`${part.kind}-${index}`}>
					{index > 0 ? " · " : null}
					{/* Only real trouble gets color; freshness stays muted. */}
					<span className={part.kind === "problem" ? "text-status-in-review" : undefined}>{text(part)}</span>
					{part.kind === "local-only" ? (
						<>
							{" · "}
							<Button
								type="button"
								variant="quiet"
								size="xs"
								className="h-auto px-0 align-baseline text-caption"
								onClick={() => openSettings("remoteHosts")}
							>
								{t("decisions.sync.linkMachines")}
							</Button>
						</>
					) : null}
				</Fragment>
			))}
		</span>
	);
}
