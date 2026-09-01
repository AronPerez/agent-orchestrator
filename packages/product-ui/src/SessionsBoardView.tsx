import {
	Fragment,
	forwardRef,
	memo,
	startTransition,
	useEffect,
	useState,
	type HTMLAttributes,
	type ReactElement,
	type ReactNode,
} from "react";
import { motion, useReducedMotion } from "motion/react";
import type { ExternalLinkComponent } from "./external-link";
import {
	ChevronIcon,
	GitBranchIcon,
	GitPullRequestIcon,
	LoaderCircleIcon,
	MessageSquareIcon,
} from "./icons";
import {
	attentionZone,
	getAgentActivityView,
	getDisplayStatusLabel,
	getKanbanColumnView,
	getSessionStatusView,
	toKanbanColumn,
	type AttentionZone,
	type AttentionZoneView,
	type KanbanColumnView,
	type ProductUITranslator,
} from "./session-presentation";
import type { KanbanColumn, SessionActivity, SessionStatus } from "./session-models";
import { cn } from "./utils";

export type BoardSessionPresentation = {
	activity?: SessionActivity;
	branch?: string;
	id: string;
	/**
	 * Daemon-derived lane placement. Absent only for fixtures and for a daemon
	 * too old to send one; {@link toKanbanColumn} then keeps the placement the
	 * session's status already implied.
	 */
	kanbanColumn?: KanbanColumn;
	/**
	 * Daemon-derived phrase for what is happening inside {@link kanbanColumn}
	 * ("Fixing CI failures", "Needs human review"), replacing {@link status} as
	 * the card's status text. Translated via `getDisplayStatusLabel` for known
	 * phrases (see {@link DisplayStatus}); an unrecognized one -- a newer
	 * daemon that shipped a phrase before this build -- renders as the raw,
	 * already-renderable English text the API guarantees. The card styles the
	 * phrase with its daemon-owned Kanban column, so presentation never has to
	 * infer lifecycle semantics from human-readable copy. A daemon too old to
	 * send one falls back to the translated {@link status} label.
	 */
	displayStatus?: string;
	provider: string;
	status: SessionStatus;
	statusPresentation?: BoardSessionStatusPresentation;
	title: string;
	trackerIssueId?: string;
	updatedAt: string;
	lastUserMessageAt?: string;
};

export type BoardSessionStatusPresentation = {
	className: string;
	indicatorClassName: string;
	label: string;
	tone?: string;
};

export type BoardPullRequestState = "closed" | "open" | "draft" | "merged";

export type BoardReviewerAvatar = {
	login: string;
	url?: string;
};

export type BoardPullRequestPresentation = {
	commentCount?: number;
	number: number;
	reviewerAvatars?: BoardReviewerAvatar[];
	state: BoardPullRequestState;
	url: string;
};

export type BoardUsagePresentation = {
	accessibleLabel: string;
	compactLabel: string;
};

export type BoardPullRequestLabels = {
	short: string;
	states: Record<BoardPullRequestState, string>;
};

export type BoardColumnLabels = {
	columnAria: (label: string) => string;
};

export type BoardSplitLaneLabels = {
	columnAria: (label: string) => string;
	countSessions: (count: number, label: string) => string;
	idleWorkingAria: string;
	laneSummary: (primary: string, secondary: string) => string;
	readyMergedAria: string;
	tones: {
		idle: BoardSplitLaneToneLabels;
		merged: BoardSplitLaneToneLabels;
		ready: BoardSplitLaneToneLabels;
		working: BoardSplitLaneToneLabels;
	};
};

export type BoardSplitLaneToneLabels = {
	countLabel: string;
	label: string;
	regionLabel: string;
};

function SessionsBoardSplitLaneGridView<TSession extends BoardSessionPresentation>({
	columns,
	labels,
	renderSessionCard,
	sessions,
}: SessionsBoardSplitLaneGridViewProps<TSession>) {
	const byZone = new Map<AttentionZone, TSession[]>();
	for (const session of sessions) {
		const zone = attentionZone(session.status);
		const sessionsForZone = byZone.get(zone);
		if (sessionsForZone) sessionsForZone.push(session);
		else byZone.set(zone, [session]);
	}

	return (
		<div
			className="board-horizontal-scrollbar h-full overflow-x-auto overflow-y-hidden"
			data-testid="board-horizontal-scroll"
		>
			<div className="relative grid h-full min-w-[64rem] grid-cols-4 divide-x divide-border-strong xl:min-w-0">
				<div
					aria-hidden="true"
					className="pointer-events-none absolute inset-x-0 top-12 z-10 border-t border-border-strong"
				/>
				{columns.map((column) => (
					<SplitLaneBoardColumnView
						column={column}
						key={column.zone}
						labels={labels}
						renderSessionCard={renderSessionCard}
						sessions={byZone.get(column.zone) ?? []}
					/>
				))}
			</div>
		</div>
	);
}

function SplitLaneBoardColumnView<TSession extends BoardSessionPresentation>({
	column,
	labels,
	renderSessionCard,
	sessions,
}: {
	column: AttentionZoneView;
	labels: BoardSplitLaneLabels;
	renderSessionCard: (session: TSession) => ReactNode;
	sessions: TSession[];
}) {
	if (column.zone === "working") {
		const idleSessions = sessions.filter((session) => session.status === "idle");
		const workingSessions = sessions.filter((session) => session.status !== "idle");
		return (
			<SplitLaneColumnView
				ariaLabel={labels.idleWorkingAria}
				countSessions={labels.countSessions}
				laneSummary={labels.laneSummary}
				primarySessions={idleSessions}
				primaryTone={splitLaneTone("idle", labels.tones.idle)}
				renderSessionCard={renderSessionCard}
				secondarySessions={workingSessions}
				secondaryTone={splitLaneTone("working", labels.tones.working)}
				zone="working"
			/>
		);
	}
	if (column.zone === "merge") {
		const mergedSessions = sessions
			.filter((session) => session.status === "merged")
			.sort((left, right) =>
				(right.lastUserMessageAt ?? "").localeCompare(left.lastUserMessageAt ?? ""),
			);
		const readySessions = sessions
			.filter((session) => session.status !== "merged")
			.sort((left, right) =>
				(right.lastUserMessageAt ?? "").localeCompare(left.lastUserMessageAt ?? ""),
			);
		return (
			<SplitLaneColumnView
				ariaLabel={labels.readyMergedAria}
				countSessions={labels.countSessions}
				laneSummary={labels.laneSummary}
				primarySessions={readySessions}
				primaryTone={splitLaneTone("ready", labels.tones.ready)}
				renderSessionCard={renderSessionCard}
				secondarySessions={mergedSessions}
				secondaryTone={splitLaneTone("merged", labels.tones.merged)}
				zone="merge"
			/>
		);
	}
	return (
		<section
			aria-label={labels.columnAria(column.label)}
			className="flex min-w-0 flex-col overflow-hidden"
			data-testid="board-column"
			data-column={column.zone}
		>
			<div className="flex h-12 shrink-0 items-center gap-2.5 px-4">
				<span
					className="size-dot-sm rounded-full"
					style={{
						background: column.dot,
						boxShadow: column.dotGlow
							? `0 0 7px color-mix(in srgb, ${column.dot} 60%, transparent)`
							: undefined,
					}}
				/>
				<span className={cn("font-mono text-2xs font-medium uppercase tracking-wide-sm", column.titleClassName)}>
					{column.label}
				</span>
				<span className="ml-auto font-mono text-2xs leading-none text-passive">{sessions.length}</span>
			</div>
			<div className="board-scrollbar min-h-0 flex-1 overflow-y-auto px-3 pb-3 pt-3">
				<div className="flex min-h-full flex-col gap-2.5">
					{sessions.map((session) => (
						<Fragment key={session.id}>{renderSessionCard(session)}</Fragment>
					))}
				</div>
			</div>
		</section>
	);
}

type SplitLaneTone = BoardSplitLaneToneLabels & {
	color: string;
	dotClassName: string;
	dotGlow: boolean;
	titleClassName: string;
};

function splitLaneTone(
	tone: "idle" | "working" | "ready" | "merged",
	labels: BoardSplitLaneToneLabels,
): SplitLaneTone {
	const styles = {
		idle: {
			color: "var(--color-status-idle)",
			dotClassName: "bg-status-idle",
			dotGlow: false,
			titleClassName: "text-status-idle",
		},
		working: {
			color: "var(--color-status-working)",
			dotClassName: "bg-status-working",
			dotGlow: true,
			titleClassName: "text-status-working",
		},
		ready: {
			color: "var(--color-status-ready)",
			dotClassName: "bg-status-ready",
			dotGlow: true,
			titleClassName: "text-status-ready",
		},
		merged: {
			color: "var(--color-status-merged)",
			dotClassName: "bg-status-merged",
			dotGlow: false,
			titleClassName: "text-status-merged",
		},
	} as const;
	return { ...labels, ...styles[tone] };
}

function SplitLaneColumnView<TSession extends BoardSessionPresentation>({
	ariaLabel,
	countSessions,
	laneSummary,
	primarySessions,
	primaryTone,
	renderSessionCard,
	secondarySessions,
	secondaryTone,
	zone,
}: {
	ariaLabel: string;
	countSessions: BoardSplitLaneLabels["countSessions"];
	laneSummary: BoardSplitLaneLabels["laneSummary"];
	primarySessions: TSession[];
	primaryTone: SplitLaneTone;
	renderSessionCard: (session: TSession) => ReactNode;
	secondarySessions: TSession[];
	secondaryTone: SplitLaneTone;
	zone: Extract<AttentionZone, "working" | "merge">;
}) {
	const showPrimary = primarySessions.length > 0;
	const showSecondary = secondarySessions.length > 0;
	return (
		<section
			aria-label={ariaLabel}
			className="flex min-w-0 flex-col overflow-hidden"
			data-column={zone}
			data-testid="board-column"
		>
			<div className="flex h-12 shrink-0 items-center gap-2.5 px-4">
				<div
					aria-label={laneSummary(primaryTone.label, secondaryTone.label)}
					className="flex min-w-0 items-center gap-2 font-mono text-2xs font-medium uppercase tracking-wide-sm"
					role="group"
				>
					<LaneStatusLabel tone={primaryTone} />
					<span className="text-passive" aria-hidden="true">/</span>
					<LaneStatusLabel tone={secondaryTone} />
				</div>
				<div className="ml-auto flex shrink-0 items-center gap-2 font-mono text-2xs leading-none text-passive">
					<SessionCount count={primarySessions.length} label={primaryTone.countLabel} format={countSessions} />
					<span aria-hidden="true">/</span>
					<SessionCount count={secondarySessions.length} label={secondaryTone.countLabel} format={countSessions} />
				</div>
			</div>
			<div className="board-scrollbar min-h-0 flex-1 overflow-y-auto px-3 pb-3 pt-3">
				<div className="flex min-h-full flex-col">
					{showPrimary ? (
						<div
							aria-label={primaryTone.regionLabel}
							className={cn("flex flex-col", showSecondary ? "flex-none pb-3" : "flex-1")}
							role="region"
						>
							<div className="flex flex-col gap-2.5">
								{primarySessions.map((session) => (
									<Fragment key={session.id}>{renderSessionCard(session)}</Fragment>
								))}
							</div>
						</div>
					) : null}
					{showSecondary ? (
						<SecondaryLaneSection
							renderSessionCard={renderSessionCard}
							sessions={secondarySessions}
							standalone={!showPrimary}
							tone={secondaryTone}
						/>
					) : null}
				</div>
			</div>
		</section>
	);
}

function LaneStatusLabel({ tone }: { tone: SplitLaneTone }) {
	return (
		<span className={cn("inline-flex shrink-0 items-center gap-2 whitespace-nowrap", tone.titleClassName)}>
			<span
				aria-hidden="true"
				className={cn("size-dot-sm rounded-full", tone.dotClassName)}
				style={{
					boxShadow: tone.dotGlow
						? `0 0 7px color-mix(in srgb, ${tone.color} 60%, transparent)`
						: undefined,
				}}
			/>
			{tone.label}
		</span>
	);
}

function SessionCount({
	count,
	format,
	label,
}: {
	count: number;
	format: BoardSplitLaneLabels["countSessions"];
	label: string;
}) {
	return <span aria-label={format(count, label)}>{count}</span>;
}

function SecondaryLaneSection<TSession extends BoardSessionPresentation>({
	renderSessionCard,
	sessions,
	standalone,
	tone,
}: {
	renderSessionCard: (session: TSession) => ReactNode;
	sessions: TSession[];
	standalone: boolean;
	tone: SplitLaneTone;
}) {
	return (
		<div
			aria-label={tone.regionLabel}
			className={cn(
				"overflow-hidden",
				standalone ? "flex flex-1 flex-col" : "flex flex-1 flex-col border-t border-border-strong",
			)}
			role="region"
		>
			<div className="flex shrink-0 items-center gap-2.5 px-4 py-2.5">
				<div className="font-mono text-2xs font-medium uppercase tracking-wide-sm">
					<LaneStatusLabel tone={tone} />
				</div>
				<span className="ml-auto font-mono text-2xs leading-none text-passive">{sessions.length}</span>
			</div>
			<div className="flex flex-col gap-2.5 pt-3">
				{sessions.map((session) => (
					<Fragment key={session.id}>{renderSessionCard(session)}</Fragment>
				))}
			</div>
		</div>
	);
}


type SessionsBoardKanbanGridViewProps<
	TSession extends BoardSessionPresentation = BoardSessionPresentation,
> = {
	columns: KanbanColumnView[];
	labels: BoardColumnLabels;
	renderSessionCard: (session: TSession) => ReactNode;
	sessions: TSession[];
};

export type SessionsBoardSplitLaneGridViewProps<
	TSession extends BoardSessionPresentation = BoardSessionPresentation,
> = {
	columns: AttentionZoneView[];
	labels: BoardSplitLaneLabels;
	renderSessionCard: (session: TSession) => ReactNode;
	sessions: TSession[];
};

export type SessionsBoardGridViewProps<
	TSession extends BoardSessionPresentation = BoardSessionPresentation,
> = SessionsBoardKanbanGridViewProps<TSession> | SessionsBoardSplitLaneGridViewProps<TSession>;

function usesSplitLanes<TSession extends BoardSessionPresentation>(
	props: SessionsBoardGridViewProps<TSession>,
): props is SessionsBoardSplitLaneGridViewProps<TSession> {
	return "idleWorkingAria" in props.labels;
}

export function SessionsBoardGridView<TSession extends BoardSessionPresentation>(
	props: SessionsBoardGridViewProps<TSession>,
) {
	return usesSplitLanes(props) ? (
		<SessionsBoardSplitLaneGridView {...props} />
	) : (
		<SessionsBoardKanbanGridView {...props} />
	);
}

function SessionsBoardKanbanGridView<TSession extends BoardSessionPresentation>({
	columns,
	labels,
	renderSessionCard,
	sessions,
}: SessionsBoardKanbanGridViewProps<TSession>) {
	const byColumn = new Map<KanbanColumn, TSession[]>();
	for (const session of sessions) {
		const column = toKanbanColumn(session.kanbanColumn, session.status);
		const sessionsForColumn = byColumn.get(column);
		if (sessionsForColumn) sessionsForColumn.push(session);
		else byColumn.set(column, [session]);
	}

	return (
		<div
			className="board-horizontal-scrollbar h-full overflow-x-auto overflow-y-hidden"
			data-testid="board-horizontal-scroll"
		>
			<div className="relative grid h-full min-w-[64rem] grid-cols-4 divide-x divide-border-strong xl:min-w-0">
				<div
					aria-hidden="true"
					className="pointer-events-none absolute inset-x-0 top-12 z-10 border-t border-border-strong"
				/>
				{columns.map((column) => (
					<BoardColumnView
						column={column}
						key={column.column}
						labels={labels}
						renderSessionCard={renderSessionCard}
						sessions={byColumn.get(column.column) ?? []}
					/>
				))}
			</div>
		</div>
	);
}

function BoardColumnView<TSession extends BoardSessionPresentation>({
	column,
	labels,
	renderSessionCard,
	sessions,
}: {
	column: KanbanColumnView;
	labels: BoardColumnLabels;
	renderSessionCard: (session: TSession) => ReactNode;
	sessions: TSession[];
}) {
	const ordered = [...sessions].sort((left, right) => {
		const attentionPriority =
			Number(boardSessionNeedsAttention(right)) - Number(boardSessionNeedsAttention(left));
		return attentionPriority || right.updatedAt.localeCompare(left.updatedAt);
	});
	return (
		<section
			aria-label={labels.columnAria(column.label)}
			className="flex min-w-0 flex-col overflow-hidden"
			data-testid="board-column"
			data-column={column.column}
		>
			<div className="flex h-12 shrink-0 items-center gap-2.5 px-4">
				<span
					data-testid="board-column-swatch"
					className="size-[var(--size-swatch)] rounded-full"
					style={{ backgroundColor: column.dot }}
				/>
				<span className={cn("text-xs font-medium", column.titleClassName)}>
					{column.label}
				</span>
				<span className="ml-auto tabular-nums text-xs leading-none text-passive">{ordered.length}</span>
			</div>
			<div className="board-scrollbar min-h-0 flex-1 overflow-y-auto pl-3 pr-2 pb-3 pt-3">
				<div className="flex min-h-full flex-col gap-2.5">
					{ordered.map((session) => (
						<Fragment key={session.id}>{renderSessionCard(session)}</Fragment>
					))}
				</div>
			</div>
		</section>
	);
}

export type SessionCardViewProps = {
	action?: ReactNode;
	branchAction?: ReactNode;
	branchIcon?: ReactNode;
	error?: string;
	externalLink: ExternalLinkComponent;
	footer?: ReactNode;
	interactive?: boolean;
	compactPullRequestLabels?: boolean;
	labels: {
		formatTime: (timestamp: string) => string;
		intakeIssue: (id: string) => string;
		pr: BoardPullRequestLabels;
		updatedAt: (timestamp: string) => string;
	};
	onOpen?: () => void;
	overlay?: ReactNode;
	prs?: BoardPullRequestPresentation[];
	renderAvatar: (provider: string) => ReactNode;
	renderUsage?: (usage: BoardUsagePresentation) => ReactNode;
	session: BoardSessionPresentation;
	showTrackerIssue?: boolean;
	translate?: ProductUITranslator;
	usage?: BoardUsagePresentation;
};

export function SessionCardView({
	action,
	branchAction,
	branchIcon,
	error,
	externalLink,
	footer,
	interactive = true,
	compactPullRequestLabels = false,
	labels,
	onOpen,
	overlay,
	prs = [],
	renderAvatar,
	renderUsage = (usage) => <SessionUsageMetricView usage={usage} />,
	session,
	showTrackerIssue = false,
	translate,
	usage,
}: SessionCardViewProps) {
	const badge = getSessionStatusView(session.status, translate);
	const activity = getAgentActivityView(session.activity, translate);
	const showLiveActivity = session.status === "working" && activity.state === "active";
	const statusPresentation = session.statusPresentation;
	const needsAttention = boardSessionNeedsAttention(session);
	const needsAttentionChip = needsAttention;
	const column = getKanbanColumnView(toKanbanColumn(session.kanbanColumn, session.status), translate);
	const branch = session.branch ?? "";
	const showBranch = branch !== "" && !sameLabel(branch, session.title) && !sameLabel(branch, session.id);
	const renderedStatusLabel =
		statusPresentation?.label ??
		(session.displayStatus ? getDisplayStatusLabel(session.displayStatus, translate) : badge.label);
	const showStatusLoader =
		Boolean(session.displayStatus) &&
		!needsAttention &&
		session.displayStatus !== "Needs human review" &&
		(session.status === "working" ||
			session.status === "review_pending" ||
			session.displayStatus === "Fixing CI failures" ||
			session.displayStatus === "Addressing comments" ||
			session.displayStatus === "Reviewing");

	return (
		<div
			onClick={interactive ? onOpen : undefined}
			role={interactive ? undefined : "listitem"}
			className={cn(
				"group relative w-full rounded-lg border border-border text-left transition-[background-color,box-shadow,transform] duration-[120ms] ease-out",
				badge.cardClassName ?? "border-border bg-surface",
				interactive &&
					"cursor-pointer hover:bg-interactive-hover focus-within:bg-interactive-hover active:scale-[0.99] has-[.pr-link:active]:scale-100",
				needsAttention &&
					"animate-attention-card-pulse border-status-needs-you bg-[color-mix(in_srgb,var(--color-status-needs-you)_8%,var(--color-surface))]",
			)}
			data-testid="board-session-card"
			data-session-id={session.id}
		>
			{interactive && onOpen ? (
				<button
					aria-label={session.title}
					className="pointer-events-none absolute inset-0 outline-none"
					type="button"
				/>
			) : null}
			{overlay}
			{action ? <div className="absolute right-2 top-1.5 z-10">{action}</div> : null}
			<div className="px-3.5 pb-2.5 pt-3">
				<div className="flex min-w-0 items-start gap-2.5">
					{renderAvatar(session.provider)}
					<div className="min-w-0 flex-1">
						<div
							className={cn(
								"line-clamp-2 overflow-hidden text-balance text-sm-md font-semibold leading-tight tracking-tight text-foreground",
								(overlay || action) && "pr-6",
							)}
							title={session.title}
						>
							{session.title}
						</div>
					</div>
				</div>
				{showBranch && (
					<div className="mt-1.5 flex min-w-0 items-center gap-1.5 font-mono text-2xs text-muted-foreground">
						{branchIcon ?? <GitBranchIcon aria-hidden="true" className="size-icon-2xs shrink-0" />}
						<span className="truncate text-muted-foreground">{branch}</span>
						{branchAction}
					</div>
				)}
			</div>
			{compactPullRequestLabels && prs.length > 0 ? (
				<div aria-hidden="true" className="mx-3.5 my-px h-px bg-border" />
			) : null}
			{prs.length > 0 && (
				<div className="flex min-w-0 flex-col gap-1.5 px-3.5 pb-1">
					{prs.length > 0 && (
						<div className="flex min-w-0 flex-col flex-wrap gap-y-1 font-mono text-2xs text-muted-foreground">
							{groupBoardPullRequests(prs).flatMap((group) => {
								const compact = group.prs.filter((pr) => (pr.commentCount ?? 0) === 0);
								const commented = group.prs.filter((pr) => (pr.commentCount ?? 0) > 0);
								return [
									...(compact.length > 0 ? [{ ...group, prs: compact }] : []),
									...commented.map((pr) => ({ ...group, prs: [pr] })),
								];
							}).map((group) => (
								<BoardPullRequestGroup
									externalLink={externalLink}
									group={group}
									key={`${group.state}-${group.prs.map((pr) => pr.url || pr.number).join("-")}`}
									labels={labels.pr}
								/>
							))}
						</div>
					)}
				</div>
			)}
			{showTrackerIssue && session.trackerIssueId ? (
				<div className="px-3.5 pb-2">
					<span
						className="inline-flex max-w-branch-chip items-center truncate rounded-sm bg-accent/12 px-1.5 py-0.5 font-mono text-micro text-accent"
						title={labels.intakeIssue(session.trackerIssueId)}
					>
						{session.trackerIssueId}
					</span>
				</div>
			) : null}
			<div className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 border-t border-border px-3.5 py-2.5">
				<div className="flex min-w-0 flex-1">
					<span
						className={cn(
							"inline-flex min-w-0 max-w-full items-center text-2xs font-medium",
							statusPresentation
								? statusPresentation.className
								: !session.displayStatus
									? badge.className
									: needsAttentionChip
										? "text-status-needs-you"
										: session.status === "mergeable" || session.displayStatus === "Mergeable"
											? "text-success"
											: column.titleClassName,
						)}
						style={!statusPresentation && !session.displayStatus && showLiveActivity ? { color: activity.tone } : undefined}
						data-kanban-column={statusPresentation ? undefined : column.column}
						data-testid="session-status"
					>
						{statusPresentation || !session.displayStatus ? (
							<span
								aria-hidden="true"
								className={cn(
									"mr-1 size-dot-sm shrink-0 rounded-full",
									statusPresentation?.indicatorClassName ??
										(showLiveActivity ? activity.indicatorClassName : "bg-current"),
								)}
							/>
						) : showStatusLoader ? (
							<LoaderCircleIcon aria-hidden="true" className="mr-1 size-icon-2xs animate-spin" />
						) : null}
						<span className="min-w-0 truncate">
							{renderedStatusLabel}
						</span>
					</span>
				</div>
				<div className="ml-auto flex shrink-0 items-center gap-2 whitespace-nowrap text-2xs text-muted-foreground">
					{usage ? renderUsage(usage) : null}
					{usage ? <span aria-hidden="true" className="text-border-strong">·</span> : null}
					<span className="tabular-nums text-muted-foreground" title={labels.updatedAt(session.updatedAt)}>
						{labels.formatTime(session.updatedAt)}
					</span>
				</div>
			</div>
			{error ? (
				<div className="border-t border-border px-3.5 py-1.5 text-2xs text-destructive" role="alert">
					{error}
				</div>
			) : null}
			{footer}
		</div>
	);
}

function boardSessionNeedsAttention(session: BoardSessionPresentation): boolean {
	if (session.statusPresentation) return false;
	switch (session.displayStatus) {
		case "Blocked":
		case "CI failing":
		case "Changes requested":
			return true;
		case undefined:
			return (
				attentionZone(session.status) === "action" || session.activity?.state === "blocked"
			);
		default:
			return false;
	}
}

export const SessionUsageMetricView = forwardRef<
	HTMLSpanElement,
	{ usage: BoardUsagePresentation } & HTMLAttributes<HTMLSpanElement>
>(({ className, usage, ...props }, ref) => (
	<span
		{...props}
		className={cn(
			"inline-flex shrink-0 items-center gap-1 whitespace-nowrap font-mono text-2xs text-muted-foreground",
			className,
		)}
		ref={ref}
	>
		{/* aria-label on a generic span is not reliably exposed, so the full
		    label is real text placed off-screen and the compact form is hidden
		    from assistive technology rather than read out twice. */}
		<span className="sr-only">{usage.accessibleLabel}</span>
		<span aria-hidden="true">{usage.compactLabel}</span>
	</span>
));
SessionUsageMetricView.displayName = "SessionUsageMetricView";

type BoardPullRequestGroupModel = {
	prs: BoardPullRequestPresentation[];
	state: BoardPullRequestState;
};

function BoardPullRequestGroup({
	externalLink: ExternalLink,
	group,
	labels,
}: {
	externalLink: ExternalLinkComponent;
	group: BoardPullRequestGroupModel;
	labels: BoardPullRequestLabels;
}) {
	const statusLabel = labels.states[group.state];
	const linkClassName = "pr-link hover:underline";
	return (
		<div
			aria-label={`${group.prs.map((pr) => `#${pr.number}`).join(", ")} ${statusLabel}`}
			className="flex min-w-0 items-center gap-x-2"
		>
			{group.prs.map((pr) => {
				const hasComments = (pr.commentCount ?? 0) > 0;
				return (
					<Fragment key={pr.url || pr.number}>
						<ExternalLink
							ariaLabel={`PR #${pr.number} ${statusLabel}`}
							className={cn("inline-flex min-w-0 items-center gap-x-2 py-0.5", linkClassName)}
							href={pr.url}
							stopPropagation
						>
			<PullRequestLifecycleIcon state={group.state} />
			<span className="sr-only">{labels.short}</span>
			<span className="font-mono text-xs font-medium text-foreground">#{pr.number}</span>
			<span className={cn("sr-only", lifecycleClassName(group.state))}>{statusLabel}</span>
			{hasComments ? (
				<div className="-ml-0.5 flex shrink-0 items-center pl-1">
					{(pr.reviewerAvatars ?? [])
						.slice(0, 3)
						.map((avatar, index) => (
							<ReviewerAvatar
								avatar={avatar}
								className={index > 0 ? "-ml-1.5" : undefined}
								key={`${avatar.login}-${index}`}
							/>
						))}
				</div>
			) : null}
						</ExternalLink>
						{hasComments ? (
							<ExternalLink
								ariaLabel={`${pr.commentCount} comments on PR #${pr.number}`}
								className={cn("ml-auto inline-flex shrink-0 items-center gap-1 text-xs tabular-nums text-muted-foreground", linkClassName)}
								href={pr.url}
								stopPropagation
							>
								<MessageSquareIcon aria-hidden="true" className="size-icon-2xs" />
								{pr.commentCount}
							</ExternalLink>
						) : null}
					</Fragment>
				);
			})}
		</div>
	);
}

function reviewerInitials(login: string): string {
	return login
		.replace(/^@/, "")
		.trim()
		.split(/[-_\s]+/)
		.filter(Boolean)
		.slice(0, 2)
		.map((part) => part[0]?.toUpperCase() ?? "")
		.join("") || "?";
}

function ReviewerAvatar({ avatar, className }: { avatar: BoardReviewerAvatar; className?: string }) {
	const [failed, setFailed] = useState(false);
	const commonClassName = cn("size-5 rounded-full border-2 border-surface ring-1 ring-border", className);
	if (avatar.url && !failed) {
		return (
			<img
				alt=""
				className={cn(commonClassName, "object-cover")}
				onError={() => setFailed(true)}
				referrerPolicy="no-referrer"
				src={avatar.url}
			/>
		);
	}
	return <span aria-hidden="true" className={cn(commonClassName, "inline-flex items-center justify-center bg-muted text-[9px] font-semibold text-muted-foreground")}>{reviewerInitials(avatar.login)}</span>;
}

function PullRequestLifecycleIcon({ state }: { state: BoardPullRequestState }) {
	const className = cn("size-icon-sm shrink-0", lifecycleClassName(state));
	return <GitPullRequestIcon aria-hidden="true" className={className} />;
}

export function groupBoardPullRequests(
	prs: BoardPullRequestPresentation[],
): BoardPullRequestGroupModel[] {
	const groups = new Map<BoardPullRequestState, BoardPullRequestGroupModel>();
	for (const pr of prs) {
		const group = groups.get(pr.state);
		if (group) group.prs.push(pr);
		else groups.set(pr.state, { state: pr.state, prs: [pr] });
	}
	return Array.from(groups.values());
}

function lifecycleClassName(state: BoardPullRequestState): string {
	switch (state) {
		case "draft":
			return "text-passive";
		case "merged":
			return "text-status-merged";
		case "closed":
			return "text-error";
		case "open":
			return "text-success";
	}
}

/**
 * Collapsed archive toggle height. The overlay bar and the board's bottom
 * padding must stay in lockstep so the archive neither overlaps lanes nor
 * leaves a gap.
 */
export const ARCHIVE_TOGGLE_HEIGHT_PX = 58;
export const archiveToggleHeightClassName = "h-[58px]";
export const archiveToggleOffsetClassName = "pb-[58px]";

/**
 * Archive lives in its own memo'd component so expand/collapse state does not
 * re-render the kanban columns. Card mount is deferred via startTransition on
 * first open; after that the sheet stays mounted and open/close only tweens
 * Motion height 0↔auto (collapsed: inert / non-interactive). Overlay
 * positioning keeps lane height stable while expanded.
 */
export const SessionsArchiveView = memo(function SessionsArchiveView<
	TSession extends BoardSessionPresentation,
>({
	labels,
	renderSessionCard,
	resetKey,
	sessions,
}: {
	labels: {
		archive: string;
		archiveAria: string;
		archivedSessions: string;
	};
	renderSessionCard: (session: TSession) => ReactNode;
	/** Collapse and drop deferred cards when the board scope changes (e.g. projectId). */
	resetKey?: string;
	sessions: TSession[];
}) {
	const prefersReducedMotion = useReducedMotion();
	const [expanded, setExpanded] = useState(false);
	const [cardsReady, setCardsReady] = useState(false);

	useEffect(() => {
		setExpanded(false);
		setCardsReady(false);
	}, [resetKey]);

	useEffect(() => {
		if (!expanded || cardsReady) return;
		let cancelled = false;
		const id = requestAnimationFrame(() => {
			startTransition(() => {
				if (!cancelled) setCardsReady(true);
			});
		});
		return () => {
			cancelled = true;
			cancelAnimationFrame(id);
		};
	}, [expanded, cardsReady]);

	if (sessions.length === 0) return null;

	return (
		<div className="absolute inset-x-0 bottom-0 z-20 border-t border-border-strong bg-background px-3">
			{/* Full-row hit target: the control stretches edge-to-edge so empty
			    space beside the label toggles archive too. Height must match
			    archiveToggleOffsetClassName on the board. */}
			<button
				aria-expanded={expanded}
				aria-label={labels.archiveAria}
				className={cn(
					"group flex w-full min-w-0 items-center gap-2 py-0 text-muted-foreground transition-colors hover:text-foreground",
					archiveToggleHeightClassName,
					expanded ? "min-h-11" : "min-h-row-md",
				)}
				onClick={() => setExpanded((open) => !open)}
				type="button"
			>
				<ChevronIcon
					className={cn(
						"size-icon-2xs shrink-0 transition-transform duration-[140ms] ease-[cubic-bezier(0.25,0.46,0.45,0.94)]",
						prefersReducedMotion && "transition-none",
						expanded && "rotate-90",
					)}
					direction="right"
				/>
				<span className="text-2xs font-medium tracking-wide-sm">{labels.archive}</span>
				<span className="ml-1.5 font-mono text-micro text-passive">{sessions.length}</span>
			</button>
			{/* Keep the sheet mounted after first open; height tracks `expanded`. */}
			{cardsReady ? (
				<motion.div
					initial={prefersReducedMotion ? false : { height: 0 }}
					animate={{ height: expanded ? "auto" : 0 }}
					transition={
						prefersReducedMotion
							? { duration: 0 }
							: { duration: 0.14, ease: [0.25, 0.46, 0.45, 0.94] }
					}
					style={{ overflow: "hidden" }}
				>
					<div
						aria-hidden={!expanded}
						aria-label={expanded ? labels.archivedSessions : undefined}
						className={cn(
							"scrollbar-none grid max-h-[28vh] grid-cols-[repeat(auto-fill,minmax(17rem,1fr))] gap-2 overflow-y-auto pb-3",
							!expanded && "pointer-events-none",
						)}
						inert={!expanded ? true : undefined}
						role="list"
					>
						{sessions.map((session) => (
							<Fragment key={session.id}>{renderSessionCard(session)}</Fragment>
						))}
					</div>
				</motion.div>
			) : null}
		</div>
	);
}) as <TSession extends BoardSessionPresentation>(props: {
	labels: {
		archive: string;
		archiveAria: string;
		archivedSessions: string;
	};
	renderSessionCard: (session: TSession) => ReactNode;
	resetKey?: string;
	sessions: TSession[];
}) => ReactElement | null;

function sameLabel(a: string, b: string): boolean {
	const normalize = (value: string) =>
		value
			.toLowerCase()
			.replace(/^(feat|fix|chore|refactor|session)\//, "")
			.replace(/[^a-z0-9]+/g, "");
	return normalize(a) === normalize(b);
}
