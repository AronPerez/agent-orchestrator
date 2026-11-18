import { useTranslation } from "react-i18next";
import { useDecisionDetail } from "../hooks/useDecisionInbox";
import { parseDecisionLink } from "../lib/decision-links";
import type { DecisionCall } from "../lib/decision-inbox";
import { cn } from "../lib/utils";
import { HoverCardContent } from "./ui/hover-card";
import { Skeleton } from "./ui/skeleton";
import { DecisionStateDot, decisionStateLabel } from "./DecisionStateLabel";

const CARD_CLASS = "w-64 max-h-48 overflow-y-auto p-2";

/** Project and work stream, as the call recorded them. */
export function decisionPlace(call: DecisionCall): string {
	return [call.projectName || call.projectId, call.streamName || call.streamSessionId].filter(Boolean).join(" · ");
}

/** Machine and owning session, as the call recorded them. */
export function decisionOwner(call: DecisionCall): string {
	return [call.machineLabel || call.hostId, call.ownerSessionName || call.ownerSessionId].filter(Boolean).join(" · ");
}

/**
 * Hover preview for an `ao://decisions/<id>` link in chat. It reads this
 * computer's ledger and shows only identifying facts, never the call's context,
 * options or notes.
 */
export function DecisionLinkPreviewCard({ href, onOpen }: { href: string; onOpen?: () => void }) {
	const { t } = useTranslation();
	const target = parseDecisionLink(href);
	const detail = useDecisionDetail(target?.callId);
	const call = detail.data?.call;
	if (target && call) {
		return (
			<HoverCardContent
				collisionPadding={8}
				sideOffset={6}
				className={cn(CARD_CLASS, onOpen && "cursor-pointer transition-colors hover:bg-accent")}
				onClick={onOpen}
			>
				<div className="space-y-1" role="status" aria-label={`${call.title}, ${decisionStateLabel(call.state, t)}`}>
					<p className="line-clamp-2 text-xs font-semibold text-foreground">{call.title}</p>
					{decisionPlace(call) ? <p className="truncate text-caption text-muted-foreground">{decisionPlace(call)}</p> : null}
					<div className="flex min-w-0 items-center gap-1.5 text-xs">
						<DecisionStateDot state={call.state} />
						<span className="shrink-0 font-medium text-popover-foreground">{decisionStateLabel(call.state, t)}</span>
						<span className="min-w-0 truncate text-muted-foreground">{decisionOwner(call)}</span>
					</div>
				</div>
			</HoverCardContent>
		);
	}
	if (target && detail.isLoading) {
		return (
			<HoverCardContent collisionPadding={8} sideOffset={6} className={CARD_CLASS}>
				<div role="status" aria-label={t("decisions.loadingDecision")} className="space-y-1.5">
					<Skeleton className="h-3 w-40" />
					<Skeleton className="h-2.5 w-28" />
				</div>
			</HoverCardContent>
		);
	}
	if (target && detail.isError) {
		return (
			<HoverCardContent collisionPadding={8} sideOffset={6} className={CARD_CLASS}>
				<p role="alert" className="text-xs font-medium text-foreground">{t("decisions.loadFailedOne")}</p>
			</HoverCardContent>
		);
	}
	return (
		<HoverCardContent collisionPadding={8} sideOffset={6} className={CARD_CLASS}>
			<p role="status" className="text-xs font-medium text-foreground">{t("decisions.linkUnavailable")}</p>
		</HoverCardContent>
	);
}
