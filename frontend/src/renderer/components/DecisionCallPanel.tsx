import { useQuery } from "@tanstack/react-query";
import { useMemo, type KeyboardEvent, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Check, ShieldAlert, TriangleAlert } from "@/components/app-icons";
import { useDecisionDetail } from "../hooks/useDecisionInbox";
import { useDecisionMachine } from "../hooks/useDecisionSessionTarget";
import { apiErrorRequestId } from "../lib/api-client";
import type { DecisionCall, DecisionHistoryEntry } from "../lib/decision-inbox";
import { decisionMaterial, decisionTextParts } from "../lib/decision-references";
import { decisionSessionChain, decisionSessionTarget, findDecisionSession, type DecisionMachine, type DecisionSessionTarget, type DecisionWorkspaceSource } from "../lib/decision-session-target";
import { decisionSyncKey, fetchDecisionSyncStatus, originFreshness, type DecisionSyncStatus } from "../lib/decision-sync";
import { openLinkInSystemBrowser } from "../lib/external-link-policy";
import { formatTimeCompact } from "../lib/format-time";
import { getSessionStatusView } from "../lib/session-presentation";
import { cn } from "../lib/utils";
import { toSessionStatus } from "../types/workspace";
import { DecisionCallActions, decisionAcceptsAnswer } from "./DecisionCallActions";
import { decisionMachineHost, DecisionSessionChip, DecisionText, shortSessionName } from "./DecisionReference";
import { DecisionSupportingMaterial, hasDecisionMaterial, useLaterDecisions } from "./DecisionSupportingMaterial";
import { DecisionStateDot, decisionStateLabel } from "./DecisionStateLabel";
import { Button } from "./ui/button";
import { CloseButton } from "./ui/close-button";
import { Skeleton } from "./ui/skeleton";

export type DecisionCallPanelProps = {
	callId: string;
	/** Closes the panel. Left out where something else closes it, as its tab does for a decision filling the page. */
	onClose?: () => void;
	/** Controls drawn before the close button, such as Keep open. */
	headerActions?: ReactNode;
	/** "page" lays out a decision that fills the page: a readable column, with what it rests on beside it when there is room. */
	layout?: "panel" | "page";
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
export function DecisionCallPanel({ callId, onClose, headerActions, layout = "panel" }: DecisionCallPanelProps) {
	const { t } = useTranslation();
	const detail = useDecisionDetail(callId);
	const onKeyDown = (event: KeyboardEvent) => {
		if (event.key !== "Escape" || event.defaultPrevented || !onClose) return;
		event.preventDefault();
		onClose();
	};

	let body: ReactNode;
	if (detail.data) {
		body = (
			<>
				{detail.isError ? <RefreshError error={detail.error} onRetry={() => void detail.refetch()} /> : null}
				<DecisionCallDetail call={detail.data.call} history={detail.data.history} layout={layout} onRecheck={() => void detail.refetch()} />
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
			{layout === "page" ? null : (
				<div className="flex h-9 shrink-0 items-center justify-end gap-2 border-b border-border px-2">
					{detail.isFetching && detail.data ? (
						<span role="status" className="mr-auto pl-2 text-caption text-passive">{t("decisions.updating")}</span>
					) : null}
					{headerActions}
					{onClose ? <CloseButton aria-label={t("decisions.close")} size="icon-sm" onClick={onClose} /> : null}
				</div>
			)}
			<div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
				{/*
				  One wrapper in both layouts, so the call below keeps its place in the
				  tree: switching between beside the board and filling the page must
				  not rebuild it and lose what was typed. Filling the page, its tab
				  closes it, and the call lays itself out for the width it has.
				*/}
				<div className={layout === "page" ? "w-full py-2" : undefined}>
					{layout === "page" && detail.isFetching && detail.data ? <p role="status" className="mx-auto max-w-3xl px-4 pt-1 text-caption text-passive">{t("decisions.updating")}</p> : null}
					{body}
				</div>
			</div>
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

/** States in which an answer or overrule still has to reach the owning machine. */
const DECISION_OPEN_STATES = new Set<string>(["needs_you", "in_progress", "decided"]);
const NO_WORKSPACES: DecisionWorkspaceSource["workspaces"] = [];

/** A machine's name as this computer knows it; the id only when nothing names it. */
function machineNamer(call: DecisionCall, sync: DecisionSyncStatus | undefined): (hostId: string) => string {
	return (hostId) => {
		if (hostId === call.hostId) return call.machineLabel || hostId;
		if (sync && hostId === sync.hostId) return sync.machineLabel || hostId;
		return originFreshness(hostId, sync, new Date()).machine;
	};
}

type DetailProps = { call: DecisionCall; history: DecisionHistoryEntry[]; layout: "panel" | "page"; onRecheck: () => void };

/**
 * The call itself. Beside the board it is one column: the question and its
 * answer, then what it rests on, then its history. Filling the page, what it
 * rests on moves to a column of its own when there is room, and the history
 * becomes a timeline. Both are the same elements in the same order, so moving
 * between them never rebuilds the answer form and loses what was typed.
 */
function DecisionCallDetail({ call, history, layout, onRecheck }: DetailProps) {
	const { t } = useTranslation();
	const exactTime = useExactTime();
	const sync = useQuery({ queryKey: decisionSyncKey(), queryFn: ({ signal }) => fetchDecisionSyncStatus(signal) });
	const syncStatus = Array.isArray(sync.data?.peers) ? sync.data : undefined;
	const servingHostId = syncStatus?.hostId;
	const remoteMachine = servingHostId && call.hostId && call.hostId !== servingHostId && DECISION_OPEN_STATES.has(call.state)
		? originFreshness(call.hostId, syncStatus, new Date()).machine
		: undefined;
	const machineName = machineNamer(call, syncStatus);
	const machine = useDecisionMachine(call);
	const workspaces = machine.kind === "ready" ? machine.workspaces : NO_WORKSPACES;
	const textContext = useMemo(() => ({
		workspaces,
		projectId: call.projectId,
		ownerSessionId: call.ownerSessionId,
		pullRequestUrls: [...call.links.map((link) => link.url), ...(call.outcomeLink ? [call.outcomeLink] : [])],
	}), [workspaces, call.projectId, call.ownerSessionId, call.links, call.outcomeLink]);
	const contextParts = useMemo(() => decisionTextParts(call.context, textContext), [call.context, textContext]);
	const outcomeParts = useMemo(() => (call.outcome ? decisionTextParts(call.outcome, textContext) : []), [call.outcome, textContext]);
	const material = useMemo(() => {
		// The reference often names the call's main file; only a file it names counts.
		const referenceFiles = call.reference ? decisionTextParts(call.reference, textContext).filter((part) => part.kind === "artifact") : [];
		return decisionMaterial(call, [...contextParts, ...outcomeParts, ...referenceFiles]);
	}, [call, contextParts, outcomeParts, textContext]);
	const later = useLaterDecisions(call);
	const page = layout === "page";
	const wide = page && (hasDecisionMaterial(material) || later.length > 0);
	return (
		<div className="@container/decision">
			<div className={cn(
				"mx-auto grid w-full grid-cols-1 items-start gap-4 px-4 py-3",
				page && "max-w-3xl",
				wide && "@4xl/decision:max-w-[68rem] @4xl/decision:grid-cols-[minmax(0,1fr)_18rem] @4xl/decision:grid-rows-[auto_1fr] @4xl/decision:gap-x-8",
			)}>
				<div className={cn("flex min-w-0 flex-col gap-4", wide && "@4xl/decision:col-start-1 @4xl/decision:row-start-1")}>
					<header className="flex flex-col gap-1">
						<p className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1 text-caption text-muted-foreground">
							<span className="truncate">{call.projectName || call.projectId}</span>
							{call.streamSessionId ? (
								<>
									<span aria-hidden="true">·</span>
									<DecisionSessionChip machine={machine} sessionId={call.streamSessionId}>{shortSessionName(call.streamName || call.streamSessionId)}</DecisionSessionChip>
								</>
							) : null}
						</p>
						<h2 className="mt-1 text-subtitle font-semibold leading-snug text-foreground">{call.title}</h2>
						{remoteMachine ? (
							<p className="text-caption text-muted-foreground" data-decision-origin-line="">{t("decisions.panel.onMachine", { machine: remoteMachine })}</p>
						) : null}
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
						<RaisedBy call={call} machine={machine} machineName={machineName} />
						{call.changedAfterClose ? (
							<p className="mt-1 flex items-center gap-1.5 text-caption text-status-in-review">
								<TriangleAlert aria-hidden="true" className="size-3.5 shrink-0" />
								{t("decisions.changedAfterClose")}
							</p>
						) : null}
					</header>
					{call.context ? <DecisionText parts={contextParts} machine={machine} className="text-sm leading-relaxed text-foreground" /> : null}
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
					<DecisionCallActions call={call} onRecheck={onRecheck} />
					{call.outcome ? (
						<section aria-label={t("decisions.outcome")} className="text-sm">
							<DecisionText parts={outcomeParts} machine={machine} className="font-medium text-foreground" />
							{call.outcomeLink ? <SafeLink url={call.outcomeLink}>{call.outcomeLink}</SafeLink> : null}
						</section>
					) : null}
				</div>
				<div className={cn("min-w-0 empty:hidden", wide && "@4xl/decision:col-start-2 @4xl/decision:row-span-2 @4xl/decision:row-start-1")}>
					<DecisionSupportingMaterial material={material} later={later} host={decisionMachineHost(machine)} />
				</div>
				<section className={cn("flex min-w-0 flex-col gap-2", wide && "@4xl/decision:col-start-1 @4xl/decision:row-start-2")}>
					<h3 className="text-caption font-medium text-muted-foreground">{t("decisions.history")}</h3>
					<ol aria-label={t("decisions.history")} data-history-style={page ? "timeline" : "list"} className={cn("flex flex-col", page ? "ml-1 gap-3 border-l border-border-strong pl-4" : "gap-2.5")}>
						{history.map((item) => (
							<li
								key={item.id}
								className={cn("text-caption", page && "relative before:absolute before:top-1.5 before:-left-[20.5px] before:size-[7px] before:rounded-full before:border before:border-muted-foreground before:bg-background")}
							>
								<p data-slot="history-what" className="text-sm text-foreground"><HistoryWhat item={item} call={call} machine={machine} /></p>
								{item.note ? <p className="whitespace-pre-wrap break-words text-muted-foreground">{item.note}</p> : null}
								{item.kind === "outcome" && item.link ? <SafeLink url={item.link}>{item.link}</SafeLink> : null}
								<p className="flex flex-wrap items-center gap-x-1 gap-y-0.5 text-muted-foreground">
									<HistoryWho item={item} call={call} machine={machine} machineName={machineName} />
									<span aria-hidden="true">·</span>
									<time dateTime={item.writtenAt}>{exactTime(item.writtenAt)}</time>
								</p>
							</li>
						))}
					</ol>
				</section>
			</div>
		</div>
	);
}

/** The raising session's state in a word, or why it cannot be opened; nothing while that is still being checked. */
function ownerStatusText(target: DecisionSessionTarget, status: string | undefined, t: TFunction): string | undefined {
	switch (target.kind) {
		case "open":
			if (target.terminated) return t("decisions.sessionEnded");
			return status ? getSessionStatusView(toSessionStatus(status), t).label : undefined;
		case "offline": return t("decisions.sessionOffline", { machine: target.machine });
		case "missing": return t("decisions.sessionMissing");
		case "error": return t("decisions.sessionCheckFailed");
		case "checking": return undefined;
	}
}

/**
 * Who raised the call, as a link to that session with its state in a word,
 * and the managers above it. It replaces a separate Open session button.
 */
function RaisedBy({ call, machine, machineName }: { call: DecisionCall; machine: DecisionMachine; machineName: (hostId: string) => string }) {
	const { t } = useTranslation();
	const target = decisionSessionTarget(machine, call.ownerSessionId);
	const owner = findDecisionSession(machine, call.ownerSessionId)?.session;
	const chain = decisionSessionChain(machine, call.ownerSessionId);
	const status = ownerStatusText(target, owner?.status, t);
	return (
		<>
			<p data-decision-raised-by="" className="mt-1 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1 text-caption text-muted-foreground">
				<span>{t("decisions.raisedBy")}</span>
				<DecisionSessionChip machine={machine} sessionId={call.ownerSessionId}>{shortSessionName(call.ownerSessionName || call.ownerSessionId)}</DecisionSessionChip>
				{status ? <span>{status}</span> : null}
				<span aria-hidden="true">·</span>
				<span>{machineName(call.hostId)}</span>
			</p>
			{chain.length > 0 ? (
				<p className="flex min-w-0 flex-wrap items-center gap-x-1 gap-y-1 text-caption text-muted-foreground">
					<span>{t("decisions.under")}</span>
					{chain.map((session, index) => (
						<span key={session.id} className="inline-flex min-w-0 items-center gap-1">
							{index > 0 ? <span aria-hidden="true" className="text-passive">›</span> : null}
							<DecisionSessionChip machine={machine} sessionId={session.id}>
								{session.kind === "orchestrator" ? t("shell.orchestrator") : shortSessionName(session.title || session.id)}
							</DecisionSessionChip>
						</span>
					))}
				</p>
			) : null}
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
function HistoryWhat({ item, call, machine }: { item: DecisionHistoryEntry; call: DecisionCall; machine: DecisionMachine }): ReactNode {
	const { t } = useTranslation();
	switch (item.kind) {
		case "raised": return t("decisions.history.raised");
		case "answered": {
			const choice = item.choice === "other"
				? t("decisions.somethingElse")
				: call.options[(item.option ?? 0) - 1]?.label ?? String(item.option ?? "");
			return t("decisions.history.answered", { choice });
		}
		case "dismissed": return t("decisions.history.dismissed");
		case "delivered": {
			if (item.status === "no_session") return t("decisions.noSession");
			const session = shortSessionName(item.sessionName || item.sessionId || "");
			if (!item.sessionId) return t(item.fallback ? "decisions.sentToFallback" : "decisions.sentTo", { session });
			return (
				<Trans
					t={t}
					i18nKey={item.fallback ? "decisions.history.sentToFallback" : "decisions.history.sentTo"}
					values={{ session }}
					components={{ session: <DecisionSessionChip machine={machine} sessionId={item.sessionId} /> }}
				/>
			);
		}
		case "outcome": return t("decisions.history.outcome", { text: item.text ?? "" });
		case "withdrawn": return t("decisions.history.withdrawn");
		case "retired": return t(item.retired ? "decisions.history.retired" : "decisions.history.unretired");
		case "decided": return t("decisions.history.decided", { text: item.text ?? call.title });
		case "overruled": return t("decisions.history.overruled");
		case "carried_out": return t("decisions.history.carriedOut");
		default: return t("decisions.history.unknown", { kind: item.kind });
	}
}

/** Who wrote an entry and on which machine; a session is a link to it. */
function HistoryWho({ item, call, machine, machineName }: { item: DecisionHistoryEntry; call: DecisionCall; machine: DecisionMachine; machineName: (hostId: string) => string }) {
	const { t } = useTranslation();
	const where = machineName(item.hostId);
	if (item.authorKind === "person") {
		const surface = item.authorSurface === "desktop" || item.authorSurface === "cli" || item.authorSurface === "mobile"
			? t(`decisions.surface.${item.authorSurface}`)
			: item.authorSurface;
		return <span>{[t("decisions.author.person"), surface, where].filter(Boolean).join(" · ")}</span>;
	}
	if (item.authorKind === "session" && item.authorSession) {
		const name = item.authorSession === call.ownerSessionId ? call.ownerSessionName || call.ownerSessionId : item.authorSession;
		return (
			<>
				<DecisionSessionChip machine={machine} sessionId={item.authorSession}>{shortSessionName(name)}</DecisionSessionChip>
				<span aria-hidden="true">·</span>
				<span>{where}</span>
			</>
		);
	}
	return <span>{[t("decisions.author.system"), where].filter(Boolean).join(" · ")}</span>;
}
