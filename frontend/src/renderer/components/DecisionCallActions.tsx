import { useId, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useDecisionAction, useDecisionAnswer, useDecisionAnswerHold } from "../hooks/useDecisionInbox";
import { detailView } from "../lib/decision-answer-guard";
import { apiErrorRequestId } from "../lib/api-client";
import { decisionOptionRequiresNote, type DecisionCall } from "../lib/decision-inbox";
import { LOCAL_HOST } from "../lib/hosts";
import { cn } from "../lib/utils";
import { useDecisionDraft } from "./DecisionDraftScope";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { RadioGroup, RadioGroupItem } from "./ui/radio-group";
import { Textarea } from "./ui/textarea";

const OUTCOME_MAX = 300;
const OTHER = "other";

function isWebUrl(url: string): boolean {
	try {
		const parsed = new URL(url);
		return parsed.protocol === "https:" || parsed.protocol === "http:";
	} catch {
		return false;
	}
}

/**
 * True when the panel offers an answer form for this call. A decision made
 * for the person is never a question, even once overruled: the daemon refuses
 * an answer to it.
 */
export function decisionAcceptsAnswer(call: DecisionCall): boolean {
	if (call.decided) return false;
	return call.state === "needs_you" || call.state === "in_progress" || call.state === "dismissed";
}

/**
 * The person's actions on one call. Answers go through the bell's shared
 * answer mutation; dismiss, outcome and retire through the matching ledger
 * action. One request at a time, nothing changes before the daemon agrees,
 * and every result refetches the call.
 */
export function DecisionCallActions({
	call,
	extraActions,
	onRecheck,
}: {
	call: DecisionCall;
	extraActions?: ReactNode;
	onRecheck?: () => void;
}) {
	const { t } = useTranslation();
	const answer = useDecisionAnswer();
	const action = useDecisionAction();
	const busy = answer.isPending || action.isPending;
	// The daemon may commit an answer whose response is lost. The hold is shared
	// with the bell and outlives this panel: until a read started after the
	// failure succeeds, sending again could deliver the answer twice.
	const view = detailView(call.id);
	const hold = useDecisionAnswerHold(call.id, view);
	const unconfirmed = hold !== undefined;
	// A standing decision is closed by the person without naming an answer: it has none.
	const outcomeFor = call.state === "decided" ? undefined : call.standingEntryId;
	const failure = answer.isError ? answer.error : action.isError ? action.error : undefined;
	const reset = () => {
		answer.reset();
		action.reset();
	};
	return (
		<div className="flex flex-col gap-4">
			{decisionAcceptsAnswer(call) ? (
				<AnswerForm
					// A changed standing answer or state starts the form over from the new facts.
					key={`${call.state}:${call.standingEntryId ?? ""}`}
					call={call}
					busy={busy}
					unconfirmed={unconfirmed}
					extraActions={extraActions}
					onAnswer={(choice, note) => {
						reset();
						answer.mutate({
							view,
							sourceHostId: LOCAL_HOST,
							callId: call.id,
							choice: choice === OTHER ? "other" : "option",
							option: choice === OTHER ? undefined : Number(choice),
							note,
						});
					}}
					onDismiss={(note) => {
						reset();
						action.mutate({ kind: "dismiss", callId: call.id, note });
					}}
				/>
			) : call.state === "decided" ? (
				<OverruleForm
					busy={busy}
					extraActions={extraActions}
					onOverrule={(note) => {
						reset();
						action.mutate({ kind: "overrule", callId: call.id, note });
					}}
				/>
			) : extraActions ? <div className="flex flex-wrap items-center gap-2">{extraActions}</div> : null}
			{call.state === "in_progress" || call.state === "decided" ? (
				<OutcomeForm
					forEntry={outcomeFor}
					busy={busy}
					onRecord={(text, link) => {
						reset();
						action.mutate({ kind: "outcome", callId: call.id, forEntry: outcomeFor, text, link });
					}}
				/>
			) : null}
			{hold === "unconfirmed" && decisionAcceptsAnswer(call) ? (
				<p role="status" className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-status-in-review">
					<span>{t("decisions.answerUnconfirmed")}</span>
					{onRecheck ? <Button type="button" size="sm" variant="secondary" onClick={onRecheck}>{t("decisions.checkAgain")}</Button> : null}
				</p>
			) : null}
			{call.state === "done" ? <RetireAction call={call} busy={busy} onRetire={(retired) => {
				reset();
				action.mutate({ kind: "retire", callId: call.id, retired });
			}} /> : null}
			{failure ? <ActionError error={failure} /> : null}
		</div>
	);
}

function ActionError({ error }: { error: unknown }) {
	const { t } = useTranslation();
	const requestId = apiErrorRequestId(error instanceof Error ? error.cause : undefined);
	return (
		<p role="alert" className="flex flex-wrap gap-x-2 text-sm text-destructive">
			<span>{error instanceof Error ? error.message : t("decisions.loadFailedOne")}</span>
			{requestId ? <span className="font-mono text-caption text-muted-foreground">{t("decisions.requestId", { id: requestId })}</span> : null}
		</p>
	);
}

function AnswerForm({
	call,
	busy,
	unconfirmed,
	extraActions,
	onAnswer,
	onDismiss,
}: {
	call: DecisionCall;
	busy: boolean;
	/** An earlier answer may have been recorded; nothing is sent until it is checked. */
	unconfirmed: boolean;
	extraActions?: ReactNode;
	onAnswer: (choice: string, note: string) => void;
	onDismiss: (note: string) => void;
}) {
	const { t } = useTranslation();
	const id = useId();
	const standing = call.state === "in_progress" && call.choice
		? call.choice === "other" ? OTHER : String(call.option ?? "")
		: "";
	const [choice, setChoice] = useState(standing);
	const [note, setNote] = useState(call.state === "in_progress" ? call.note ?? "" : "");
	const [noteMissing, setNoteMissing] = useState(false);
	const [dismissing, setDismissing] = useState(false);
	const [dismissNote, setDismissNote] = useState("");
	// Typed text that has not been sent; a pick alone is quick to make again.
	useDecisionDraft(note.trim() !== (call.state === "in_progress" ? call.note ?? "" : "").trim() || dismissNote.trim() !== "");
	// Sending the standing answer again would only append and deliver a copy.
	const unchanged = standing !== "" && choice === standing && note.trim() === (call.note ?? "").trim();
	const blocked = busy || unconfirmed || !choice || unchanged;

	const submit = (event: FormEvent) => {
		event.preventDefault();
		if (blocked) return;
		if (!note.trim() && (choice === OTHER || decisionOptionRequiresNote(call, Number(choice)))) {
			setNoteMissing(true);
			return;
		}
		onAnswer(choice, note);
	};
	const noteErrorId = `${id}-note-error`;
	const hintId = `${id}-hint`;
	const hint = call.state === "in_progress" ? t("decisions.changeHint") : call.state === "dismissed" ? t("decisions.reopenHint") : undefined;

	return (
		<form noValidate onSubmit={submit} className="flex flex-col gap-3" aria-describedby={hint ? hintId : undefined}>
			<fieldset className="flex flex-col gap-2">
				<legend className="sr-only">{t("decisions.answerLegend")}</legend>
				<RadioGroup
					aria-label={t("decisions.answerLegend")}
					value={choice}
					onValueChange={(value) => {
						setChoice(value);
						setNoteMissing(false);
					}}
					disabled={busy}
					className="gap-0 overflow-hidden rounded-lg bg-card"
				>
					{call.options.map((option, index) => {
						const value = String(index + 1);
						const itemId = `${id}-option-${value}`;
						return (
							<label key={value} htmlFor={itemId} className="flex cursor-pointer items-start gap-2.5 border-border px-3 py-2 not-first:border-t has-[:disabled]:cursor-not-allowed">
								<RadioGroupItem id={itemId} value={value} className="mt-0.5" />
								<span className="min-w-0 flex-1">
									<span className="block text-sm font-medium text-foreground">{option.label}</span>
									{option.consequence ? <span className="block text-caption text-muted-foreground">{option.consequence}</span> : null}
									{option.recommended ? <span className="mt-0.5 block text-caption text-muted-foreground">{t("decisionInbox.recommended")}</span> : null}
								</span>
							</label>
						);
					})}
					<label htmlFor={`${id}-option-other`} className="flex cursor-pointer items-start gap-2.5 border-t border-border px-3 py-2 has-[:disabled]:cursor-not-allowed">
						<RadioGroupItem id={`${id}-option-other`} value={OTHER} className="mt-0.5" />
						<span className="text-sm font-medium text-foreground">{t("decisions.somethingElse")}</span>
					</label>
				</RadioGroup>
			</fieldset>
			<div className="flex flex-col gap-1.5">
				<Label htmlFor={`${id}-note`}>{t("decisions.noteLabel")}</Label>
				<Textarea
					id={`${id}-note`}
					value={note}
					maxLength={1000}
					disabled={busy}
					aria-invalid={noteMissing || undefined}
					aria-describedby={noteMissing ? noteErrorId : undefined}
					onChange={(event) => {
						setNote(event.target.value);
						if (event.target.value.trim()) setNoteMissing(false);
					}}
				/>
				{noteMissing ? (
					<p id={noteErrorId} className="text-caption text-destructive">
						{choice === OTHER ? t("decisions.noteRequired") : t("decisions.changesRequired")}
					</p>
				) : null}
			</div>
			{hint ? <p id={hintId} className="text-caption text-muted-foreground">{hint}</p> : null}
			<div className="flex flex-wrap items-center gap-2">
				<Button type="submit" variant="primary" size="sm" disabled={blocked} aria-busy={busy || undefined}>
					{t("decisions.send")}
				</Button>
				{extraActions}
				{call.state === "needs_you" && !dismissing ? (
					<Button type="button" variant="quiet" size="sm" disabled={busy} className="ml-auto" onClick={() => setDismissing(true)}>
						{t("decisions.dismiss")}
					</Button>
				) : null}
			</div>
			{call.state === "needs_you" && dismissing ? (
				<div className="flex flex-col gap-2 rounded-lg bg-card px-3 py-2.5">
					<Label htmlFor={`${id}-dismiss-note`}>{t("decisions.dismissNoteLabel")}</Label>
					<Textarea id={`${id}-dismiss-note`} value={dismissNote} maxLength={1000} disabled={busy} className="min-h-12" onChange={(event) => setDismissNote(event.target.value)} />
					<div className="flex items-center gap-2">
						<Button type="button" variant="secondary" size="sm" disabled={busy} onClick={() => onDismiss(dismissNote)}>
							{t("decisions.dismissConfirm")}
						</Button>
						<Button type="button" variant="quiet" size="sm" disabled={busy} onClick={() => setDismissing(false)}>
							{t("decisions.cancel")}
						</Button>
					</div>
				</div>
			) : null}
		</form>
	);
}

function OutcomeForm({ forEntry, busy, onRecord }: { forEntry?: string; busy: boolean; onRecord: (text: string, link: string) => void }) {
	const { t } = useTranslation();
	const id = useId();
	const [text, setText] = useState("");
	const [link, setLink] = useState("");
	const [errors, setErrors] = useState<{ text?: string; link?: string }>({});
	// The answer this draft was written against. If another surface changes the
	// answer meanwhile, the draft stays and the person reviews the new answer
	// before recording, instead of losing what they typed.
	const [draftFor, setDraftFor] = useState(forEntry);
	useDecisionDraft(text.trim() !== "" || link.trim() !== "");
	const answerChanged = Boolean(text.trim() || link.trim()) && draftFor !== forEntry;
	const submit = (event: FormEvent) => {
		event.preventDefault();
		if (busy) return;
		if (answerChanged) {
			setDraftFor(forEntry);
			return;
		}
		const next: { text?: string; link?: string } = {};
		const trimmed = text.trim();
		if (!trimmed || /[\r\n]/.test(trimmed)) next.text = t("decisions.outcomeRequired");
		if (link.trim() && !isWebUrl(link.trim())) next.link = t("decisions.linkInvalid");
		setErrors(next);
		if (next.text || next.link) return;
		onRecord(trimmed, link.trim());
	};
	return (
		<form noValidate onSubmit={submit} className="flex flex-col gap-2 border-t border-border pt-3">
			<fieldset className="flex flex-col gap-2">
				<legend className="mb-1 text-caption font-medium text-muted-foreground">{t("decisions.outcomeLegend")}</legend>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor={`${id}-text`}>{t("decisions.outcomeText")}</Label>
					<Input
						id={`${id}-text`}
						value={text}
						maxLength={OUTCOME_MAX}
						disabled={busy}
						aria-invalid={errors.text ? true : undefined}
						aria-describedby={errors.text ? `${id}-text-error` : undefined}
						onChange={(event) => {
							if (!text.trim() && !link.trim()) setDraftFor(forEntry);
							setText(event.target.value);
						}}
					/>
					{errors.text ? <p id={`${id}-text-error`} className="text-caption text-destructive">{errors.text}</p> : null}
				</div>
				<div className="flex flex-col gap-1.5">
					<Label htmlFor={`${id}-link`}>{t("decisions.outcomeLink")}</Label>
					<Input
						id={`${id}-link`}
						type="url"
						inputMode="url"
						value={link}
						disabled={busy}
						aria-invalid={errors.link ? true : undefined}
						aria-describedby={errors.link ? `${id}-link-error` : undefined}
						onChange={(event) => setLink(event.target.value)}
					/>
					{errors.link ? <p id={`${id}-link-error`} className="text-caption text-destructive">{errors.link}</p> : null}
				</div>
			</fieldset>
			{answerChanged ? <p role="status" className="text-caption text-status-in-review">{t("decisions.outcomeAnswerChanged")}</p> : null}
			<div>
				<Button type="submit" variant="secondary" size="sm" disabled={busy} aria-busy={busy || undefined}>
					{t("decisions.recordOutcome")}
				</Button>
			</div>
		</form>
	);
}

/** Overruling a decision made for the person needs a note saying what to do instead. */
function OverruleForm({ busy, extraActions, onOverrule }: { busy: boolean; extraActions?: ReactNode; onOverrule: (note: string) => void }) {
	const { t } = useTranslation();
	const id = useId();
	const [note, setNote] = useState("");
	const [missing, setMissing] = useState(false);
	useDecisionDraft(note.trim() !== "");
	const submit = (event: FormEvent) => {
		event.preventDefault();
		if (busy) return;
		if (!note.trim()) {
			setMissing(true);
			return;
		}
		onOverrule(note);
	};
	return (
		<form noValidate onSubmit={submit} className="flex flex-col gap-2">
			<div className="flex flex-col gap-1.5">
				<Label htmlFor={`${id}-note`}>{t("decisions.overruleLabel")}</Label>
				<Textarea
					id={`${id}-note`}
					value={note}
					maxLength={1000}
					disabled={busy}
					aria-invalid={missing || undefined}
					aria-describedby={missing ? `${id}-error` : undefined}
					onChange={(event) => {
						setNote(event.target.value);
						if (event.target.value.trim()) setMissing(false);
					}}
				/>
				{missing ? <p id={`${id}-error`} className="text-caption text-destructive">{t("decisions.noteRequired")}</p> : null}
			</div>
			<div className="flex flex-wrap items-center gap-2">
				<Button type="submit" variant="primary" size="sm" disabled={busy} aria-busy={busy || undefined}>
					{t("decisions.overrule")}
				</Button>
				{extraActions}
			</div>
		</form>
	);
}

function RetireAction({ call, busy, onRetire }: { call: DecisionCall; busy: boolean; onRetire: (retired: boolean) => void }) {
	const { t } = useTranslation();
	return (
		<div className={cn("flex items-center gap-2")}>
			<Button type="button" variant="secondary" size="sm" disabled={busy} aria-busy={busy || undefined} onClick={() => onRetire(!call.retired)}>
				{call.retired ? t("decisions.unretire") : t("decisions.retire")}
			</Button>
		</div>
	);
}
