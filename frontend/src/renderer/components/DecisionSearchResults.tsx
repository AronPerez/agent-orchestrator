import { useTranslation } from "react-i18next";
import type { DecisionCall } from "../lib/decision-inbox";
import { formatTimeCompact } from "../lib/format-time";
import { cn } from "../lib/utils";
import { decisionPlace } from "./DecisionLinkPreviewCard";
import { DecisionStateDot, decisionStateLabel } from "./DecisionStateLabel";
import { Pressable } from "./ui/pressable";

/**
 * Full-ledger search results as one shared list: every state and age, in the
 * daemon's ranking. Choosing a row opens the same call panel as the board.
 */
export function DecisionSearchResults({ calls, selectedId, onSelect }: { calls: DecisionCall[]; selectedId?: string; onSelect: (callId: string) => void }) {
	const { t } = useTranslation();
	return (
		<ul aria-label={t("decisions.searchResults")} className="divide-y divide-border">
			{calls.map((call) => {
				const place = decisionPlace(call);
				const when = call.state === "done" && call.outcomeAt
					? t("decisions.finishedAt", { time: formatTimeCompact(call.outcomeAt) })
					: t("decisions.raisedAt", { time: formatTimeCompact(call.raisedAt) });
				const selected = call.id === selectedId;
				return (
					<li key={call.id}>
						<Pressable
							aria-current={selected ? "true" : undefined}
							data-decision-result={call.id}
							onClick={() => onSelect(call.id)}
							className={cn(
								"flex w-full min-w-0 flex-col gap-0.5 px-(--space-4_5) py-2 transition-colors duration-(--duration-fast) hover:bg-interactive-hover focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset motion-reduce:transition-none",
								selected && "bg-interactive-active hover:bg-interactive-active",
							)}
						>
							<span className="truncate text-sm font-medium text-foreground">{call.title}</span>
							<span className="flex min-w-0 flex-wrap items-center gap-x-2 text-caption text-muted-foreground">
								<span className="inline-flex items-center gap-1.5 font-medium text-foreground">
									<DecisionStateDot state={call.state} />
									{decisionStateLabel(call.state, t)}
								</span>
								{call.retired ? <span>{t("decisions.retiredMarker")}</span> : null}
								{place ? <span className="truncate">{place}</span> : null}
								<span className="truncate">{call.machineLabel || call.hostId}</span>
								<span className="tabular-nums">{when}</span>
							</span>
						</Pressable>
					</li>
				);
			})}
		</ul>
	);
}
