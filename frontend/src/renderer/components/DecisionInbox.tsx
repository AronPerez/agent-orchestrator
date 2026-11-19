import { LoaderCircle } from "@/components/app-icons";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { decisionOptionRequiresNote, type DecisionInboxAnswer, type DecisionInboxEntry } from "../lib/decision-inbox";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Label } from "./ui/label";

type DecisionInboxProps = {
	entries: DecisionInboxEntry[];
	isLoading: boolean;
	loadFailed: boolean;
	pending?: DecisionInboxAnswer;
	answerError?: Omit<DecisionInboxAnswer, "option"> & { message: string };
	onAnswer: (entry: DecisionInboxEntry, option: number, note?: string) => void;
	onRetry: () => void;
	/** Calls whose durable announcement is unread (or was when the panel opened). */
	unreadCallIds?: ReadonlySet<string>;
	/** `${sourceHostId}:${callId}` keys whose last answer may already be recorded. */
	unconfirmed?: ReadonlySet<string>;
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
	unreadCallIds,
	unconfirmed,
}: DecisionInboxProps) {
	const { t } = useTranslation();
	const [notes, setNotes] = useState<Record<string, string>>({});

	if (entries.length === 0 && !isLoading && !loadFailed) return null;

	return (
		<section aria-label={t("decisionInbox.title")} className="border-b border-border bg-surface" role="group">
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
				const callKey = `${entry.sourceHostId}:${call.id}`;
				const callPending = sameCall(entry, pending);
				const error = sameCall(entry, answerError) ? answerError?.message : undefined;
				const unread = unreadCallIds?.has(call.id) ?? false;
				// A lost response may hide a recorded answer: nothing is sent until it is checked.
				const held = unconfirmed?.has(callKey) ?? false;
				const disabled = Boolean(pending) || held;
				return (
					<article
						className={cn("border-t border-border px-3 py-3 first:border-t-0", unread && "bg-interactive-hover")}
						key={`${entry.sourceHostId}:${call.id}`}
						role="listitem"
					>
						{unread ? <span className="sr-only">{t("decisionInbox.unread")}</span> : null}
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
								const noteId = `${descriptionId}-note`;
								const requiresNote = decisionOptionRequiresNote(call, optionNumber);
								const note = notes[callKey] ?? "";
								return (
									<div className="min-w-0" key={`${call.id}:${optionNumber}`}>
										{requiresNote ? (
											<form
												onSubmit={(event) => {
													event.preventDefault();
													const trimmedNote = note.trim();
													if (trimmedNote) onAnswer(entry, optionNumber, trimmedNote);
												}}
											>
												<Label className="mb-1.5 text-caption text-muted-foreground" htmlFor={noteId}>{t("workbench.changes")}</Label>
												<Input
													autoComplete="off"
													disabled={disabled}
													id={noteId}
													onChange={(event) => setNotes((current) => ({ ...current, [callKey]: event.target.value }))}
													value={note}
												/>
												<Button
													aria-describedby={descriptionId}
													className="mt-2 w-full min-w-0 justify-start"
													disabled={disabled || !note.trim()}
													size="sm"
													type="submit"
													variant={option.recommended ? "accent" : "secondary"}
												>
													{isPendingOption ? <LoaderCircle className="size-icon-sm shrink-0 animate-spin" aria-hidden="true" /> : null}
													<span className="truncate">{option.label}</span>
												</Button>
											</form>
										) : (
											<Button
												aria-describedby={descriptionId}
												className="w-full min-w-0 justify-start"
												disabled={disabled}
												onClick={() => onAnswer(entry, optionNumber)}
												size="sm"
												type="button"
												variant={option.recommended ? "accent" : "secondary"}
											>
												{isPendingOption ? <LoaderCircle className="size-icon-sm shrink-0 animate-spin" aria-hidden="true" /> : null}
												<span className="truncate">{option.label}</span>
												{option.recommended ? <span className="ml-auto shrink-0 text-caption opacity-75">{t("decisionInbox.recommended")}</span> : null}
											</Button>
										)}
										<p className="mt-1 px-1 text-caption leading-snug text-muted-foreground" id={descriptionId}>{option.consequence}</p>
									</div>
								);
							})}
						</div>
						{error ? <p className="mt-2 text-caption text-error" role="alert">{error}</p> : null}
						{held ? (
							<p className="mt-2 flex flex-wrap items-center gap-x-2 text-caption text-status-in-review" role="status">
								<span>{t("decisionInbox.answerUnconfirmed")}</span>
								<Button onClick={onRetry} size="xs" type="button" variant="link">{t("decisionInbox.checkAgain")}</Button>
							</p>
						) : null}
					</article>
				);
			})}
		</section>
	);
}
