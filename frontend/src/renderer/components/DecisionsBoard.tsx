import { createContext, useContext, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ChevronRight, ShieldAlert, TriangleAlert } from "@/components/app-icons";
import type { DecisionFolds } from "../hooks/useDecisionFolds";
import {
	DECISION_LANES,
	initialDecisionFocusId,
	moveDecisionFocus,
	replacementDecisionFocusId,
	type DecisionBoardModel,
	type DecisionFocusDirection,
	type DecisionCardModel,
	type DecisionLane,
	type DecisionProjectBand,
	type DecisionStreamRow,
} from "../lib/decision-board";
import { decisionSecondaryText, decisionStatusText } from "../lib/decision-card-text";
import { cappedDoneCards, DECISION_DONE_SHOWN, decisionBandFoldKey, decisionRowFoldKey, visibleDecisionTopology } from "../lib/decision-folds";
import type { DecisionSyncStatus } from "../lib/decision-sync";
import { cn } from "../lib/utils";
import { DecisionStateDot, decisionStateLabel } from "./DecisionStateLabel";
import { Button } from "./ui/button";
import { Pressable } from "./ui/pressable";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";


/** Sync freshness for the cards of calls another machine must still deliver. */
const DecisionSyncContext = createContext<DecisionSyncStatus | undefined>(undefined);

export type DecisionsBoardProps = {
	model: DecisionBoardModel;
	/** The call open in the panel, from the route. */
	selectedId?: string;
	onSelect: (callId: string) => void;
	/** Clock for "Waiting 3h"; the view passes a minute-resolution value. */
	now: number;
	/** Sync freshness, for calls another machine must still deliver. */
	sync?: DecisionSyncStatus;
	/** Which projects and streams are folded; the page owns it so its toolbar can fold everything. */
	folds: DecisionFolds;
	/**
	 * The model holds one project chosen in the list beside the board. Its
	 * header is left out, since the list already names it, and with no header
	 * to fold it by, it is always open.
	 */
	soloBand?: boolean;
	/** The calls kept open as tabs; their cards carry a stronger edge. */
	keptIds?: ReadonlySet<string>;
	/** Keep a call open as a tab: a double-click on its card, or Shift and Enter. */
	onKeep?: (callId: string) => void;
};

// Narrow pane: the state columns alone, each project's and stream's name as a
// header row above them. From @3xl the names move into their own leading column.
// Literal class strings for three or four lanes (the Decided for you lane
// shows only while decisions stand), so Tailwind can see both.
export const DECISION_GRID_CLASS = {
	3: "grid min-w-[27rem] grid-cols-[repeat(3,minmax(9rem,1fr))] @3xl:min-w-[35rem] @3xl:grid-cols-[minmax(8rem,13rem)_repeat(3,minmax(9rem,1fr))]",
	4: "grid min-w-[36rem] grid-cols-[repeat(4,minmax(9rem,1fr))] @3xl:min-w-[44rem] @3xl:grid-cols-[minmax(8rem,13rem)_repeat(4,minmax(9rem,1fr))]",
} as const;

function laneLabel(lane: DecisionLane, t: TFunction) {
	return decisionStateLabel(lane, t);
}

/** "Needs you: 2, Done: 1" for a toggle's accessible name; the visible counts are decoration beside it. */
function countsText(counts: Record<DecisionLane, number>, t: TFunction): string {
	return DECISION_LANES.filter((lane) => counts[lane] > 0)
		.map((lane) => t("decisions.countFor", { state: decisionStateLabel(lane, t), count: counts[lane] }))
		.join(", ");
}

/**
 * The Decisions swimlanes: one continuous grid whose columns are the states,
 * whose bands are projects and whose rows are work streams. Projects and
 * streams both fold to their counts. Cards are the only individually bordered
 * objects; everything else shares hairlines.
 */
export function DecisionsBoard({ model, selectedId, onSelect, now, sync, folds: pageFolds, soloBand = false, keptIds, onKeep }: DecisionsBoardProps) {
	const { t } = useTranslation();
	const hintId = useId();
	const folds = useMemo(() => {
		if (!soloBand) return pageFolds;
		const bandKeys = new Set(model.bands.map((band) => decisionBandFoldKey(band.key)));
		return { ...pageFolds, folded: new Set([...pageFolds.folded].filter((key) => !bandKeys.has(key))) };
	}, [model.bands, pageFolds, soloBand]);

	// A row's Done cell shows its newest cards and the selected one; the rest open on request.
	const [expandedDone, setExpandedDone] = useState<ReadonlySet<string>>(() => new Set());
	const toggleDone = (key: string) => setExpandedDone((current) => {
		const next = new Set(current);
		if (next.has(key)) next.delete(key);
		else next.add(key);
		return next;
	});
	// Roving focus: one card is the board's tab stop; arrows move it.
	const gridRef = useRef<HTMLDivElement>(null);
	const visible = useMemo(() => visibleDecisionTopology(model, folds.folded, expandedDone, selectedId), [model, folds.folded, expandedDone, selectedId]);
	const visibleIds = useMemo(() => new Set(visible.flatMap((row) => DECISION_LANES.flatMap((lane) => row.lanes[lane]))), [visible]);
	const [focusId, setFocusId] = useState<string | undefined>(undefined);
	const tabStop = focusId && visibleIds.has(focusId) ? focusId : initialDecisionFocusId(visible, selectedId);
	const focusCard = (id: string) => {
		setFocusId(id);
		gridRef.current?.querySelector<HTMLElement>(`[data-decision-card="${CSS.escape(id)}"]`)?.focus();
	};
	// A route selection (a link, a search result) becomes the tab stop too.
	useEffect(() => {
		if (selectedId) setFocusId(selectedId);
	}, [selectedId]);
	// When the focused card leaves the board (its state changed, it was
	// removed, or its project or stream folded), keep focus on the board
	// instead of losing it to the page.
	// A card that changes lane is remounted in its new cell, which drops the
	// focus it had; cardHadFocus remembers that a card held it.
	const previousVisible = useRef(visible);
	const cardHadFocus = useRef(false);
	useLayoutEffect(() => {
		const before = previousVisible.current;
		previousVisible.current = visible;
		if (!focusId) return;
		const stillVisible = visibleIds.has(focusId);
		const target = stillVisible ? focusId : replacementDecisionFocusId(before, focusId, visible);
		if (!stillVisible) setFocusId(target);
		// Only when the focused card itself was lost: focus elsewhere (the
		// search field, the panel, a fold toggle) is never taken.
		const active = document.activeElement;
		if (!target || (active && active !== document.body)) return;
		if (stillVisible && !cardHadFocus.current) return;
		gridRef.current?.querySelector<HTMLElement>(`[data-decision-card="${CSS.escape(target)}"]`)?.focus();
	}, [focusId, visible, visibleIds]);
	const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		const id = (event.target as HTMLElement).getAttribute?.("data-decision-card");
		const direction: DecisionFocusDirection | undefined = event.key === "ArrowDown" ? "down" : event.key === "ArrowUp" ? "up" : event.key === "ArrowLeft" ? "left" : event.key === "ArrowRight" ? "right" : undefined;
		if (!id || !direction) return;
		event.preventDefault();
		focusCard(moveDecisionFocus(visible, id, direction));
	};

	return (
		<DecisionSyncContext.Provider value={sync}>
		<div className="@container min-w-0">
			<p id={hintId} className="sr-only">{t("decisions.keyboardHint")}{onKeep ? <>{" "}{t("decisions.tabs.keepHint")}</> : null}</p>
			<div
				ref={gridRef}
				className={DECISION_GRID_CLASS[model.lanes.length === 4 ? 4 : 3]}
				aria-label={t("decisions.boardLabel")}
				aria-describedby={hintId}
				role="group"
				onKeyDown={onKeyDown}
				onFocus={(event) => {
					const id = (event.target as HTMLElement).getAttribute?.("data-decision-card");
					cardHadFocus.current = Boolean(id);
					if (id) setFocusId(id);
				}}
				onBlur={(event) => {
					// A card removed from the page may blur on its way out; that
					// is not the person moving focus away.
					if (!event.relatedTarget && !(event.target as HTMLElement).isConnected) return;
					cardHadFocus.current = false;
				}}
			>
				<div aria-hidden="true" className="hidden border-b border-border @3xl:block" />
				<ul aria-label={t("decisions.statesLabel")} className="contents">
					{model.lanes.map((lane) => (
						<li
							key={lane}
							className="flex min-w-0 items-center gap-1.5 border-b border-l border-border px-2.5 py-2 text-caption font-medium text-foreground"
							title={lane === "done" ? t("decisions.doneWindow") : undefined}
						>
							<DecisionStateDot state={lane} />
							<span className="truncate">{laneLabel(lane, t)}</span>
							<span className="font-normal text-muted-foreground tabular-nums">{model.totals[lane]}</span>
							{lane === "done" ? <span className="sr-only">{t("decisions.doneWindow")}</span> : null}
						</li>
					))}
				</ul>
				{model.bands.map((band) => (
					<DecisionBand
						key={band.key}
						band={band}
						solo={soloBand}
						folds={folds}
						lanes={model.lanes}
						expandedDone={expandedDone}
						onToggleDone={toggleDone}
						selectedId={selectedId}
						tabStop={tabStop}
						onSelect={onSelect}
						keptIds={keptIds}
						onKeep={onKeep}
						now={now}
					/>
				))}
			</div>
		</div>
		</DecisionSyncContext.Provider>
	);
}

type RowShared = {
	lanes: readonly DecisionLane[];
	folds: DecisionFolds;
	expandedDone: ReadonlySet<string>;
	onToggleDone: (key: string) => void;
	selectedId?: string;
	tabStop?: string;
	onSelect: (callId: string) => void;
	keptIds?: ReadonlySet<string>;
	onKeep?: (callId: string) => void;
	now: number;
};

function FoldChevron({ open, className }: { open: boolean; className?: string }) {
	return (
		<ChevronRight
			aria-hidden="true"
			className={cn("size-3 shrink-0 text-muted-foreground transition-transform duration-(--duration-fast) motion-reduce:transition-none", open && "rotate-90", className)}
		/>
	);
}

/** A folded project's or stream's count in one state column. Decoration: the toggle's name already says the counts. */
function LaneCount({ lane, count, testId, className }: { lane: DecisionLane; count: number; testId: string; className: string }) {
	return (
		<div
			aria-hidden="true"
			data-testid={testId}
			className={cn("min-w-0 border-b border-l border-border px-2.5 text-caption tabular-nums text-muted-foreground", lane === "needs_you" && "font-medium text-status-needs-you", className)}
		>
			{count > 0 ? count : ""}
		</div>
	);
}

function DecisionBand({ band, solo, ...shared }: { band: DecisionProjectBand; solo: boolean } & RowShared) {
	const { t } = useTranslation();
	const name = band.name ?? t("decisions.noProject");
	const foldKey = decisionBandFoldKey(band.key);
	const open = !shared.folds.folded.has(foldKey);
	return (
		<section aria-label={name} className="contents">
			{solo ? null : (
				<>
				{/* Narrow: one full-width row with the counts at its end. From @3xl the counts sit in the state columns. */}
				<Pressable
					aria-expanded={open}
					onClick={() => shared.folds.toggle(foldKey)}
					className="col-span-full flex min-h-8 min-w-0 items-center gap-2 border-b border-border bg-muted/40 px-3 py-1.5 text-caption focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset @3xl:col-span-1"
				>
					<FoldChevron open={open} />
					<span className="min-w-0 truncate font-medium text-foreground">{name}</span>
					<span className="sr-only">{countsText(band.counts, t)}</span>
					<span aria-hidden="true" className="ml-auto flex shrink-0 items-center gap-2.5 text-muted-foreground tabular-nums @3xl:hidden">
						{DECISION_LANES.filter((lane) => band.counts[lane] > 0).map((lane) => (
							<span key={lane} className={cn("inline-flex items-center gap-1", lane === "needs_you" && "font-medium text-status-needs-you")}>
								<DecisionStateDot state={lane} />
								{band.counts[lane]}
							</span>
						))}
					</span>
				</Pressable>
				{shared.lanes.map((lane) => (
					<LaneCount key={lane} lane={lane} count={band.counts[lane]} testId="decision-band-count" className="hidden items-center bg-muted/40 @3xl:flex" />
				))}
				</>
			)}
			{open ? band.rows.map((row) => <DecisionRow key={row.key} bandKey={band.key} row={row} {...shared} />) : null}
		</section>
	);
}

function DecisionRow({ bandKey, row, lanes, folds, expandedDone, onToggleDone, selectedId, tabStop, onSelect, keptIds, onKeep, now }: { bandKey: string; row: DecisionStreamRow } & RowShared) {
	const { t } = useTranslation();
	const name = row.projectWide
		? t("decisions.projectWide")
		: [row.name ?? "", row.machine].filter(Boolean).join(" · ");
	const foldKey = decisionRowFoldKey(bandKey, row.key);
	const open = !folds.folded.has(foldKey);
	const allDone = expandedDone.has(foldKey);
	return (
		<div role="group" aria-label={name} className="contents">
			<Pressable
				aria-expanded={open}
				data-slot="decision-row-name"
				onClick={() => folds.toggle(foldKey)}
				className="col-span-full flex min-w-0 items-start gap-1.5 border-b border-border px-3 pt-2 pb-1 text-caption leading-4 text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset @3xl:col-span-1 @3xl:pb-2"
			>
				<FoldChevron open={open} className="mt-0.5" />
				<span className="line-clamp-3 min-w-0 break-words" title={name}>{name}</span>
				<span className="sr-only">{countsText(row.counts, t)}</span>
			</Pressable>
			{lanes.map((lane) => {
				if (!open) return <LaneCount key={lane} lane={lane} count={row.counts[lane]} testId="decision-fold-count" className="py-2 leading-4" />;
				const cards = row.lanes[lane];
				const capped = lane === "done" && cards.length > DECISION_DONE_SHOWN;
				const shown = capped && !allDone ? cappedDoneCards(cards, (card) => card.id, selectedId) : cards;
				return (
					<ul
						key={lane}
						aria-label={decisionStateLabel(lane, t)}
						className="flex min-h-10 min-w-0 flex-col gap-1 border-b border-l border-border p-1.5"
					>
						{shown.map((card) => (
							<li key={card.id} className="min-w-0">
								<DecisionCard card={card} selected={card.id === selectedId} kept={keptIds?.has(card.id) ?? false} tabStop={card.id === tabStop} onSelect={onSelect} onKeep={onKeep} now={now} />
							</li>
						))}
						{capped && (allDone || shown.length < cards.length) ? (
							<li>
								<Button aria-expanded={allDone} size="xs" variant="quiet" onClick={() => onToggleDone(foldKey)}>
									{allDone ? t("decisions.showFewer") : t("decisions.showMore", { count: cards.length - shown.length })}
								</Button>
							</li>
						) : null}
					</ul>
				);
			})}
		</div>
	);
}

/**
 * A card draws the call's title alone. Who raised it and how it stands are in
 * its accessible name and in a tooltip that opens on keyboard focus as well as
 * hover, so arrowing through the board shows them; the column already gives
 * the state. The tooltip passes Escape on, so the key still closes the open
 * call from a card, the panel or the search field in one press. What a person must not miss stays on the card: the two flags keep
 * their glyph, and an answer that has not reached a session says so under the
 * title.
 */
function DecisionCard({ card, selected, kept, tabStop, onSelect, onKeep, now }: { card: DecisionCardModel; selected: boolean; kept: boolean; tabStop: boolean; onSelect: (callId: string) => void; onKeep?: (callId: string) => void; now: number }) {
	const { t, i18n } = useTranslation();
	const sync = useContext(DecisionSyncContext);
	const status = decisionStatusText(card.status, now, t, { sync, locale: i18n.language });
	const secondary = decisionSecondaryText(card.secondary, t);
	const undelivered = card.status.kind === "pending" || card.status.kind === "no_session";
	const flags = [
		...(card.outsidePolicy ? [{ Icon: ShieldAlert, label: t("decisions.outsidePolicyShort") }] : []),
		...(card.changedAfterClose ? [{ Icon: TriangleAlert, label: t("decisions.changedAfterClose") }] : []),
	];
	return (
		<Tooltip>
		<TooltipTrigger asChild>
		<Pressable
			aria-current={selected ? "true" : undefined}
			data-decision-card={card.id}
			data-kept={kept || undefined}
			tabIndex={tabStop ? 0 : -1}
			onClick={() => onSelect(card.id)}
			onDoubleClick={onKeep ? () => onKeep(card.id) : undefined}
			onKeyDown={onKeep ? (event) => {
				// Shift and Enter keeps the call open as a tab; plain Enter is the click that views it.
				if (event.key !== "Enter" || !event.shiftKey) return;
				event.preventDefault();
				onKeep(card.id);
			} : undefined}
			className={cn(
				"flex w-full min-w-0 flex-wrap items-start gap-x-1 rounded-md border border-border bg-card px-2 py-1 transition-colors duration-(--duration-fast) hover:bg-interactive-hover focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none",
				kept && "border-border-strong",
				selected && "border-border-strong bg-interactive-active hover:bg-interactive-active",
			)}
		>
			<span className="line-clamp-2 min-w-0 flex-1 text-xs leading-4 text-foreground">{card.title}</span>
			{secondary ? <>{" "}<span className="sr-only">{secondary}</span></> : null}
			{status && !undelivered ? <>{" "}<span className="sr-only">{status}</span></> : null}
			{flags.map((flag) => (
				<span key={flag.label} className="mt-0.5 inline-flex shrink-0 items-center text-status-in-review">
					<flag.Icon aria-hidden="true" className="size-3" />
					{" "}<span className="sr-only">{flag.label}</span>
				</span>
			))}
			{status && undelivered ? <>{" "}<span className="line-clamp-2 w-full text-caption leading-4 text-muted-foreground">{status}</span></> : null}
		</Pressable>
		</TooltipTrigger>
		<TooltipContent side="bottom" align="start" hideArrow className="flex max-w-xs flex-col gap-0.5" passEscape>
			<span className="font-medium">{card.title}</span>
			{secondary ? <span className="text-muted-foreground">{secondary}</span> : null}
			{status ? <span className={card.lane === "needs_you" ? "text-status-needs-you" : "text-muted-foreground"}>{status}</span> : null}
			{flags.map((flag) => <span key={flag.label} className="text-status-in-review">{flag.label}</span>)}
		</TooltipContent>
		</Tooltip>
	);
}
