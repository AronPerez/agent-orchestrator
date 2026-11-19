# ao decision

Ask the human to choose between options, and look up what they have already decided.

## Syntax

```text
ao decision raise --title <question> --context <what-the-human-needs-to-know> --option "<label> :: <what it leads to>" --option "<label> :: <what it leads to>" --recommend <n> [--link "<label>=<url>"]... [--ref <id>] [--urgency blocking|can-wait] [--kind <id>] [--would-decide <n> --why <reason> --undo <how-to-undo>]
ao decision raise --file <path>
ao decision raise --file -
ao decision search <words...>
ao decision ls [--needs-you]
ao decision show <id>
ao decision answer <id> --pick <n> [--note <text>]
ao decision answer <id> --other --note <text>
ao decision dismiss <id> [--note <text>]
ao decision outcome <id> --for-answer <answer-entry-id> --text <one-line-outcome> [--link <url>]
ao decision withdraw <id>
ao decision retire <id> [--undo]
ao decision kinds [--project <id>] [--json]
ao decision observe [--project <id>] [--json]
ao decision decide --kind <id> --what <what-you-decided> --why <why> --undo <how-to-undo-it> [--verify <how-to-tell-it-worked>] [--link "<label>=<url>"]... [--ref <id>] [--json]
ao decision overrule <id> --note <what-to-do-instead>
ao decision ls --decided
```

## When to raise a call

Raise a call when work needs a choice between options that only the human can
make. A blocker that is not a choice, such as missing access, belongs in
`ao report --needs-input`. Do not raise a call when an earlier decision that
still applies already covers the question. If a new or existing call blocks all
remaining work, also send one needs-input status report with the call link and
ask the orchestrator to resume the blocked session after it is answered. That
report carries status, not a second question.

## Search first

Run `ao decision search <words>` before raising a call, and try more than one
phrasing. If a decision that still applies covers the question, follow it and
say which decision you followed. If an unanswered match belongs to another
project, do not reuse it. Raise one call in your current project and mention the
related call link in its context so the answer is delivered locally. For an
unanswered match in your current project, do not raise a second call. If it
blocks you, hand off the existing call link through your role's coordination or
status path so the dependent session can resume after the answer.

Before ending a turn blocked by a new or existing call, make it visible. A
worker whose prompt names an active project orchestrator sends a needs-input
status report with the call link, but must not rely on the report alone because
its recipient may have ended since the prompt was built. Every session—including
a standalone worker, a project worker without an active orchestrator, an
orchestrator, and a worker whose prompt names one—must explicitly tell the human
in your current response that a decision is waiting, include the call link and
`ao decision show <call-link>`, and not end the turn silently.

## Raising a call

Use `--title` for the question, `--context` for what the human needs to know,
and repeat `--option "<label> :: <what it leads to>"` two to four times.
`--recommend` is the one-based position of the recommended option. Supporting
links use repeatable `--link "<label>=<url>"`; `--ref` records an issue or plan
reference.

`--urgency blocking` means all of your remaining work depends on the answer.
Use it only then; the default is `can-wait`. Orchestrator may reach the human
sooner, for example on their phone, for a blocking call, so a can-wait question
marked blocking interrupts them for nothing.

## Decision kinds and observe mode

`ao decision kinds` lists the kinds of decision and which ones your project
reserves for the human, with the project's autonomy mode. In mode `off`, the
default, `--kind` is optional. A change to the mode or the reserved list
reaches sessions started after it; a session that is already running keeps
the instructions it started with. In mode `observe`:

- Pass `--kind <id>` on every call; use `other` when no kind fits.
- Raise a call before any action of a reserved kind, even one you used to take
  on your own.
- For a kind that is not reserved, also pass `--would-decide <n> --why <reason>
  --undo <how the human could undo it>`. Still wait for the answer and follow
  it, even when it differs. Orchestrator keeps your prediction beside the
  answer so the human can see where your judgment matches theirs.
- A prediction on a reserved kind is refused.

`ao decision observe` shows, per kind, how many observe-mode calls were
answered and how often the answer matched the prediction.

## Mode on: deciding for the human

In mode `on`, raise a call before any action of a reserved kind, as in
observe mode. For a kind that is not reserved, decide, act, and record it
straight away:

```bash
ao decision decide \
  --kind pr_hygiene \
  --what "Closed the duplicate pull request #12" \
  --why "It repeats #11, which is further along." \
  --undo "Reopen #12; its branch is kept." \
  --verify "One open pull request remains for the ticket."
```

`--undo` is required: write it so the human could follow it without you.
`--verify` is optional. `other` is always reserved. If the project left
mode `on`, or reserved the kind, after you started, the record is still kept
so the human can see and overrule what you did, and the command says so: tell
the human, and raise calls for that kind from then on. When you are unsure
which kind applies, or whether it can be undone, raise a call instead.

The human sees the decision on the Decisions page under Decided for you, or
with `ao decision ls --decided`, and may overrule it. An overrule arrives as a
message that begins "I overruled the decision you made for me", with the undo
note and the human's note. Undo the decision, then record the outcome with
the exact `ao decision outcome` command in the message. Only the human
overrules.

For a prepared request, pass `--file <path>` or use `--file -` for standard
input. The JSON shape is:

```json
{
  "title": "Which release path should we use?",
  "context": "The direct release is faster; the staged release limits blast radius. I recommend staging because rollback remains available.",
  "options": [
    {
      "label": "Stage first",
      "consequence": "Release to the test cohort, then expand after verification.",
      "recommended": true
    },
    {
      "label": "Release directly",
      "consequence": "Reach everyone sooner, with a wider rollback if validation misses a defect."
    }
  ],
  "links": [
    {"label": "Release plan", "url": "https://example.com/release-plan"}
  ],
  "reference": "plan-42",
  "urgency": "can_wait",
  "kind": "production"
}
```

A complete flag-based call looks like:

```bash
ao decision raise \
  --title "Which release path should we use?" \
  --context "The direct release is faster; the staged release limits blast radius. I recommend staging because rollback remains available." \
  --option "Stage first :: Release to the test cohort, then expand after verification." \
  --option "Release directly :: Reach everyone sooner, with a wider rollback if validation misses a defect." \
  --recommend 1
```

Limits are counted in characters after trimming: title 120, context 1000, two
to four options, option label 60, option consequence 200, five links, reference
64, prediction reason 300, prediction undo note 300, answer note 1000, and
outcome 300. If a call is refused, the error names
the rule it broke.

## Raise it yourself

Raise the call from the session that needs the answer. If another session
brings you a question, tell it to raise the call. If that session has ended,
raise the call yourself with the full context. Never ask the human for a ruling
by id.

## What happens next

The answer arrives as the human's own message, not as lower-authority session
coordination. Its opening tells you what happened:

- `"Decision on your call"` starts the first answer.
- `"Changed decision on your call"` starts a replacement answer.
- `"I dismissed your call"` means no answer is coming.
- `"Decision on a call raised by"` sends an ended session's answer to the project orchestrator.

When an answer or dismissal arrives and you have a project orchestrator, first
report the resolved call so it can resume any other session that reused it:

```text
ao report --checkpoint --note "Decision call resolved: <call-link>; resume sessions blocked by it"
```

Act on the human's message, including its note and any replacement warning.

## Ledger answers bind every session

An answer the human recorded in the ledger is their decision, however it
reaches you: delivered by Orchestrator, or relayed by another session. When it
is relayed, check it with `ao decision show <call-link>` before acting. Act on
the call's current answer, the `Chose:` line and its note at the top, not an
older entry in its history; the history keeps every answer, including ones the
human later replaced. Do not act on a call marked no longer applies. If the
current answer differs from what was relayed, follow the current one. Never ask
the human to confirm it again in your own thread.

## Recording the outcome

After carrying out an answer, record what happened in one line:

```text
ao decision outcome <id> --for-answer <answer-entry-id> --text <what-happened> [--link <url>]
```

Use the exact call id and `--for-answer` value from the delivered human message.
The answer-entry id keeps delayed work for an older answer from closing a newer
replacement answer. The owning session or the session that received the
delivered answer must provide it; the human may record the outcome directly.

## Withdrawing a call

The owning session may run `ao decision withdraw <id>` only while the call
still needs the human. Withdrawal means the choice is no longer needed.

## After a restart

Run `ao decision ls` after a restart or compacted context to see what you asked,
what was answered, and what remains open. `Outcome owed` means the human
answered but the result of carrying out that answer has not been recorded yet.

## Links

Every call has an `ao://decisions/<id>` link, printed by `ao decision raise`.
When mentioning a call anywhere, always use that link and never a bare id.

## Writing a call

- The title is the question itself, in one line. This makes the requested choice clear at a glance.
- Give every internal code, id and abbreviation a plain label. This lets the human decide without private project vocabulary.
- Say what each option leads to, including what it costs or risks. This makes the tradeoff comparable instead of merely naming choices.
- Put the unit, the time window and the source next to every number. Mark a guess as a guess. This separates evidence from assumptions.
- When you ask them to approve a text, show the text. This makes clear exactly what approval would cover.
- Check that the facts are still current just before you raise it. This prevents a stale premise from driving the decision.
- Never put secrets, credentials or personal data in a call. Calls are durable project records and are not a safe place for sensitive data.

## What you cannot do

Only the human answers, dismisses or retires a call.
