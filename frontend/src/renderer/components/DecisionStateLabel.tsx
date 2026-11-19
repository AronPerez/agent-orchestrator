import type { TFunction } from "i18next";
import type { DecisionCall } from "../lib/decision-inbox";
import { cn } from "../lib/utils";

type DecisionState = DecisionCall["state"];

const STATE_DOT: Record<DecisionState, string> = {
	needs_you: "bg-status-needs-you",
	in_progress: "bg-status-working",
	done: "bg-status-ready",
	dismissed: "bg-status-idle",
	withdrawn: "bg-status-idle",
	decided: "bg-status-idle",
};

/** The one translated name of a decision state, used on columns, cards and results. */
export function decisionStateLabel(state: DecisionState, t: TFunction): string {
	switch (state) {
		case "needs_you": return t("decisions.state.needsYou");
		case "in_progress": return t("decisions.state.inProgress");
		case "done": return t("decisions.state.done");
		case "dismissed": return t("decisions.state.dismissed");
		case "withdrawn": return t("decisions.state.withdrawn");
		case "decided": return t("decisions.state.decided");
	}
}

/** The state's semantic dot. Only Needs you uses the needs-you colour. */
export function DecisionStateDot({ state, className }: { state: DecisionState; className?: string }) {
	return <span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full", STATE_DOT[state], className)} />;
}
