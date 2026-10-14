import { useTranslation } from "react-i18next";
import { useCodexAccountsQuery, type CodexAccount } from "../../hooks/useCodexAccountsQuery";
import { Button } from "../ui/button";
import { SettingsSection } from "./SettingsSection";

const authenticationLabels = {
	authorized: "settings.codexAccounts.signedIn",
	unauthorized: "settings.codexAccounts.signedOut",
	unknown: "settings.codexAccounts.unknown",
	not_applicable: "settings.codexAccounts.notApplicable",
} as const;
const capacityLabels = {
	available: "settings.codexAccounts.reason.capacityAvailable",
	near_limit: "settings.codexAccounts.reason.capacityNearLimit",
	exhausted: "settings.codexAccounts.reason.capacityExhausted",
	unknown: "settings.codexAccounts.usageDetailsUnavailable",
	unsupported: "settings.codexAccounts.reason.capacityUnsupported",
} as const;
const reconciliationLabels = {
	not_checked: "settings.codexAccounts.deviceNotChecked",
	checking: "settings.codexAccounts.deviceChecking",
	verified: "settings.codexAccounts.deviceVerified",
	temporarily_unavailable: "settings.codexAccounts.deviceUnavailable",
	blocked: "settings.codexAccounts.deviceBlocked",
} as const;

export function CodexAccountsSection({ active = true, titleHidden = false }: { active?: boolean; titleHidden?: boolean }) {
	const { t } = useTranslation();
	const query = useCodexAccountsQuery(active);
	const data = query.available ? query.data : undefined;
	return (
		<SettingsSection title={t("settings.codexAccounts.title")} titleHidden={titleHidden} sectionId="accounts">
			<div className="flex items-center justify-between gap-3">
				<p className="text-sm font-medium">{t("settings.codexAccounts.localOverview")}</p>
				<Button variant="secondary" size="sm" className="focus-visible:ring-2 focus-visible:ring-ring" disabled={!active || !query.available || query.isFetching} onClick={() => void query.refetch()}>
					{t("settings.project.refresh")}
				</Button>
			</div>
			<p className="text-xs text-muted-foreground">{t("settings.codexAccounts.cachedHint")}</p>
			<div role="status" className="text-sm text-muted-foreground">
				{!query.available ? t(query.disconnected ? "settings.codexAccounts.disconnected" : "settings.codexAccounts.localOnly")
					: query.isLoading ? t("settings.codexAccounts.loading")
					: query.isFetching ? t("settings.codexAccounts.refreshing") : null}
			</div>
			{query.available && query.isError && (
				<p role="alert" className="text-sm text-error">
					{t(query.error.message === "unauthorized" ? "settings.codexAccounts.accessDenied" : query.error.message === "localOnly" ? "settings.codexAccounts.localOnly" : "settings.codexAccounts.loadFailed")}
					{data && <> {t("settings.codexAccounts.retained")}</>}
				</p>
			)}
			{data && <>
				<p role="status" className="text-sm text-muted-foreground">{t(reconciliationLabels[data.deviceReconciliation.status])}</p>
				{data.accounts.length === 0 ? <p className="py-4 text-sm text-muted-foreground">{t("settings.codexAccounts.empty")}</p> : (
					<ul className="divide-y divide-border rounded-md bg-[var(--color-bg-settings-row)]">
						{data.accounts.map((account) => <AccountRow key={account.id} account={account} activeVerified={data.deviceReconciliation.activeAccountVerified} />)}
					</ul>
				)}
			</>}
		</SettingsSection>
	);
}

function AccountRow({ account, activeVerified }: { account: CodexAccount; activeVerified: boolean }) {
	const { t, i18n } = useTranslation();
	const { authentication, capacity } = account;
	const remaining = capacity.remainingPercent;
	return (
		<li className="space-y-2 px-4 py-3 text-sm">
			<div className="flex items-start justify-between gap-3">
				<div className="min-w-0 break-words">
					<p className="font-medium">{account.label}</p>
					{account.accountEmail && <p className="text-xs text-muted-foreground">{account.accountEmail}</p>}
				</div>
				{account.active && activeVerified && <span className="shrink-0 text-xs text-muted-foreground">{t("settings.codexAccounts.inUse")}</span>}
			</div>
			{account.status === "broken" && <p className="text-error">{t("settings.codexAccounts.reason.accountDescriptorInvalid")}</p>}
			<dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1">
				<dt className="text-muted-foreground">{t("settings.codexAccounts.authentication")}</dt>
				<dd>{t(authenticationLabels[authentication.state])}<Freshness value={authentication.freshness} /></dd>
				<dt className="text-muted-foreground">{t("settings.codexAccounts.capacity")}</dt>
				<dd>
					{t(capacityLabels[capacity.state])}
					{typeof remaining === "number" && Number.isFinite(remaining) && remaining >= 0 && remaining <= 100 && (
						<> {t("settings.codexAccounts.percentLeft", { value: new Intl.NumberFormat(i18n.language, { style: "percent", maximumFractionDigits: 0 }).format(remaining / 100) })}</>
					)}
					<Freshness value={capacity.freshness} />
				</dd>
				{capacity.plan && <><dt className="text-muted-foreground">{t("settings.codexAccounts.yourPlan")}</dt><dd className="break-words">{capacity.plan}</dd></>}
			</dl>
		</li>
	);
}

function Freshness({ value }: { value: CodexAccount["authentication"]["freshness"] }) {
	const { t } = useTranslation();
	if (value === "fresh") return null;
	return <span className="block text-xs text-muted-foreground">{t(value === "checking" ? "settings.codexAccounts.checking" : "settings.codexAccounts.observationStale")}</span>;
}
