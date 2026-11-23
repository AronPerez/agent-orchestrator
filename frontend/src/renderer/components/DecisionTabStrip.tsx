import { useEffect, useRef, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { ListChecks } from "@/components/app-icons";
import { useDecisionDetail } from "../hooks/useDecisionInbox";
import type { DecisionCall } from "../lib/decision-inbox";
import { cn } from "../lib/utils";
import { DecisionStateDot } from "./DecisionStateLabel";
import { CloseButton } from "./ui/close-button";
import { Pressable } from "./ui/pressable";

export type DecisionTabStripProps = {
	/** The decisions kept open, in the order they were kept. */
	ids: readonly string[];
	/** The decision filling the page; undefined while the board shows. */
	activeId?: string;
	/** Undefined selects the board. */
	onSelect: (id: string | undefined) => void;
	onClose: (id: string) => void;
	/** What the board knows of a call, shown until its own record loads. */
	fallback: (id: string) => Pick<DecisionCall, "title" | "state"> | undefined;
};

const tabClass = "inline-flex h-6.5 min-w-0 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground hover:bg-interactive-hover hover:text-foreground focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring data-[active=true]:bg-interactive-active data-[active=true]:text-foreground";

/**
 * The Decisions page's tabs: the board first, then one tab per decision kept
 * open. Choosing a decision's tab fills the page with it; the board's tab
 * brings the board back. Arrow keys move along the strip, Enter chooses, and
 * Delete closes the decision in focus. The board's tab stays put while the
 * decisions' tabs scroll sideways once there are more than fit.
 */
export function DecisionTabStrip({ ids, activeId, onSelect, onClose, fallback }: DecisionTabStripProps) {
	const { t } = useTranslation();
	const stripRef = useRef<HTMLDivElement>(null);
	const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
		const tabs = [...(stripRef.current?.querySelectorAll<HTMLElement>("[role='tab']") ?? [])];
		const index = tabs.indexOf(event.target as HTMLElement);
		if (index < 0) return;
		const target = event.key === "ArrowRight" ? tabs[index + 1]
			: event.key === "ArrowLeft" ? tabs[index - 1]
				: event.key === "Home" ? tabs[0]
					: event.key === "End" ? tabs[tabs.length - 1]
						: undefined;
		if (target) {
			event.preventDefault();
			target.focus();
			return;
		}
		const id = tabs[index]?.dataset.decisionTab;
		if ((event.key === "Delete" || event.key === "Backspace") && id) {
			event.preventDefault();
			close(id);
		}
	};
	const close = (id: string) => {
		// When focus is on the tab that closes, or its button, it moves to the tab
		// before it rather than falling off the page. The page takes it to the
		// board when the strip goes with its last tab.
		const strip = stripRef.current;
		if (strip?.contains(document.activeElement)) {
			const tabs = [...strip.querySelectorAll<HTMLElement>("[role='tab']")];
			const index = tabs.findIndex((tab) => tab.dataset.decisionTab === id);
			(tabs[index - 1] ?? tabs[0])?.focus();
		}
		onClose(id);
	};
	return (
		<div
			ref={stripRef}
			role="tablist"
			aria-label={t("decisions.tabs.label")}
			className="flex h-9 shrink-0 items-center gap-0.5 border-b border-border px-2"
			onKeyDown={onKeyDown}
		>
			<Pressable
				role="tab"
				aria-selected={activeId === undefined}
				data-active={activeId === undefined}
				tabIndex={activeId === undefined ? 0 : -1}
				onClick={() => onSelect(undefined)}
				className={cn(tabClass, "shrink-0")}
			>
				<ListChecks aria-hidden="true" className="size-icon-sm" />
				{t("decisions.tabs.board")}
			</Pressable>
			<span aria-hidden="true" className="mx-1 h-4 w-px shrink-0 bg-border" />
			{/* Only the decisions scroll; a focused tab scrolls itself into sight. */}
			<div role="presentation" data-slot="decision-tab-scroll" className="scrollbar-none flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto">
				{ids.map((id) => (
					<DecisionTab key={id} id={id} active={id === activeId} onSelect={onSelect} onClose={close} fallback={fallback(id)} />
				))}
			</div>
		</div>
	);
}

function DecisionTab({ id, active, onSelect, onClose, fallback }: { id: string; active: boolean; onSelect: (id: string) => void; onClose: (id: string) => void; fallback?: Pick<DecisionCall, "title" | "state"> }) {
	const { t } = useTranslation();
	// The same record the decision's own view reads, so this costs no extra request.
	const detail = useDecisionDetail(id);
	const call = detail.data?.call ?? fallback;
	const title = call?.title ?? (detail.data === null ? t("decisions.unavailableTitle") : t("decisions.loadingDecision"));
	// The tab in view is never left scrolled out of the strip.
	const tabRef = useRef<HTMLButtonElement>(null);
	useEffect(() => {
		if (active) tabRef.current?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
	}, [active]);
	return (
		<span className="group/tab relative inline-flex min-w-28 flex-[0_1_14rem] items-center">
			<Pressable
				ref={tabRef}
				role="tab"
				aria-selected={active}
				data-active={active}
				data-decision-tab={id}
				tabIndex={active ? 0 : -1}
				title={title}
				onClick={() => onSelect(id)}
				className={cn(tabClass, "w-full pr-7")}
			>
				{call ? <DecisionStateDot state={call.state} /> : null}
				<span className="truncate">{title}</span>
			</Pressable>
			{/* Over the end of the title, like a session tab's close: always there for the tab in view, on hover or focus otherwise. */}
			<CloseButton
				aria-label={t("decisions.tabs.close", { title })}
				size="icon-xs"
				tabIndex={active ? 0 : -1}
				onClick={() => onClose(id)}
				className={cn("absolute right-1 top-1/2 -translate-y-1/2", !active && "opacity-0 focus-visible:opacity-100 group-hover/tab:opacity-100")}
			/>
		</span>
	);
}
