import { useId, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Check, ShieldAlert, TriangleAlert } from "@/components/app-icons";
import { useDecisionDetail } from "../hooks/useDecisionInbox";
import { useDecisionSessionTarget } from "../hooks/useDecisionSessionTarget";
import { useNavigateToSession } from "../lib/navigate-to-session";
import { apiErrorRequestId } from "../lib/api-client";
import type { DecisionCall, DecisionHistoryEntry } from "../lib/decision-inbox";
import { openLinkInSystemBrowser } from "../lib/external-link-policy";
import { formatTimeCompact } from "../lib/format-time";
import { cn } from "../lib/utils";
import { DecisionCallActions, decisionAcceptsAnswer } from "./DecisionCallActions";
import { decisionOwner, decisionPlace } from "./DecisionLinkPreviewCard";
import { DecisionStateDot, decisionStateLabel } from "./DecisionStateLabel";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";
import { Skeleton } from "./ui/skeleton";

export type DecisionCallPanelProps = {
	callId: string;
	onClose: () => void;
};

function isWebUrl(url: string): boolean {
	try {
		const parsed = new URL(url);
		return parsed.protocol === "https:" || parsed.protocol === "http:";
	} catch {
		return false;
	}
}

/** A supporting link: web links open in the system browser; anything else stays text. */
function SafeLink({ url, children }: { url: string; children: ReactNode }) {
	if (!isWebUrl(url)) return <span className="text-muted-foreground">{children}</span>;
	return (
		<a
			href={url}
			rel="noreferrer noopener"
			target="_blank"
			className="break-words text-markdown-link underline decoration-markdown-link/45 underline-offset-2 hover:text-markdown-link-hover"
			onClick={(event) => {
				event.preventDefault();
				void openLinkInSystemBrowser(url);
			}}
		>
			{children}
		</a>
	);
}

function useExactTime() {
	const { i18n } = useTranslation();
	const format = new Intl.DateTimeFormat(i18n.language, { dateStyle: "medium", timeStyle: "short" });
	return (iso: string) => {
		const at = new Date(iso);
		return Number.isNaN(at.getTime()) ? iso : format.format(at);
	};
}

/**
 * One call beside the board, in the shell's inspector position: where it
 * belongs, what was asked, and everything that has happened to it. Read-only
 * here; the actions arrive with the actionable panel.
 */
export function DecisionCallPanel({ callId, onClose }: DecisionCallPanelProps) {
	const { t } = useTranslation();
	const detail = useDecisionDetail(callId);
	const onKeyDown = (event: KeyboardEvent) => {
		if (event.key !== "Escape" || event.defaultPrevented) return;
		event.preventDefault();
		onClose();
	};

	let body: ReactNode;
	if (detail.data) {
		body = (
			<>
				{detail.isError ? <RefreshError error={detail.error} onRetry={() => void detail.refetch()} /> : null}
				<DecisionCallDetail call={detail.data.call} history={detail.data.history} onRecheck={() => void detail.refetch()} />
			</>
		);
	} else if (detail.data === null) {
		body = <p role="status" className="px-4 py-6 text-sm text-muted-foreground">{t("decisions.unavailableTitle")}</p>;
	} else if (detail.isError) {
		const requestId = apiErrorRequestId(detail.error instanceof Error ? detail.error.cause : undefined);
		body = (
			<div role="alert" className="flex flex-col items-start gap-2 px-4 py-6 text-sm">
				<span className="text-destructive">{detail.error instanceof Error ? detail.error.message : t("decisions.loadFailedOne")}</span>
				{requestId ? <span className="font-mono text-caption text-muted-foreground">{t("decisions.requestId", { id: requestId })}</span> : null}
				<Button size="sm" variant="secondary" onClick={() => void detail.refetch()}>{t("decisions.retry")}</Button>
			</div>
		);
	} else {
		body = (
			<div role="status" aria-label={t("decisions.loadingDecision")} className="space-y-2 px-4 py-4">
				<Skeleton className="h-3 w-40" />
				<Skeleton className="h-5 w-64" />
				<Skeleton className="h-16 w-full" />
			</div>
		);
	}

	return (
		<aside
			aria-label={t("decisions.panelLabel")}
			aria-busy={detail.isFetching && Boolean(detail.data)}
			className="flex h-full min-w-0 flex-col bg-background"
			onKeyDown={onKeyDown}
		>
			<div className="flex h-9 shrink-0 items-center justify-end gap-2 border-b border-border px-2">
				{detail.isFetching && detail.data ? (
					<span role="status" className="mr-auto pl-2 text-caption text-passive">{t("decisions.updating")}</span>
				) : null}
				<CloseButton aria-label={t("decisions.close")} size="icon-sm" onClick={onClose} />
			</div>
			<div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">{body}</div>
		</aside>
	);
}

/** A failed refresh over a call still on screen: the call stays, with a retry. */
function RefreshError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
	const { t } = useTranslation();
	const requestId = apiErrorRequestId(error instanceof Error ? error.cause : undefined);
	return (
		<div role="alert" className="mx-4 mt-3 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
			<span className="text-destructive">{error instanceof Error ? error.message : t("decisions.loadFailedOne")}</span>
			{requestId ? <span className="font-mono text-caption text-muted-foreground">{t("decisions.requestId", { id: requestId })}</span> : null}
			<Button size="sm" variant="secondary" onClick={onRetry}>{t("decisions.retry")}</Button>
		</div>
	);
}

function DecisionCallDetail({ call, history, onRecheck }: { call: DecisionCall; history: DecisionHistoryEntry[]; onRecheck: () => void }) {
	const { t } = useTranslation();
	const exactTime = useExactTime();
	const place = decisionPlace(call);
	return (
		<div className="flex flex-col gap-4 px-4 py-3">
			<header className="flex flex-col gap-1">
				{place ? <p className="truncate text-caption text-muted-foreground" title={place}>{place}</p> : null}
				<p className="truncate text-caption text-muted-foreground">{decisionOwner(call)}</p>
				<h2 className="mt-1 text-subtitle font-semibold leading-snug text-foreground">{call.title}</h2>
				<div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-caption">
					<span className="inline-flex items-center gap-1.5 font-medium text-foreground">
						<DecisionStateDot state={call.state} />
						{decisionStateLabel(call.state, t)}
					</span>
					<span className="text-muted-foreground" title={exactTime(call.raisedAt)}>
						{t("decisions.raisedAt", { time: formatTimeCompact(call.raisedAt) })}
					</span>
					{call.retired ? <span className="text-muted-foreground">{t("decisions.history.retired")}</span> : null}
				</div>
				{call.changedAfterClose ? (
					<p className="mt-1 flex items-center gap-1.5 text-caption text-status-in-review">
						<TriangleAlert aria-hidden="true" className="size-3.5 shrink-0" />
						{t("decisions.changedAfterClose")}
					</p>
				) : null}
			</header>
			{call.context ? <p className="whitespace-pre-wrap break-words text-sm leading-relaxed text-foreground">{call.context}</p> : null}
			{call.links.length > 0 ? (
				<section aria-label={t("decisions.links")} className="flex flex-col gap-1 text-sm">
					{call.links.map((link) => (
						<SafeLink key={`${link.label}:${link.url}`} url={link.url}>{link.label || link.url}</SafeLink>
					))}
				</section>
			) : null}
			{call.decided ? <DecidedDetails call={call} exactTime={exactTime} /> : null}
			{decisionAcceptsAnswer(call) || call.options.length === 0 ? null : <ul aria-label={t("decisions.options")} className="overflow-hidden rounded-lg bg-card">
				{call.options.map((option, index) => {
					const chosen = call.choice === "option" && call.option === index + 1;
					return (
						<li key={`${index}:${option.label}`} className="flex gap-2 border-border px-3 py-2 not-first:border-t">
							<span aria-hidden="true" className={cn("mt-0.5 flex size-3.5 shrink-0 items-center justify-center", chosen ? "text-accent" : "text-transparent")}>
								<Check className="size-3.5" />
							</span>
							<span className="min-w-0 flex-1">
								<span className="block text-sm font-medium text-foreground">{option.label}</span>
								{option.consequence ? <span className="block text-caption text-muted-foreground">{option.consequence}</span> : null}
								{option.recommended || chosen ? (
									<span className="mt-0.5 block text-caption text-muted-foreground">
										{[option.recommended ? t("decisionInbox.recommended") : "", chosen ? t("decisions.chosen") : ""].filter(Boolean).join(" · ")}
									</span>
								) : null}
							</span>
						</li>
					);
				})}
			</ul>}
			{call.note && !decisionAcceptsAnswer(call) ? <p className="whitespace-pre-wrap break-words border-l-2 border-border pl-3 text-sm text-muted-foreground">{call.note}</p> : null}
			<DecisionCallActions call={call} extraActions={<OpenSessionButton call={call} />} onRecheck={onRecheck} />
			{call.outcome ? (
				<section aria-label={t("decisions.outcome")} className="text-sm">
					<p className="font-medium text-foreground">{call.outcome}</p>
					{call.outcomeLink ? <SafeLink url={call.outcomeLink}>{call.outcomeLink}</SafeLink> : null}
				</section>
			) : null}
			<section className="flex flex-col gap-2">
				<h3 className="text-caption font-medium text-muted-foreground">{t("decisions.history")}</h3>
				<ol aria-label={t("decisions.history")} className="flex flex-col gap-2.5">
					{history.map((item) => (
						<li key={item.id} className="text-caption">
							<p data-slot="history-what" className="text-sm text-foreground">{historyWhat(item, call, t)}</p>
							{item.note ? <p className="whitespace-pre-wrap break-words text-muted-foreground">{item.note}</p> : null}
							{item.kind === "outcome" && item.link ? <SafeLink url={item.link}>{item.link}</SafeLink> : null}
							<p className="text-muted-foreground">
								{historyWho(item, call, t)} · <time dateTime={item.writtenAt}>{exactTime(item.writtenAt)}</time>
							</p>
						</li>
					))}
				</ol>
			</section>
		</div>
	);
}

/**
 * Opens the call's owning session on the machine that raised it, or says why
 * it cannot. A finished session opens to its existing view; nothing restarts it.
 */
function OpenSessionButton({ call }: { call: DecisionCall }) {
	const { t } = useTranslation();
	const reasonId = useId();
	const target = useDecisionSessionTarget(call);
	const navigateToSession = useNavigateToSession();
	const reason = target.kind === "checking"
		? t("decisions.sessionChecking")
		: target.kind === "offline"
			? t("decisions.sessionOffline", { machine: target.machine })
			: target.kind === "missing"
				? t("decisions.sessionMissing")
				: target.kind === "error"
					? t("decisions.sessionCheckFailed")
					: undefined;
	return (
		<>
			<Button
				type="button"
				variant="secondary"
				size="sm"
				disabled={target.kind !== "open"}
				aria-describedby={reason ? reasonId : undefined}
				onClick={() => {
					if (target.kind === "open") navigateToSession(target.projectId, target.sessionId, target.host);
				}}
			>
				{t("decisions.openSession")}
			</Button>
			{reason ? <span id={reasonId} className="text-caption text-muted-foreground">{reason}</span> : null}
		</>
	);
}

/**
 * What a decision made for the person carries: how to undo and check it,
 * whether the session confirmed it acted, and why it was outside policy.
 */
function DecidedDetails({ call, exactTime }: { call: DecisionCall; exactTime: (iso: string) => string }) {
	const { t } = useTranslation();
	const decided = call.decided!;
	const outsidePolicy = decided.outsidePolicy === "autonomy_not_on"
		? t("decisions.outsidePolicy.autonomyNotOn")
		: decided.outsidePolicy === "kind_reserved"
			? t("decisions.outsidePolicy.kindReserved")
			: undefined;
	return (
		<section className="flex flex-col gap-2 text-sm">
			{outsidePolicy ? (
				<p className="flex items-start gap-1.5 text-caption text-status-in-review">
					<ShieldAlert aria-hidden="true" className="mt-px size-3.5 shrink-0" />
					{outsidePolicy}
				</p>
			) : null}
			<dl className="flex flex-col gap-2">
				<div>
					<dt className="text-caption text-muted-foreground">{t("decisions.undoLabel")}</dt>
					<dd className="whitespace-pre-wrap break-words text-foreground">{decided.undo}</dd>
				</div>
				{decided.verify ? (
					<div>
						<dt className="text-caption text-muted-foreground">{t("decisions.verifyLabel")}</dt>
						<dd className="whitespace-pre-wrap break-words text-foreground">{decided.verify}</dd>
					</div>
				) : null}
			</dl>
			<p className="text-caption text-muted-foreground">
				{decided.carriedOut
					? <span title={decided.carriedOutAt ? exactTime(decided.carriedOutAt) : undefined}>{t("decisions.carriedOutAt", { time: decided.carriedOutAt ? formatTimeCompact(decided.carriedOutAt) : "" }).trim()}</span>
					: t("decisions.notCarriedOutYet")}
			</p>
		</section>
	);
}

/** What one history entry recorded, in the page's words. Reduction stays in the daemon. */
function historyWhat(item: DecisionHistoryEntry, call: DecisionCall, t: TFunction): string {
	switch (item.kind) {
		case "raised": return t("decisions.history.raised");
		case "answered": {
			const choice = item.choice === "other"
				? t("decisions.somethingElse")
				: call.options[(item.option ?? 0) - 1]?.label ?? String(item.option ?? "");
			return t("decisions.history.answered", { choice });
		}
		case "dismissed": return t("decisions.history.dismissed");
		case "delivered":
			if (item.status === "no_session") return t("decisions.noSession");
			return t(item.fallback ? "decisions.sentToFallback" : "decisions.sentTo", { session: item.sessionName || item.sessionId || "" });
		case "outcome": return t("decisions.history.outcome", { text: item.text ?? "" });
		case "withdrawn": return t("decisions.history.withdrawn");
		case "retired": return t(item.retired ? "decisions.history.retired" : "decisions.history.unretired");
		case "decided": return t("decisions.history.decided", { text: item.text ?? call.title });
		case "overruled": return t("decisions.history.overruled");
		case "carried_out": return t("decisions.history.carriedOut");
		default: return t("decisions.history.unknown", { kind: item.kind });
	}
}

/** Who wrote an entry and from which device. */
function historyWho(item: DecisionHistoryEntry, call: DecisionCall, t: TFunction): string {
	const machine = item.hostId === call.hostId ? call.machineLabel || call.hostId : item.hostId;
	if (item.authorKind === "person") {
		const surface = item.authorSurface === "desktop" || item.authorSurface === "cli" || item.authorSurface === "mobile"
			? t(`decisions.surface.${item.authorSurface}`)
			: item.authorSurface;
		return [t("decisions.author.person"), surface, machine].filter(Boolean).join(" · ");
	}
	if (item.authorKind === "session") {
		const session = item.authorSession === call.ownerSessionId ? call.ownerSessionName || call.ownerSessionId : item.authorSession;
		return [session, machine].filter(Boolean).join(" · ");
	}
	return [t("decisions.author.system"), machine].filter(Boolean).join(" · ");
}
