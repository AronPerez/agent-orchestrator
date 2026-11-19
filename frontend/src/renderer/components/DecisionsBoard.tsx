import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { ChevronRight, TriangleAlert } from "@/components/app-icons";
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
import { cn } from "../lib/utils";
import { DecisionStateDot, decisionStateLabel } from "./DecisionStateLabel";
import { Pressable } from "./ui/pressable";

export type DecisionsBoardProps = {
	model: DecisionBoardModel;
	/** The call open in the panel, from the route. */
	selectedId?: string;
	onSelect: (callId: string) => void;
	/** Clock for "Waiting 3h"; the view passes a minute-resolution value. */
	now: number;
};

// Narrow pane: three lane columns, each stream's name as a header row above
// them. From @3xl the name moves into its own leading column.
const GRID_CLASS =
	"grid min-w-[33rem] grid-cols-[repeat(3,minmax(11rem,1fr))] @3xl:min-w-[45rem] @3xl:grid-cols-[minmax(7rem,10rem)_repeat(3,minmax(11rem,1fr))]";

function laneLabel(lane: DecisionLane, t: TFunction) {
	return decisionStateLabel(lane, t);
}

/**
 * The Decisions swimlanes: one continuous grid whose columns are the three
 * states, whose bands are projects and whose rows are work streams. Cards are
 * the only individually bordered objects; everything else shares hairlines.
 */
export function DecisionsBoard({ model, selectedId, onSelect, now }: DecisionsBoardProps) {
	const { t } = useTranslation();
	const hintId = useId();
	const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(() => new Set());
	const selectedBand = selectedId ? model.cardById.get(selectedId)?.bandKey : undefined;
	// Opening a call always shows it, even inside a band the person folded away.
	useEffect(() => {
		if (!selectedBand) return;
		setCollapsed((current) => {
			if (!current.has(selectedBand)) return current;
			const next = new Set(current);
			next.delete(selectedBand);
			return next;
		});
	}, [selectedBand]);
	const toggleBand = (key: string) => setCollapsed((current) => {
		const next = new Set(current);
		if (next.has(key)) next.delete(key);
		else next.add(key);
		return next;
	});

	// Roving focus: one card is the board's tab stop; arrows move it.
	const gridRef = useRef<HTMLDivElement>(null);
	const visible = useMemo(() => model.topology.filter((row) => !collapsed.has(row.bandKey)), [model.topology, collapsed]);
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
	// removed, or its band folded), keep focus on the board instead of losing
	// it to the page.
	const previousVisible = useRef(visible);
	useLayoutEffect(() => {
		const before = previousVisible.current;
		previousVisible.current = visible;
		if (!focusId || visibleIds.has(focusId)) return;
		const replacement = replacementDecisionFocusId(before, focusId, visible);
		setFocusId(replacement);
		// Only when the focused card itself vanished: focus elsewhere (the
		// search field, the panel) is never taken.
		const active = document.activeElement;
		if (replacement && (!active || active === document.body)) {
			gridRef.current?.querySelector<HTMLElement>(`[data-decision-card="${CSS.escape(replacement)}"]`)?.focus();
		}
	}, [focusId, visible, visibleIds]);
	const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		const id = (event.target as HTMLElement).getAttribute?.("data-decision-card");
		const direction: DecisionFocusDirection | undefined = event.key === "ArrowDown" ? "down" : event.key === "ArrowUp" ? "up" : event.key === "ArrowLeft" ? "left" : event.key === "ArrowRight" ? "right" : undefined;
		if (!id || !direction) return;
		event.preventDefault();
		focusCard(moveDecisionFocus(visible, id, direction));
	};

	return (
		<div className="@container min-w-0">
			<p id={hintId} className="sr-only">{t("decisions.keyboardHint")}</p>
			<div
				ref={gridRef}
				className={GRID_CLASS}
				aria-label={t("decisions.boardLabel")}
				aria-describedby={hintId}
				role="group"
				onKeyDown={onKeyDown}
				onFocus={(event) => {
					const id = (event.target as HTMLElement).getAttribute?.("data-decision-card");
					if (id) setFocusId(id);
				}}
			>
				<div aria-hidden="true" className="hidden border-b border-border @3xl:block" />
				<ul aria-label={t("decisions.statesLabel")} className="contents">
					{DECISION_LANES.map((lane) => (
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
						collapsed={collapsed.has(band.key)}
						onToggle={() => toggleBand(band.key)}
						selectedId={selectedId}
						tabStop={tabStop}
						onSelect={onSelect}
						now={now}
					/>
				))}
			</div>
		</div>
	);
}

function DecisionBand({
	band,
	collapsed,
	onToggle,
	selectedId,
	tabStop,
	onSelect,
	now,
}: {
	band: DecisionProjectBand;
	collapsed: boolean;
	onToggle: () => void;
	selectedId?: string;
	tabStop?: string;
	onSelect: (callId: string) => void;
	now: number;
}) {
	const { t } = useTranslation();
	const name = band.name ?? t("decisions.noProject");
	return (
		<section aria-label={name} className="contents">
			<div className="col-span-full flex min-w-0 items-center border-b border-border bg-muted/40">
				<Pressable
					aria-expanded={!collapsed}
					onClick={onToggle}
					className="flex min-h-8 min-w-0 flex-1 items-center gap-2 px-3 py-1.5 text-caption focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
				>
					<ChevronRight
						aria-hidden="true"
						className={cn("size-3 shrink-0 text-muted-foreground transition-transform duration-(--duration-fast) motion-reduce:transition-none", !collapsed && "rotate-90")}
					/>
					<span className="min-w-0 truncate font-medium text-foreground">{name}</span>
					<span className="ml-auto flex shrink-0 items-center gap-2.5 text-muted-foreground tabular-nums">
						{DECISION_LANES.filter((lane) => band.counts[lane] > 0).map((lane) => (
							<span key={lane} className={cn("inline-flex items-center gap-1", lane === "needs_you" && "font-medium text-status-needs-you")}>
								<DecisionStateDot state={lane} />
								<span aria-hidden="true">{band.counts[lane]}</span>
								<span className="sr-only">{t("decisions.countFor", { state: decisionStateLabel(lane, t), count: band.counts[lane] })}</span>
							</span>
						))}
					</span>
				</Pressable>
			</div>
			{collapsed ? null : band.rows.map((row) => (
				<DecisionRow key={row.key} row={row} selectedId={selectedId} tabStop={tabStop} onSelect={onSelect} now={now} />
			))}
		</section>
	);
}

function DecisionRow({ row, selectedId, tabStop, onSelect, now }: { row: DecisionStreamRow; selectedId?: string; tabStop?: string; onSelect: (callId: string) => void; now: number }) {
	const { t } = useTranslation();
	const name = row.projectWide
		? t("decisions.projectWide")
		: [row.name ?? "", row.machine].filter(Boolean).join(" · ");
	return (
		<div role="group" aria-label={name} className="contents">
			<div
				data-slot="decision-row-name"
				className={cn(
					"col-span-full min-w-0 border-b border-border px-3 pt-2 pb-1 text-caption leading-4 text-muted-foreground @3xl:col-span-1 @3xl:pb-2",
					// The project's own row has no name; it only needs a header cell beside the lanes.
					row.projectWide && "hidden @3xl:block",
				)}
			>
				{row.projectWide ? <span className="sr-only">{name}</span> : <span className="line-clamp-3 break-words" title={name}>{name}</span>}
			</div>
			{DECISION_LANES.map((lane) => (
				<ul
					key={lane}
					aria-label={decisionStateLabel(lane, t)}
					className="flex min-h-14 min-w-0 flex-col gap-1.5 border-b border-l border-border p-1.5"
				>
					{row.lanes[lane].map((card) => (
						<li key={card.id} className="min-w-0">
							<DecisionCard card={card} selected={card.id === selectedId} tabStop={card.id === tabStop} onSelect={onSelect} now={now} />
						</li>
					))}
				</ul>
			))}
		</div>
	);
}

function DecisionCard({ card, selected, tabStop, onSelect, now }: { card: DecisionCardModel; selected: boolean; tabStop: boolean; onSelect: (callId: string) => void; now: number }) {
	const { t } = useTranslation();
	const status = decisionStatusText(card.status, now, t);
	const secondary = decisionSecondaryText(card.secondary, t);
	return (
		<Pressable
			aria-current={selected ? "true" : undefined}
			data-decision-card={card.id}
			tabIndex={tabStop ? 0 : -1}
			onClick={() => onSelect(card.id)}
			className={cn(
				"block w-full min-w-0 rounded-md border border-border bg-card px-2 py-1.5 transition-colors duration-(--duration-fast) hover:bg-interactive-hover focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none",
				selected && "border-border-strong bg-interactive-active hover:bg-interactive-active",
			)}
		>
			<span className="line-clamp-2 block text-xs font-medium leading-4 text-foreground" title={card.title}>{card.title}</span>
			{secondary ? <span className="mt-0.5 block truncate text-caption text-muted-foreground" title={secondary}>{secondary}</span> : null}
			<span className="mt-0.5 flex min-w-0 items-start gap-1 text-caption">
				{status ? (
					<span
						className={cn("line-clamp-2 min-w-0", card.lane === "needs_you" ? "font-medium text-status-needs-you" : "text-muted-foreground")}
						title={card.status.kind === "outcome" ? status : undefined}
					>
						{status}
					</span>
				) : null}
				{card.changedAfterClose ? (
					<span className="ml-auto inline-flex shrink-0 items-center text-status-in-review" title={t("decisions.changedAfterClose")}>
						<TriangleAlert aria-hidden="true" className="size-3" />
						<span className="sr-only">{t("decisions.changedAfterClose")}</span>
					</span>
				) : null}
			</span>
		</Pressable>
	);
}
