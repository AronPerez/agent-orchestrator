import { LoaderCircle } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import type { DecisionInboxAnswer, DecisionInboxEntry } from "../lib/decision-inbox";
import { Button } from "./ui/button";

type DecisionInboxProps = {
	entries: DecisionInboxEntry[];
	isLoading: boolean;
	loadFailed: boolean;
	pending?: DecisionInboxAnswer;
	answerError?: Omit<DecisionInboxAnswer, "option"> & { message: string };
	onAnswer: (entry: DecisionInboxEntry, option: number) => void;
	onRetry: () => void;
	onRetryAnswer?: () => void;
	onDismissAnswerError?: () => void;
};

function sameCall(entry: DecisionInboxEntry, target?: { sourceHostId: string; callId: string }): boolean {
	return target?.sourceHostId === entry.sourceHostId && target.callId === entry.call.id;
}

export function DecisionInbox({
	entries,
	isLoading,
	loadFailed,
	pending,
	answerError,
	onAnswer,
	onRetry,
	onRetryAnswer,
	onDismissAnswerError,
}: DecisionInboxProps) {
	const { t } = useTranslation();

	if (entries.length === 0 && !isLoading && !loadFailed && !answerError) return null;

	return (
		<section aria-label={t("decisionInbox.title")} className="border-b border-border bg-surface" role="group">
			{answerError ? (
				<div className="flex items-center justify-between gap-3 px-3 py-2 text-caption text-error" role="alert">
					<span>{t("decisionInbox.answerFailed", { message: answerError.message })}</span>
					<div className="flex shrink-0 items-center gap-2">
						<Button onClick={onRetryAnswer} size="xs" type="button" variant="link">{t("notify.retry")}</Button>
						<Button onClick={onDismissAnswerError} size="xs" type="button" variant="link">{t("common.close")}</Button>
					</div>
				</div>
			) : null}
			{loadFailed ? (
				<div className="flex items-center justify-between gap-2 px-3 py-2 text-caption text-error" role="alert">
					<span>{t("decisionInbox.loadFailed")}</span>
					<Button onClick={onRetry} size="xs" type="button" variant="link">{t("notify.retry")}</Button>
				</div>
			) : null}
			{isLoading && entries.length === 0 ? (
				<div className="flex items-center gap-2 px-3 py-3 text-caption text-muted-foreground">
					<LoaderCircle className="size-icon-sm animate-spin" aria-hidden="true" />
					{t("decisionInbox.loading")}
				</div>
			) : null}
			{entries.map((entry) => {
				const { call } = entry;
				const callPending = sameCall(entry, pending);
				return (
					<article className="border-t border-border px-3 py-3 first:border-t-0" key={`${entry.sourceHostId}:${call.id}`} role="listitem">
						<p className="text-caption font-medium text-status-needs-you">
							{call.projectName ? t("decisionInbox.neededForProject", { project: call.projectName }) : t("decisionInbox.needed")}
						</p>
						<h3 className="mt-1 text-control font-medium leading-snug text-foreground">{call.title}</h3>
						<p className="mt-1 whitespace-pre-wrap text-caption leading-relaxed text-muted-foreground">{call.context}</p>
						<p className="mt-1.5 truncate text-caption text-passive">
							{call.ownerSessionName} · {call.machineLabel || entry.sourceHostLabel}
						</p>
						<div className="mt-2.5 grid gap-2">
							{call.options.map((option, index) => {
								const optionNumber = index + 1;
								const isPendingOption = callPending && pending?.option === optionNumber;
								const descriptionId = `decision-${call.id}-option-${optionNumber}`;
								return (
									<div className="min-w-0" key={`${call.id}:${optionNumber}`}>
										<Button
											aria-describedby={descriptionId}
											className="h-auto min-h-control-md w-full min-w-0 justify-start whitespace-normal py-1.5 text-left"
											disabled={Boolean(pending)}
											onClick={() => onAnswer(entry, optionNumber)}
											size="sm"
											type="button"
											variant={option.recommended ? "accent" : "secondary"}
										>
											{isPendingOption ? <LoaderCircle className="size-icon-sm shrink-0 animate-spin" aria-hidden="true" /> : null}
											<span className="min-w-0 flex-1 break-words text-left leading-snug">{option.label}</span>
											{option.recommended ? <span className="ml-auto shrink-0 text-caption opacity-75">{t("decisionInbox.recommended")}</span> : null}
										</Button>
										<p className="mt-1 px-1 text-caption leading-snug text-muted-foreground" id={descriptionId}>{option.consequence}</p>
									</div>
								);
							})}
						</div>
					</article>
				);
			})}
		</section>
	);
}
