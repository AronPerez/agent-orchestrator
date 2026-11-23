import { useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import { useTranslation } from "react-i18next";
import { Search, TriangleAlert } from "@/components/app-icons";
import { filterDecisionArchive, type DecisionArchiveEntry, type DecisionArchiveScope } from "../lib/decision-archive";
import { decisionSecondaryText } from "../lib/decision-card-text";
import { formatTimeCompact } from "../lib/format-time";
import { gridNavigationTarget, isGridNavigationKey, measureGridColumns, measureVisibleRows, type GridNavigationKey } from "../lib/grid-navigation";
import { cn } from "../lib/utils";
import { FilterChip } from "./PageToolbar";
import { CloseButton } from "./ui/close-button";
import { CommandBar, CommandDialog, CommandFooter, CommandShortcut, commandInputClassName } from "./ui/command";
import { Pressable } from "./ui/pressable";

export type DecisionArchiveModalProps = {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	/** Every done decision on the board, newest first. */
	entries: readonly DecisionArchiveEntry[];
	/** The stream it was opened from; it starts narrowed to that stream. */
	scope?: DecisionArchiveScope;
	/** Open one decision; the modal closes first. */
	onOpen: (callId: string) => void;
	onCloseAutoFocus?: (event: Event) => void;
};

/**
 * The board's done decisions as one searchable grid of cards, newest first, in
 * the same surface and with the same keys as Archived sessions: the search
 * field keeps focus while the arrows move one selection through the grid as it
 * is laid out, and Enter opens it. A stream's Done cell opens it narrowed to
 * that stream; one chip says so and widens it to every stream.
 *
 * It holds only what the board holds, the last seven days of Done. Older and
 * closed calls are in the page's own search.
 */
export function DecisionArchiveModal({ open, onOpenChange, entries, scope, onOpen, onCloseAutoFocus }: DecisionArchiveModalProps) {
	const { t } = useTranslation();
	const baseId = useId();
	const gridId = `${baseId}-cards`;
	const cellId = (entry: DecisionArchiveEntry) => `${baseId}-card-${entry.card.id}`;
	const [query, setQuery] = useState("");
	const [narrowed, setNarrowed] = useState(true);
	// One selection, held by decision: another one finishing while the archive is
	// open is inserted ahead of it and must not hand the selection, and Enter, to
	// whichever card took its place. That goes for the card highlighted without
	// being chosen too: the first one, on opening and after a new search or scope.
	// The place is kept so the selection stays where it was once its decision is gone.
	const [activeState, setActiveState] = useState<{ id?: string; index: number }>({ index: 0 });

	const projectName = (entry: DecisionArchiveEntry) => entry.projectName ?? t("decisions.noProject");
	const streamName = (entry: DecisionArchiveEntry) => entry.projectWide
		? t("decisions.projectWide")
		: [entry.streamName ?? "", entry.streamMachine].filter(Boolean).join(" · ");
	const chosenText = (entry: DecisionArchiveEntry) => decisionSecondaryText(entry.card.secondary, t);
	const outcomeText = (entry: DecisionArchiveEntry) => (entry.card.status.kind === "outcome" ? entry.card.status.text : "");
	// The scope in words, from any entry of that stream. The last one seen is kept: when that
	// stream's done decisions all leave the board, the chip still names it and still widens it.
	const scopeFound = scope ? entries.find((entry) => entry.projectKey === scope.project && entry.streamKey === scope.stream) : undefined;
	const [scopeSeen, setScopeSeen] = useState(scopeFound);
	if (scopeFound && scopeFound !== scopeSeen) setScopeSeen(scopeFound);
	const scopeEntry = scopeFound ?? scopeSeen;
	// A stream with nothing to name it by is not narrowed to: there would be no chip to widen it.
	const activeScope = narrowed && scopeEntry ? scope : undefined;
	// Searched as read: the title, the pick, the outcome and where it belongs. The board holds a week of Done at most, so this is cheap per render.
	const results = filterDecisionArchive(entries, activeScope, query, (entry) => [entry.card.title, chosenText(entry), outcomeText(entry), projectName(entry), streamName(entry)].join(" "));
	const heldIndex = activeState.id === undefined ? -1 : results.findIndex((entry) => entry.card.id === activeState.id);
	const activeIndex = heldIndex >= 0 ? heldIndex : Math.min(activeState.index, results.length - 1);
	const active = activeIndex >= 0 ? results[activeIndex] : undefined;
	if (heldIndex >= 0) {
		// The held decision moved (a newer one came in ahead of it): remember where
		// it is now, so that is the place the selection keeps if it later goes.
		if (heldIndex !== activeState.index) setActiveState({ id: activeState.id, index: heldIndex });
	} else if (active) {
		// Nothing is held yet, or what was held is no longer among the results: the
		// highlighted card is the held one from this render on.
		setActiveState({ id: active.card.id, index: activeIndex });
	} else if (activeState.id !== undefined) {
		// Nothing to hold; the place is kept for when there is.
		setActiveState({ index: activeState.index });
	}
	const select = (index: number) => {
		const entry = results[index];
		if (entry) setActiveState({ id: entry.card.id, index });
	};

	const scrollerRef = useRef<HTMLDivElement>(null);
	const gridRef = useRef<HTMLDivElement>(null);
	const inputRef = useRef<HTMLInputElement>(null);
	// A key that moved the selection brings its card into view at once.
	const scrollToActive = useRef(false);
	useLayoutEffect(() => {
		if (!scrollToActive.current || !active) return;
		scrollToActive.current = false;
		document.getElementById(cellId(active))?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
	});

	const openEntry = (entry: DecisionArchiveEntry) => {
		onOpenChange(false);
		onOpen(entry.card.id);
	};
	const move = (key: GridNavigationKey) => {
		if (activeIndex < 0) return;
		const at = activeIndex;
		const columns = measureGridColumns(gridRef.current);
		// Only a page key needs the rows' laid-out heights.
		const pageRows = key === "PageUp" || key === "PageDown"
			? measureVisibleRows(scrollerRef.current, gridRef.current, at, columns, key === "PageUp" ? -1 : 1)
			: 1;
		const target = gridNavigationTarget(key, at, results.length, columns, pageRows);
		if (target < 0 || target === at) return;
		scrollToActive.current = true;
		select(target);
	};
	const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		if (event.nativeEvent.isComposing || event.defaultPrevented) return;
		// Modified arrows and Home/End, Shift included, stay the field's text editing.
		if (isGridNavigationKey(event.key) && !event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey) {
			event.preventDefault();
			move(event.key);
		} else if (event.key === "Enter" && active && (event.target as HTMLElement) === inputRef.current) {
			event.preventDefault();
			openEntry(active);
		}
	};
	// The pointer takes the selection only when it actually moves: a card that
	// scrolls under a still pointer does not.
	const onPointerMove = (event: PointerEvent<HTMLDivElement>) => {
		if (event.movementX === 0 && event.movementY === 0) return;
		const id = (event.target as HTMLElement).closest<HTMLElement>("[data-decision-archive]")?.dataset.decisionArchive;
		const index = results.findIndex((entry) => entry.card.id === id);
		if (index >= 0 && index !== activeIndex) select(index);
	};

	return (
		<CommandDialog
			plain
			open={open}
			onOpenChange={onOpenChange}
			title={t("decisions.archive.title")}
			description={t("decisions.archive.search")}
			// The sessions archive's size, so cards sit at least three to a row.
			// `!` because tailwind-merge does not know the palette's sizes.
			className="top-[6vh]! w-[min(1200px,calc(100vw-4rem))]!"
			contentProps={onCloseAutoFocus ? { onCloseAutoFocus } : undefined}
		>
			<div className="contents" onKeyDown={onKeyDown}>
				<CommandBar>
					<Search className="size-icon-base shrink-0 text-muted-foreground" aria-hidden="true" />
					<input
						aria-activedescendant={active ? cellId(active) : undefined}
						aria-autocomplete="list"
						aria-controls={gridId}
						aria-expanded={true}
						aria-haspopup="grid"
						aria-label={t("decisions.archive.search")}
						autoComplete="off"
						autoCorrect="off"
						autoFocus
						className={commandInputClassName}
						data-slot="command-input"
						onChange={(event) => {
							setQuery(event.target.value);
							// A new search starts at its first result.
							setActiveState({ index: 0 });
							scrollToActive.current = true;
						}}
						placeholder={t("decisions.archive.search")}
						ref={inputRef}
						role="combobox"
						spellCheck={false}
						type="text"
						value={query}
					/>
					<CloseButton aria-label={t("command.close")} className="-my-1 -mr-1.5" onClick={() => onOpenChange(false)} size="icon-sm" />
				</CommandBar>
				<div
					className="h-[calc(88vh-6rem)] max-h-[calc(88vh-6rem)] scrollbar-none scroll-py-1.5 overflow-y-auto overflow-x-hidden overscroll-contain px-3 pt-1 pb-3 outline-none"
					data-slot="command-list"
					ref={scrollerRef}
				>
					<div className="flex min-h-7 items-center gap-2 px-1 pb-2 text-caption text-muted-foreground">
						{activeScope && scopeEntry ? (
							<FilterChip
								label={projectName(scopeEntry)}
								value={streamName(scopeEntry)}
								removeLabel={t("decisions.archive.showAll", { scope: `${projectName(scopeEntry)}: ${streamName(scopeEntry)}` })}
								onRemove={() => {
									setNarrowed(false);
									setActiveState({ index: 0 });
									inputRef.current?.focus();
								}}
							/>
						) : null}
						<span>{t("decisions.doneWindow")}</span>
					</div>
					{results.length === 0 ? (
						<div className="px-[var(--size-command-pad-x)] py-8 text-center text-control text-muted-foreground" role="status">
							{t("decisions.archive.noMatches")}
						</div>
					) : null}
					<div
						aria-label={t("decisions.archive.title")}
						className="grid grid-cols-[repeat(auto-fill,minmax(min(17rem,calc((100%-1rem)/3)),1fr))] gap-2"
						id={gridId}
						onPointerMove={onPointerMove}
						ref={gridRef}
						role="grid"
					>
						{results.map((entry) => {
							const selected = entry === active;
							const chosen = chosenText(entry);
							const outcome = outcomeText(entry);
							return (
								// Each card is a row of one cell, as in the sessions archive, so a
								// column change never regroups cards; the arrows follow the visual columns.
								<div key={entry.card.id} className="min-w-0" role="row">
									<div aria-selected={selected} className="h-full" id={cellId(entry)} role="gridcell">
										<Pressable
											data-decision-archive={entry.card.id}
											tabIndex={-1}
											onClick={() => openEntry(entry)}
											className={cn(
												"flex h-full w-full min-w-0 flex-col gap-1 rounded-lg border border-border bg-card p-2.5",
												// One selection, switched at once with no fade.
												selected && "border-border-strong bg-interactive-hover",
											)}
										>
											<span className="flex min-w-0 items-start gap-1.5">
												<span className="line-clamp-2 min-w-0 flex-1 text-sm font-medium leading-5 text-foreground">{entry.card.title}</span>
												{entry.card.changedAfterClose ? (
													<span className="mt-1 inline-flex shrink-0 text-status-in-review">
														<TriangleAlert aria-hidden="true" className="size-3" />
														<span className="sr-only">{t("decisions.changedAfterClose")}</span>
													</span>
												) : null}
											</span>
											{chosen ? <span className="truncate text-caption text-muted-foreground">{chosen}</span> : null}
											{outcome ? <span className="line-clamp-3 text-caption leading-4 text-foreground">{outcome}</span> : null}
											<span className="mt-auto flex min-w-0 items-baseline justify-between gap-2 pt-1 text-xs text-passive">
												<span className="truncate">{[projectName(entry), streamName(entry)].join(" · ")}</span>
												{entry.card.call.outcomeAt ? <span className="shrink-0 tabular-nums">{t("decisions.finishedAt", { time: formatTimeCompact(entry.card.call.outcomeAt) })}</span> : null}
											</span>
										</Pressable>
									</div>
								</div>
							);
						})}
					</div>
				</div>
				<CommandFooter aria-hidden="true">
					<FooterHint keys={["←↑↓→"]} label={t("command.navigate")} />
					{active ? <FooterHint keys={["Enter"]} label={t("command.open")} /> : null}
					<FooterHint keys={["Esc"]} label={t("command.close")} />
				</CommandFooter>
			</div>
		</CommandDialog>
	);
}

function FooterHint({ keys, label }: { keys: readonly string[]; label: string }) {
	return (
		<span className="inline-flex shrink-0 items-center gap-1.5">
			<CommandShortcut keys={keys} variant="caps" />
			<span>{label}</span>
		</span>
	);
}
