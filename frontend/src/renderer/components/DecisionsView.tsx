import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useTranslation } from "react-i18next";
import { useMinuteClock } from "@aoagents/product-ui";
import { ChevronsDownUp, ChevronsUpDown, FolderTree, ListChecks } from "@/components/app-icons";
import { useDecisionFolds } from "../hooks/useDecisionFolds";
import { useDecisionBoard, useDecisionSearch } from "../hooks/useDecisionInbox";
import { apiErrorRequestId } from "../lib/api-client";
import { buildDecisionBoard, DECISION_LANES } from "../lib/decision-board";
import { decisionRowFoldKey } from "../lib/decision-folds";
import { closeDecisionTab, DECISION_TABS_MAX, keepDecisionTab, readDecisionTabs, writeDecisionTabs } from "../lib/decision-tabs";
import { decisionScopeTree, readDecisionListHidden, resolveDecisionScope, scopeDecisionBoard, writeDecisionListHidden, type DecisionScope } from "../lib/decision-scope";
import { ConfirmDialog } from "./ConfirmDialog";
import { DecisionCallPanel } from "./DecisionCallPanel";
import { DecisionDraftScope } from "./DecisionDraftScope";
import { DecisionPanelSlot, useDecisionPanelHosts } from "./DecisionPanelHosts";
import { DecisionTabStrip } from "./DecisionTabStrip";
import { DecisionScopeList } from "./DecisionScopeList";
import { DecisionSearchResults } from "./DecisionSearchResults";
import { DecisionSyncFooter } from "./DecisionSyncFooter";
import { decisionSyncKey, fetchDecisionSyncStatus } from "../lib/decision-sync";
import { DECISION_GRID_CLASS, DecisionsBoard } from "./DecisionsBoard";
import { ToolbarSearch } from "./PageToolbar";
import { PageTopbar, PageTopbarTitle } from "./PageTopbar";
import { Button } from "./ui/button";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "./ui/resizable";
import { Skeleton } from "./ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

export type DecisionsViewProps = {
	/** The selected call from `?call=`. */
	callId?: string;
	/** Search words from `?q=`. */
	query?: string;
	/** The project chosen in the list beside the board, from `?project=`. */
	project?: string;
	/** The stream of that project chosen in the list, from `?stream=`. */
	stream?: string;
	/** The kept-open decision filling the page, from `?tab=`. */
	tab?: string;
};

/** The Decisions page: this computer's ledger as a live swimlane board. */
const SEARCH_DEBOUNCE_MS = 250;

/** One failed read, with the daemon's request id and a retry in place. */
function LoadError({ error, fallback, onRetry, className }: { error: unknown; fallback: string; onRetry: () => void; className?: string }) {
	const { t } = useTranslation();
	const requestId = apiErrorRequestId(error instanceof Error ? error.cause : undefined);
	return (
		<div role="alert" className={className ?? "mx-(--space-4_5) mt-4 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm"}>
			<span className="text-destructive">{error instanceof Error ? error.message : fallback}</span>
			{requestId ? <span className="font-mono text-caption text-muted-foreground">{t("decisions.requestId", { id: requestId })}</span> : null}
			<Button size="sm" variant="secondary" onClick={onRetry}>{t("decisions.retry")}</Button>
		</div>
	);
}

function TopbarIconButton({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button type="button" size="icon-sm" variant="quiet" aria-label={label} onClick={onClick}>{children}</Button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{label}</TooltipContent>
		</Tooltip>
	);
}

export function DecisionsView({ callId, query, project, stream, tab }: DecisionsViewProps) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const board = useDecisionBoard();
	const now = useMinuteClock();
	const model = useMemo(() => buildDecisionBoard(board.data?.calls ?? [], board.data?.servingHostId), [board.data]);
	const folds = useDecisionFolds(model, callId);
	// The list beside the board narrows it. The URL keeps what was chosen; the
	// board honours as much of it as is still there.
	const scope = useMemo(() => resolveDecisionScope(model, { project, stream }), [model, project, stream]);
	const scopedModel = useMemo(() => scopeDecisionBoard(model, scope), [model, scope]);
	const scopeTree = useMemo(() => decisionScopeTree(model), [model]);
	const [listHidden, setListHidden] = useState(readDecisionListHidden);
	const toggleList = () => setListHidden((hidden) => {
		writeDecisionListHidden(!hidden);
		return !hidden;
	});
	// Every navigation on this page carries the scope along.
	const scopeSearch = useMemo(() => ({ ...(project ? { project } : {}), ...(project && stream ? { stream } : {}) }), [project, stream]);

	// Decisions kept open as tabs. The list is saved on this computer; the URL
	// says only which one fills the page, and names nothing that is not kept.
	const [kept, setKept] = useState(readDecisionTabs);
	const loadedKept = useRef(kept);
	useEffect(() => {
		if (kept !== loadedKept.current) writeDecisionTabs(kept);
	}, [kept]);
	const keptIds = useMemo(() => new Set(kept), [kept]);
	const activeTab = tab !== undefined && keptIds.has(tab) ? tab : undefined;
	const keepOpen = useCallback((id: string) => setKept((current) => keepDecisionTab(current, id)), []);
	// One live view per open decision: every kept one, and the one beside the board.
	const panelIds = useMemo(() => (callId && !keptIds.has(callId) ? [...kept, callId] : kept), [callId, kept, keptIds]);
	const hosts = useDecisionPanelHosts(panelIds);
	// Which open decisions hold text that has not been sent.
	const [drafts, setDrafts] = useState<ReadonlySet<string>>(() => new Set());
	const markDraft = useCallback((id: string, dirty: boolean) => setDrafts((current) => {
		if (current.has(id) === dirty) return current;
		const next = new Set(current);
		if (dirty) next.add(id);
		else next.delete(id);
		return next;
	}), []);
	const [closing, setClosing] = useState<string | undefined>(undefined);
	const sync = useQuery({ queryKey: decisionSyncKey(), queryFn: ({ signal }) => fetchDecisionSyncStatus(signal) });
	const words = query?.trim() ?? "";
	const search = useDecisionSearch(words);
	// The URL owns the search: the field shows what the person is typing and
	// writes it back after a pause, replacing the entry so history holds one
	// step per search, not one per key.
	const [draft, setDraft] = useState(words);
	useEffect(() => {
		setDraft((current) => (current.trim() === words ? current : words));
	}, [words]);
	useEffect(() => {
		const next = draft.trim();
		if (next === words) return;
		const timer = setTimeout(() => {
			// Search results show in place of the board, so a search started from a decision's tab goes back to it.
			void navigate({ to: "/decisions", search: { ...(callId ? { call: callId } : {}), ...(next ? { q: next } : activeTab ? { tab: activeTab } : {}), ...scopeSearch }, replace: true });
		}, SEARCH_DEBOUNCE_MS);
		return () => clearTimeout(timer);
	}, [activeTab, callId, draft, navigate, scopeSearch, words]);
	const clearSearch = useCallback(() => {
		setDraft("");
		void navigate({ to: "/decisions", search: { ...(callId ? { call: callId } : {}), ...scopeSearch }, replace: true });
	}, [callId, navigate, scopeSearch]);

	const selectCall = useCallback((id: string) => {
		void navigate({ to: "/decisions", search: { call: id, ...(words ? { q: words } : {}), ...scopeSearch } });
	}, [navigate, scopeSearch, words]);
	const chooseScope = useCallback((next: DecisionScope) => {
		// Choosing a stream shows its cards, even if its row was folded.
		if (next.project !== undefined && next.stream !== undefined) folds.open(decisionRowFoldKey(next.project, next.stream));
		void navigate({
			to: "/decisions",
			search: { ...(callId ? { call: callId } : {}), ...(next.project !== undefined ? { project: next.project } : {}), ...(next.stream !== undefined ? { stream: next.stream } : {}) },
		});
	}, [callId, folds, navigate]);
	const closeCall = useCallback(() => {
		const closing = callId;
		void navigate({ to: "/decisions", search: { ...(words ? { q: words } : {}), ...scopeSearch } });
		// Return focus to the card the panel was opened from, when it is still on the board.
		if (closing) {
			requestAnimationFrame(() => {
				document.querySelector<HTMLElement>(`[data-decision-card="${CSS.escape(closing)}"], [data-decision-result="${CSS.escape(closing)}"]`)?.focus();
			});
		}
	}, [callId, navigate, scopeSearch, words]);

	const selectTab = useCallback((id: string | undefined) => {
		void navigate({ to: "/decisions", search: { ...(callId ? { call: callId } : {}), ...(words ? { q: words } : {}), ...scopeSearch, ...(id ? { tab: id } : {}) } });
	}, [callId, navigate, scopeSearch, words]);
	const closeTab = useCallback((id: string) => {
		setClosing(undefined);
		setKept((current) => closeDecisionTab(current, id));
		if (id === activeTab) selectTab(undefined);
	}, [activeTab, selectTab]);
	// A tab whose decision is also beside the board closes without loss: that view, and its text, stay.
	const requestCloseTab = useCallback((id: string) => {
		if (drafts.has(id) && id !== callId) setClosing(id);
		else closeTab(id);
	}, [callId, closeTab, drafts]);

	const boardShown = !words && !activeTab && Boolean(board.data && board.data.calls.length > 0);
	let body;
	if (words) {
		if (search.data && search.data.length > 0) {
			body = (
				<div aria-busy={search.isPlaceholderData || undefined}>
					{search.isError ? (
						// The last results stay; say that they could not be refreshed.
						<LoadError error={search.error} fallback={t("decisionInbox.loadFailed")} onRetry={() => void search.refetch()} className="mx-(--space-4_5) my-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm" />
					) : null}
					{search.isPlaceholderData ? <p role="status" className="px-(--space-4_5) pt-2 text-caption text-passive">{t("decisions.updating")}</p> : null}
					<DecisionSearchResults calls={search.data} selectedId={callId} onSelect={selectCall} />
				</div>
			);
		} else if (search.data && !search.isError) {
			body = (
				<div className="flex flex-col items-center gap-3 px-6 py-16 text-center">
					<p className="text-sm text-muted-foreground">{t("decisions.noMatches", { query: words })}</p>
					<Button size="sm" variant="secondary" onClick={clearSearch}>{t("decisions.clearSearch")}</Button>
				</div>
			);
		} else if (search.isError) {
			body = <LoadError error={search.error} fallback={t("decisionInbox.loadFailed")} onRetry={() => void search.refetch()} />;
		} else {
			body = <p role="status" aria-label={t("decisions.loading")} className="px-(--space-4_5) py-4 text-sm text-muted-foreground">{t("decisions.loading")}</p>;
		}
	} else if (board.data && board.data.calls.length > 0) {
		body = (
			<>
				{board.isError ? (
					// The last board stays; say that it could not be refreshed.
					<LoadError error={board.error} fallback={t("decisionInbox.loadFailed")} onRetry={() => void board.refetch()} className="mx-(--space-4_5) my-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm" />
				) : null}
				{board.data.needsYouCount === 0 ? (
					<p role="status" aria-label={t("decisions.nothingNeedsYou")} className="px-(--space-4_5) pt-3 pb-2 text-caption text-muted-foreground">
						{t("decisions.nothingNeedsYou")}
					</p>
				) : null}
				<DecisionsBoard model={scopedModel} soloBand={scope.project !== undefined} keptIds={keptIds} onKeep={keepOpen} selectedId={callId} onSelect={selectCall} now={now} sync={Array.isArray(sync.data?.peers) ? sync.data : undefined} folds={folds} />
			</>
		);
	} else if (board.data && !board.isError) {
		body = (
			<div className="flex flex-col items-center px-6 py-16 text-center">
				<h2 className="text-heading-sm font-medium text-foreground">{t("decisions.emptyTitle")}</h2>
				<p className="mt-1.5 text-sm text-muted-foreground">{t("decisions.emptyHint")}</p>
			</div>
		);
	} else if (board.isError) {
		body = <LoadError error={board.error} fallback={t("decisionInbox.loadFailed")} onRetry={() => void board.refetch()} />;
	} else {
		body = <DecisionsBoardSkeleton label={t("decisions.loading")} />;
	}

	return (
		<div
			className="flex min-h-0 flex-1 flex-col bg-background"
			onKeyDown={(event) => {
				// Escape closes the open call from anywhere on the page, a focused card included.
				if (event.key !== "Escape" || event.defaultPrevented || !callId || activeTab) return;
				event.preventDefault();
				closeCall();
			}}
		>
			<p aria-live="polite" className="sr-only">
				{board.data ? t("decisions.navNeedsYou", { count: board.data.needsYouCount }) : ""}
			</p>
			<PageTopbar
				lead={<PageTopbarTitle as="h1" icon={<ListChecks className="size-icon-md" aria-hidden="true" />}>{t("decisions.title")}</PageTopbarTitle>}
				actions={(
					<>
						{/* The list and folding apply to the board, so their controls leave with it during a search. */}
						{boardShown ? (
							<>
								<TopbarIconButton label={t(listHidden ? "decisions.scope.showList" : "decisions.scope.hideList")} onClick={toggleList}><FolderTree aria-hidden="true" className="size-icon-sm" /></TopbarIconButton>
								<TopbarIconButton label={t("decisions.expandAll")} onClick={() => folds.setAll(false)}><ChevronsUpDown aria-hidden="true" className="size-icon-sm" /></TopbarIconButton>
								<TopbarIconButton label={t("decisions.collapseAll")} onClick={() => folds.setAll(true)}><ChevronsDownUp aria-hidden="true" className="size-icon-sm" /></TopbarIconButton>
							</>
						) : null}
						<ToolbarSearch label={t("decisions.search")} query={draft} onQueryChange={setDraft} />
					</>
				)}
			/>
			{kept.length > 0 ? (
				<DecisionTabStrip
					ids={kept}
					activeId={activeTab}
					onSelect={selectTab}
					onClose={requestCloseTab}
					fallback={(id) => model.cardById.get(id)?.card.call}
				/>
			) : null}
			{/* A decision's tab fills the page. The board stays mounted underneath, so it comes back as it was left. */}
			{activeTab ? <DecisionPanelSlot host={hosts.get(activeTab)} className="min-h-0 min-w-0 flex-1" /> : null}
			<div hidden={Boolean(activeTab)} className="flex min-h-0 min-w-0 flex-1">
				<ResizablePanelGroup className="min-h-0 flex-1">
					<ResizablePanel id="decisions-board" minSize="30%">
						<div className="@container/decisions flex h-full min-w-0">
							{/* Beside the board while there is room for both; a narrow pane keeps the board. */}
							{boardShown && !listHidden ? <DecisionScopeList tree={scopeTree} scope={scope} onChoose={chooseScope} className="hidden @2xl/decisions:flex" /> : null}
							<div className="flex h-full min-w-0 flex-1 flex-col">
								<main className="min-h-0 flex-1 overflow-auto overscroll-contain">{body}</main>
								{/* Whose ledger this is, and how fresh each linked machine is. */}
								<footer className="shrink-0 border-t border-border px-(--space-4_5) py-1.5 text-caption text-muted-foreground">
									<DecisionSyncFooter now={now} />
								</footer>
							</div>
						</div>
					</ResizablePanel>
					{callId ? (
						<>
							<ResizableHandle />
							{/* The shell's inspector position: beside the board, never a modal. */}
							<ResizablePanel id="decision-call" defaultSize="420px" minSize="360px" maxSize="55%">
								<DecisionPanelSlot host={activeTab ? undefined : hosts.get(callId)} className="h-full min-w-0" />
							</ResizablePanel>
						</>
					) : null}
				</ResizablePanelGroup>
			</div>
			{panelIds.map((id) => {
				const host = hosts.get(id);
				if (!host) return null;
				const filling = id === activeTab;
				return createPortal(
					<DecisionDraftScope onDraftChange={(dirty) => markDraft(id, dirty)}>
						<DecisionCallPanel
							callId={id}
							layout={filling ? "page" : "panel"}
							onClose={filling ? undefined : closeCall}
							headerActions={keptIds.has(id)
								? <span className="px-1.5 text-caption text-passive">{t("decisions.tabs.kept")}</span>
								: kept.length >= DECISION_TABS_MAX
									? <span className="px-1.5 text-caption text-passive">{t("decisions.tabs.full")}</span>
									: <Button size="xs" variant="quiet" onClick={() => keepOpen(id)}>{t("decisions.tabs.keep")}</Button>}
						/>
					</DecisionDraftScope>,
					host,
					id,
				);
			})}
			<ConfirmDialog
				open={closing !== undefined}
				title={t("decisions.tabs.discardTitle")}
				description={t("decisions.tabs.discardBody")}
				confirmLabel={t("decisions.tabs.discardConfirm")}
				destructive
				initialFocus="cancel"
				onConfirm={() => { if (closing) closeTab(closing); }}
				onOpenChange={(open) => { if (!open) setClosing(undefined); }}
			/>
		</div>
	);
}

// The skeleton draws the three lanes every board has.
const SKELETON_LANES = DECISION_LANES.filter((lane) => lane !== "decided");

/** Placeholder cells on the board's own grid, so loading does not reflow. */
function DecisionsBoardSkeleton({ label }: { label: string }) {
	return (
		<div role="status" aria-label={label} className="@container">
			<div className={DECISION_GRID_CLASS[3]}>
				<div className="hidden border-b border-border @3xl:block" />
				{SKELETON_LANES.map((lane) => (
					<div key={lane} className="border-b border-l border-border px-2.5 py-2"><Skeleton className="h-3 w-20" /></div>
				))}
				{[0, 1].map((row) => (
					<div key={row} className="contents">
						<div className="hidden border-b border-border px-3 py-2 @3xl:block"><Skeleton className="h-3 w-16" /></div>
						{SKELETON_LANES.map((lane) => (
							<div key={lane} className="min-h-10 border-b border-l border-border p-1.5">
								{row === 0 ? <Skeleton className="h-6 w-full rounded-md" /> : null}
							</div>
						))}
					</div>
				))}
			</div>
		</div>
	);
}
