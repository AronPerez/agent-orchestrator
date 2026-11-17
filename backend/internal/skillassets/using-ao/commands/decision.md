# ao decision

Ask the human to choose between options, and look up what they have already decided.

## Syntax

```text
ao decision raise --title <question> --context <what-the-human-needs-to-know> --option "<label> :: <what it leads to>" --option "<label> :: <what it leads to>" --recommend <n> [--link "<label>=<url>"]... [--ref <id>]
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
worker whose prompt names an active project orchestrator uses a needs-input
status report with the call link. Any session without a named active project
orchestrator—including a standalone worker, a project worker without an active
orchestrator, or an orchestrator—must explicitly tell the human in your current
response that a decision is waiting, include the call link and
`ao decision show <call-link>`, and not end the turn silently.

## Raising a call

Use `--title` for the question, `--context` for what the human needs to know,
and repeat `--option "<label> :: <what it leads to>"` two to four times.
`--recommend` is the one-based position of the recommended option. Supporting
links use repeatable `--link "<label>=<url>"`; `--ref` records an issue or plan
reference.

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
  "reference": "plan-42"
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
64, answer note 1000, and outcome 300. If a call is refused, the error names
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
