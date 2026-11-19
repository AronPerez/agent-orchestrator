import { useNavigate } from "@tanstack/react-router";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useMinuteClock } from "@aoagents/product-ui";
import { ListChecks } from "@/components/app-icons";
import { useDecisionBoard, useDecisionSearch } from "../hooks/useDecisionInbox";
import { apiErrorRequestId } from "../lib/api-client";
import { buildDecisionBoard, DECISION_LANES } from "../lib/decision-board";
import { DecisionCallPanel } from "./DecisionCallPanel";
import { DecisionSearchResults } from "./DecisionSearchResults";
import { DecisionsBoard } from "./DecisionsBoard";
import { ToolbarSearch } from "./PageToolbar";
import { PageTopbar, PageTopbarTitle } from "./PageTopbar";
import { Button } from "./ui/button";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "./ui/resizable";
import { Skeleton } from "./ui/skeleton";

export type DecisionsViewProps = {
	/** The selected call from `?call=`. */
	callId?: string;
	/** Search words from `?q=`. */
	query?: string;
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

export function DecisionsView({ callId, query }: DecisionsViewProps) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const board = useDecisionBoard();
	const now = useMinuteClock();
	const model = useMemo(() => buildDecisionBoard(board.data?.calls ?? []), [board.data]);
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
			void navigate({ to: "/decisions", search: { ...(callId ? { call: callId } : {}), ...(next ? { q: next } : {}) }, replace: true });
		}, SEARCH_DEBOUNCE_MS);
		return () => clearTimeout(timer);
	}, [callId, draft, navigate, words]);
	const clearSearch = useCallback(() => {
		setDraft("");
		void navigate({ to: "/decisions", search: callId ? { call: callId } : {}, replace: true });
	}, [callId, navigate]);

	const selectCall = useCallback((id: string) => {
		void navigate({ to: "/decisions", search: { call: id, ...(words ? { q: words } : {}) } });
	}, [navigate, words]);
	const closeCall = useCallback(() => {
		const closing = callId;
		void navigate({ to: "/decisions", search: words ? { q: words } : {} });
		// Return focus to the card the panel was opened from, when it is still on the board.
		if (closing) {
			requestAnimationFrame(() => {
				document.querySelector<HTMLElement>(`[data-decision-card="${CSS.escape(closing)}"], [data-decision-result="${CSS.escape(closing)}"]`)?.focus();
			});
		}
	}, [callId, navigate, words]);

	let body;
	if (words) {
		if (search.data && search.data.length > 0) {
			body = (
				<div aria-busy={search.isPlaceholderData || undefined}>
					{search.isPlaceholderData ? <p role="status" className="px-(--space-4_5) pt-2 text-caption text-passive">{t("decisions.updating")}</p> : null}
					<DecisionSearchResults calls={search.data} selectedId={callId} onSelect={selectCall} />
				</div>
			);
		} else if (search.data) {
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
				<DecisionsBoard model={model} selectedId={callId} onSelect={selectCall} now={now} />
			</>
		);
	} else if (board.data) {
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
				if (event.key !== "Escape" || event.defaultPrevented || !callId) return;
				event.preventDefault();
				closeCall();
			}}
		>
			<p aria-live="polite" className="sr-only">
				{board.data ? t("decisions.navNeedsYou", { count: board.data.needsYouCount }) : ""}
			</p>
			<PageTopbar
				lead={<PageTopbarTitle as="h1" icon={<ListChecks className="size-icon-md" aria-hidden="true" />}>{t("decisions.title")}</PageTopbarTitle>}
				actions={<ToolbarSearch label={t("decisions.search")} query={draft} onQueryChange={setDraft} />}
			/>
			<ResizablePanelGroup className="min-h-0 flex-1">
				<ResizablePanel id="decisions-board" minSize="30%">
					<div className="flex h-full min-w-0 flex-col">
						<main className="min-h-0 flex-1 overflow-auto overscroll-contain">{body}</main>
						{/* Slice 4 replaces this with sync freshness; until then it names whose ledger this is. */}
						<footer className="shrink-0 border-t border-border px-(--space-4_5) py-1.5 text-caption text-muted-foreground">
							{t("decisions.footerLocal")}
						</footer>
					</div>
				</ResizablePanel>
				{callId ? (
					<>
						<ResizableHandle />
						{/* The shell's inspector position: beside the board, never a modal. */}
						<ResizablePanel id="decision-call" defaultSize="420px" minSize="360px" maxSize="55%">
							<DecisionCallPanel key={callId} callId={callId} onClose={closeCall} />
						</ResizablePanel>
					</>
				) : null}
			</ResizablePanelGroup>
		</div>
	);
}

// The skeleton draws the three lanes every board has.
const SKELETON_LANES = DECISION_LANES.filter((lane) => lane !== "decided");

/** Placeholder cells on the board's own grid, so loading does not reflow. */
function DecisionsBoardSkeleton({ label }: { label: string }) {
	return (
		<div role="status" aria-label={label} className="@container">
			<div className="grid grid-cols-[repeat(3,minmax(11rem,1fr))] @3xl:grid-cols-[minmax(7rem,10rem)_repeat(3,minmax(11rem,1fr))]">
				<div className="hidden border-b border-border @3xl:block" />
				{SKELETON_LANES.map((lane) => (
					<div key={lane} className="border-b border-l border-border px-2.5 py-2"><Skeleton className="h-3 w-20" /></div>
				))}
				{[0, 1].map((row) => (
					<div key={row} className="contents">
						<div className="hidden border-b border-border px-3 py-2 @3xl:block"><Skeleton className="h-3 w-16" /></div>
						{SKELETON_LANES.map((lane) => (
							<div key={lane} className="min-h-14 border-b border-l border-border p-1.5">
								{row === 0 ? <Skeleton className="h-11 w-full rounded-md" /> : null}
							</div>
						))}
					</div>
				))}
			</div>
		</div>
	);
}
