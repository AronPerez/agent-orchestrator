# Autonomy levels and reserved decisions: design delta to the decision ledger

- **Status:** approved by the person on 2026-11-18 ("approved, yes", relayed by the project orchestrator), on top of the feature shortlist approval of 2026-11-17. The first slice (observe mode) is being built.
- **Changes:** [the decision ledger design](2026-11-14-decision-ledger-design.md). It lifts the non-goal "calls an agent made on the person's behalf ('Watching')" and adds fields and entry kinds. Nothing in this document changes what an existing field or entry kind means.
- **Source:** shortlist entry #9 ("Autonomy levels and reserved decisions") and a read-only study of one fleet's decisions over six days (148 needs-input or stuck reports behind about 45 questions; 52 decisions catalogued, 5 of them risky agent decisions). Both live in the orchestrator's session artifacts, not in this repository.

## Why

The ledger exists to interrupt the person less. In the studied fleet, about 30 interruptions in six days could have been avoided, against about 18 asks that were correctly kept for the person:

- About 11 asks were for decisions an agent could have made and recorded, such as closing a duplicate pull request with its branch kept.
- At least 14 were repeat confirmations. Sessions refused an answer the person had already given because another session relayed it, and asked again in their own thread. This stalled one line of work for about two days.
- About 4 were can-wait questions opened as blocking dialogs overnight.

The person had already written their own list of what to keep: production changes, merges or publishing beyond what they authorized, design choices, gates, and passwords or 2FA only they can enter.

## What changes

### 1. Decision kinds and a per-project policy

A fixed vocabulary of decision kinds (`domain.DecisionKinds`). A project's config gains:

```json
"autonomy": { "mode": "off" | "observe", "reserved": ["production", "design"] }
```

An empty `reserved` list means the kinds reserved by default:

| id | Label | Reserved by default |
| --- | --- | --- |
| `production` | Production changes and deploys | yes |
| `merge_publish` | Merges, releases or publishing beyond what was authorized | yes |
| `destructive` | Destructive or hard-to-undo operations | yes |
| `credentials` | Secrets, credentials, passwords and 2FA | yes |
| `spending` | Spending, and new paid or public infrastructure | yes |
| `public_api` | Public API or schema changes | yes |
| `dependency` | New dependencies | yes |
| `scope` | Scope changes | yes |
| `design` | Design and product behaviour | yes |
| `people` | Actions that reach other people | yes |
| `project_default` | Departing from a project default (agent, model, vendor) | yes |
| `ticket_hygiene` | Ticket hygiene | no |
| `pr_hygiene` | Pull request hygiene | no |
| `tests_hooks` | Test and hook handling, short of deleting tests | no |
| `session_lifecycle` | Session lifecycle | no |
| `test_fixtures` | Test fixtures and leftovers outside production | no |
| `observability` | Observability and alert config, after a dry run | no |
| `interpretation` | Interpreting an instruction | no |
| `other` | Anything else | yes |

`people` and `project_default` come from the study: reassigning a teammate's ticket and switching a fleet to a different agent than the project's default were both decisions the person wanted to make. `other` stays reserved, so a decision that fits no kind is never made alone.

### 2. Modes, shipped observe-first

| Mode | What sessions do |
| --- | --- |
| `off` (default) | As before. |
| `observe` | Ask everything they would ask today, and also ask before any action of a reserved kind. Every call names its kind. For a kind that is not reserved, the call also records what the session would have decided, why, and how to undo it. The session still waits for the answer and follows it. |
| `on` | Raise a call for reserved kinds. Decide the rest, record a `decided` entry **before** acting (so an interrupted session still leaves a record), then act; a decision not carried out after all is closed by its session with an outcome. Set through project config (`ao project set-config --config-json`); until the person decides whether Settings should offer it, the settings page only shows it for a project already in it, so saving there never downgrades it. |

The policy reaches sessions as a standing-instructions section, rendered only in observe (and later on) mode. Standing instructions are built when a session starts, so a change to the mode or the reserved list reaches sessions started after it. A running session keeps the instructions it started with, and its calls are never refused for lacking a kind it was never told to give. It is a guard rail, not a boundary: an agent can file a decision under the wrong kind. The observe period exists to find that out before any session acts alone.

### 3. New fields on the `raised` entry

All optional, all ignored by older machines (section 5 of the ledger design):

| Field | Written by | Meaning |
| --- | --- | --- |
| `urgency` | Session | `blocking` (all its remaining work depends on the answer) or `can_wait`. Absent means can wait. |
| `kind` | Session | A decision kind id. Observe-mode instructions ask for it on every call. It is not enforced, because a session started before the switch has no such instruction; calls without a kind are left out of the observe readout. |
| `prediction` | Session | `{option, why, undo}`: what the session would have decided on its own. Required in observe mode for kinds that are not reserved; refused for reserved kinds in every mode. |
| `reserved`, `autonomyMode` | Orchestrator | The project's policy at the moment the call was raised. Snapshotted, so a later policy change does not rewrite history and every machine reads the same thing. |

The read model gains `predictionAgreed`: once a predicted call has an answer, whether the person picked the predicted option. "Something else" counts as a different decision.

**Urgency and notifications.** Urgency is data on the call. Routing it belongs to the notifications slice: a blocking call may push to the phone; a can-wait call goes to the bell and may notify again later. This delta sends no notification itself.

### 4. Answers recorded in the ledger bind every session

A rule for every session, in every mode: an answer the person recorded in the ledger is their decision, however it reaches a session — delivered by Orchestrator, or relayed by another session. A session that receives a relayed answer checks it with `ao decision show <call-link>` and acts on the call's **current** answer and note, not on an older answer still in its append-only history, and not on a call marked no longer applies. If the current answer differs from what was relayed, it follows the current one. It never asks the person to confirm it again. The delivered answer message says the same thing, so a session that forwards it forwards the rule too.

This was the largest single cost in the study, and without it the rest of this design adds a layer while the main cost stays.

### 5. Decided entries and overrule

Two new entry kinds:

- `decided`: written by a session in `on` mode for a kind the project has not reserved. Carries what was decided, why, **how to undo it (required)**, optionally how to know it worked, and the kind, plus the same Orchestrator-filled project, stream, machine and owner fields as `raised`. It forms a call in a new state, "Decided for you", listed on the Decisions page.
- `overruled`: written only by the person, with a required note. It becomes the call's standing entry and is delivered to the owning session like an answer, including the fallback to the project's orchestrator when that session has ended. The session undoes the decision and records an outcome, which closes the call.

Sessions record one with `ao decision decide`. Because a session acts before it records, a record is never refused for policy reasons: if the project left mode `on` or reserved the kind after the session started, the record is kept with `outsidePolicy` set (`autonomy_not_on` or `kind_reserved`), so the person can still see it. Since sessions record before acting, the command then tells the session not to carry it out, to close the record with a "Not carried out" outcome, and to raise a call instead. Malformed records, such as one without an undo note, are refused. the person overrules with `ao decision overrule <id> --note` or from the Decisions page, and lists them with `ao decision ls --decided` (`GET /api/v1/decisions?scope=decided`). The catch-all kind `other` is reserved under every policy, including a custom reserved list, so a decision that fits no kind is never made alone. An older machine stores and passes on both kinds. With no `raised` entry it builds no call from a `decided` entry, and it ignores `overruled`.

## Changes to the ledger design

- **Non-goals:** "calls an agent made on the person's behalf ('Watching')" is lifted by this delta.
- **Section 2, what a call holds:** urgency, kind and prediction, as above.
- **Section 4, entry kinds:** `decided` and `overruled` are added to the list of kinds.
- **Decided by the person:** row 13 records this approval.

## Risks

| Risk | What to do |
| --- | --- |
| A session files a decision under the wrong kind. | Observe mode first. The prediction and the person's answer sit side by side, so a misfiled kind shows up as a disagreement before any session acts alone. |
| Two risky actions in the study were never recognised as decisions at all: copying a staging key in a web page, and dismissing a warning about a path synced to a public repository. | Out of scope. Instructions cannot catch what an agent does not see as a decision; those need tool-level guards. |
| The kind list grows too long to follow. | Keep the vocabulary fixed and short; a project chooses which kinds to reserve, not new kinds. |
| Upstream is changing standing instructions and adding action policies. | Keep the policy's renderer in `domain` and its prompt hook to one call, so a rebase touches one line. |
