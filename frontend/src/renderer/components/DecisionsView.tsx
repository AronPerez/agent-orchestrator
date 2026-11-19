import { useNavigate } from "@tanstack/react-router";
import { useCallback, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { useMinuteClock } from "@aoagents/product-ui";
import { ListChecks } from "@/components/app-icons";
import { useDecisionBoard } from "../hooks/useDecisionInbox";
import { apiErrorRequestId } from "../lib/api-client";
import { buildDecisionBoard, DECISION_LANES } from "../lib/decision-board";
import { DecisionCallPanel } from "./DecisionCallPanel";
import { DecisionsBoard } from "./DecisionsBoard";
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
export function DecisionsView({ callId }: DecisionsViewProps) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const board = useDecisionBoard();
	const now = useMinuteClock();
	const model = useMemo(() => buildDecisionBoard(board.data?.calls ?? []), [board.data]);
	const selectCall = useCallback((id: string) => {
		void navigate({ to: "/decisions", search: { call: id } });
	}, [navigate]);
	const closeCall = useCallback(() => {
		const closing = callId;
		void navigate({ to: "/decisions", search: {} });
		// Return focus to the card the panel was opened from, when it is still on the board.
		if (closing) {
			requestAnimationFrame(() => {
				document.querySelector<HTMLElement>(`[data-decision-card="${CSS.escape(closing)}"]`)?.focus();
			});
		}
	}, [callId, navigate]);

	let body;
	if (board.data && board.data.calls.length > 0) {
		body = (
			<>
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
		const requestId = apiErrorRequestId(board.error instanceof Error ? board.error.cause : undefined);
		body = (
			<div role="alert" className="mx-(--space-4_5) mt-4 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
				<span className="text-destructive">{board.error instanceof Error ? board.error.message : t("decisionInbox.loadFailed")}</span>
				{requestId ? <span className="font-mono text-caption text-muted-foreground">{t("decisions.requestId", { id: requestId })}</span> : null}
				<Button size="sm" variant="secondary" onClick={() => void board.refetch()}>{t("decisions.retry")}</Button>
			</div>
		);
	} else {
		body = <DecisionsBoardSkeleton label={t("decisions.loading")} />;
	}

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<PageTopbar lead={<PageTopbarTitle as="h1" icon={<ListChecks className="size-icon-md" aria-hidden="true" />}>{t("decisions.title")}</PageTopbarTitle>} />
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

/** Placeholder cells on the board's own grid, so loading does not reflow. */
function DecisionsBoardSkeleton({ label }: { label: string }) {
	return (
		<div role="status" aria-label={label} className="@container">
			<div className="grid grid-cols-[repeat(3,minmax(11rem,1fr))] @3xl:grid-cols-[minmax(7rem,10rem)_repeat(3,minmax(11rem,1fr))]">
				<div className="hidden border-b border-border @3xl:block" />
				{DECISION_LANES.map((lane) => (
					<div key={lane} className="border-b border-l border-border px-2.5 py-2"><Skeleton className="h-3 w-20" /></div>
				))}
				{[0, 1].map((row) => (
					<div key={row} className="contents">
						<div className="hidden border-b border-border px-3 py-2 @3xl:block"><Skeleton className="h-3 w-16" /></div>
						{DECISION_LANES.map((lane) => (
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
