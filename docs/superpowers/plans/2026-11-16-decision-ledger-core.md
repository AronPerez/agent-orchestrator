# Decision Ledger Core (Slice 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On one machine and from a terminal, a session can raise a call, the person can answer it, the answer is delivered into the session, and the session records the outcome.

**Architecture:** An append-only table of entries is the only truth. A pure reducer in `domain` turns one call's entries into its current state, and the store rewrites that call's row in a rebuildable read table whenever an entry is appended. A service applies the rules about who may do what. A background deliverer sends each answer into the owning session as the person's own message. The `ao decision` command group is a thin client over new daemon routes.

**Tech Stack:** Go (daemon and Cobra CLI), SQLite with goose migrations, sqlc and FTS5, chi, code-first OpenAPI with `openapi-typescript`.

**Spec:** `docs/superpowers/specs/2026-11-14-decision-ledger-design.md` once Task 1 copies it into the repo. Until then it is at `/Users/among/.ao/data/artifacts/agent-orchestrator-7/2026-11-14-decision-ledger-design.md`. This plan implements slice 1 of that design. "Section N" below means a section of the spec.

File paths and line numbers are as of commit `ccd3d9713` on `develop`.

## Scope

In this plan: the entry log, the read table, search, the service, delivery of answers, the daemon routes, the `ao decision` commands, and what sessions are told.

Not in this plan, and not to be built here:

- Notifications (slice 2), the Decisions page (slice 3), syncing between machines and the link commands (slice 4), the phone (slice 5).
- Story pages (slice 6). The `raised` payload has its `page` field from now on, but nothing sets it.
- Asking for more from a call (section 13).
- Change-stream triggers for the new tables. Slice 3 adds them when something listens.
- A command that rebuilds the read table from the log.
- Telemetry events.

### Pull requests

Three pull requests, each targeting `develop`, each leaving the product working:

| PR | Tasks | After it merges |
| --- | --- | --- |
| 1 | 1 to 8 | A call can be raised, searched, listed and shown from a terminal. Sessions are not told about it yet. |
| 2 | 9 to 12 | The person can answer, the answer reaches the session, and the outcome can be recorded. |
| 3 | 13 to 15 | Sessions are told to use it. |

They merge in that order. PR 3 names commands and message openings that PR 2 defines, so it must not merge first.

## Global Constraints

- All state lives under `~/.ao`. Add no listener and change no bind address.
- The CLI is a thin client. Nothing in `backend/internal/cli` opens the database.
- Create the migration with `npm run migration:create -- decision_ledger` and append its id and filename to `shippedMigrations` in `backend/internal/storage/sqlite/migrate_burned_versions_test.go`. Never edit a merged migration.
- Never hand-edit `backend/internal/storage/sqlite/gen/*`. Change `queries/` or the migration and run `npm run sqlc`.
- The daemon API is code-first. Edit `controllers/dto.go` and `apispec/specgen/build.go`, run `npm run api`, and commit `openapi.yaml` and `frontend/src/api/schema.ts` with the Go change.
- Entries are appended and never updated or deleted. There is no `UPDATE` or `DELETE` query on `decision_entries`.
- An entry's kind is free text with no `CHECK` constraint. An unknown kind, and unknown payload fields, are stored byte for byte and ignored when computing state.
- A call's state is computed by `domain.ReduceDecisionCall` from its entries. Nothing sets a state directly.
- Limits, counted in characters after trimming: title 120, context 1000, two to four options, option label 60, option consequence 200, five links, reference 64, note 1000, outcome 300.
- State names shown to people: `Needs you`, `In progress`, `Done`, `Dismissed`, `Withdrawn`.
- Call text never goes to telemetry or logs. Log ids only.
- CLI misuse returns a `usageError` (exit 2). Daemon failures exit 1 and keep the API error envelope's message.
- Tests use `httptest`, fakes and `sqlitetest.MustOpen`. No network.
- Conventional commit messages. Follow `.agents/skills/pr-description/SKILL.md` for PR descriptions.

## Decisions made in this plan

The spec left these to the builder. They are decided here so that every task agrees.

| # | Decision | Why |
| --- | --- | --- |
| 1 | The command group is `ao decision` with `raise`, `search`, `ls`, `show`, `outcome`, `withdraw`, `answer`, `dismiss` and `retire`. | `ls` is the listing verb most groups use. |
| 2 | A call's link is `ao://decisions/<call-id>`. Slice 1 only prints it. | Same family as `ao://sessions/...`. |
| 3 | A call's id is `dec_` plus a UUID. An entry's id is `dent_` plus a UUID. | Unique across machines without coordination. |
| 4 | The order stamp is a hybrid logical clock: wall-clock milliseconds plus a counter. A new entry's stamp is derived from the newest stamp already stored. Ties are broken by origin, then entry id. | Meets the three ordering requirements in section 4 with no clock table. |
| 5 | An answer is delivered into the session as the person's own message (`AuthoredByUser`), not as coordination from another session. | Sessions treat coordination as lower trust and may refuse to act on it. An answer carries the person's authority. |
| 6 | Delivery is at least once in one narrow case: the daemon stops after sending and before recording. Otherwise it is exactly once. | The session message path has no idempotency key for a user-authored message. A repeated answer is harmless; a lost one is not. |
| 7 | Entries the daemon writes itself (`delivered`) have a third author kind, `system`. A `delivered` entry carries `status`: `sent` or `no_session`. | "No session to carry this out" must show on every machine, so it has to be in the log. |
| 8 | The outcome may be recorded by the owning session, by the session the answer was delivered to, or by the person. | When the owner has ended the answer goes to the project's orchestrator, which must be able to close the call. |
| 9 | Answering a dismissed call reopens it. Answering a done or withdrawn call is refused. | Follows the state rules in section 3. The "changed after it closed" flag is for answers that cross during sync. |
| 10 | The read table keeps the reduced call as one JSON `body` column, with real columns only for filtering and ordering. | The table is rebuildable, and one mapping is less code than thirty columns. |
| 11 | `raise` reads its session from the caller header the CLI already sends from an agent's shell. A request with no caller is the person. | One signal decides who is acting, and it is the signal section 8's guard is defined by. |

## Review Focus

Failure modes the spec implies but does not test. Each has a test in the task that owns the code.

1. **The daemon stops between sending an answer and recording it.** The answer is sent again on the next run and recorded once. Task 12, `TestDelivererSendsAgainWhenRecordingFailed`.
2. **The machine's clock moves backwards.** A later entry still orders after an earlier one. Task 1, `TestDecisionStampNextNeverGoesBackwards`; Task 4, `TestAppendDecisionEntryAssignsSequenceAndStamp`.
3. **A search contains punctuation, quotes or search operators.** It is treated as text and never fails. Task 5, `TestSearchDecisionCallsTreatsPunctuationAsText`.
4. **A session with the same id on another machine, or a different session, tries to close a call.** It is refused. Task 9, `TestOnlyTheOwnerOnThisMachineCanWithdraw`.
5. **The spawn lineage is broken or loops, or the session has no project.** The call is still raised, with no stream. Task 3, `TestDeriveDecisionStreamSurvivesBrokenLineage`; Task 6, `TestRaiseFromASessionWithNoProject`.

## File map

| File | Responsibility |
| --- | --- |
| `backend/internal/domain/decision.go` | Entries, stamps, payloads, limits, the rules a call must meet |
| `backend/internal/domain/decision_reduce.go` | A call's state from its entries |
| `backend/internal/domain/decision_stream.go` | Work streams |
| `backend/internal/storage/sqlite/migrations/<id>_decision_ledger.sql` | The log, the read table, the search index |
| `backend/internal/storage/sqlite/queries/decisions.sql` | Generated queries |
| `backend/internal/storage/sqlite/store/decision_store.go` | Append and read |
| `backend/internal/storage/sqlite/store/decision_search.go` | Hand-written search |
| `backend/internal/service/decision/service.go` | Who may do what |
| `backend/internal/service/decision/message.go` | The text delivered into a session |
| `backend/internal/service/decision/delivery.go` | The background deliverer |
| `backend/internal/httpd/controllers/decisions.go` | Routes |
| `backend/internal/cli/decision.go` | The `ao decision` commands |
| `backend/internal/skillassets/using-ao/commands/decision.md` | The command guide for sessions |

Modified: `controllers/dto.go`, `apispec/specgen/build.go`, `httpd/api.go`, `daemon/daemon.go`, `cli/root.go`, `cli/report.go`, `session_manager/prompt.go`, `skillassets/using-ao/SKILL.md`, `skillassets/using-ao/commands/report.md`, `service/report/coordinator.go`, `domain/session.go`, `frontend/src/docs/content/cli.mdx`.

## How to read the test steps

A test step names each test and gives what it asserts, as Go expressions with the exact values. A line such as `call.State == domain.DecisionStateDone` is an assertion. `X -> CODE` means the call fails with that error code or rule. Lines that are statements set the test up, and fixtures and helpers are described in a sentence. Write the surrounding Go, the loops and the error checks in the style of the neighbouring tests in the same package.

---

# Part A: the log and its read model

### Task 1: Entries, stamps, payloads and the rules a call must meet

**Files:**
- Create: `docs/superpowers/specs/2026-11-14-decision-ledger-design.md`, `docs/superpowers/plans/2026-11-16-decision-ledger-core.md`
- Create: `backend/internal/domain/decision.go`
- Test: `backend/internal/domain/decision_test.go` (`package domain`)

**Interfaces:**
- Consumes: `RequestSurface`, `SessionID`, `ProjectID` from `domain`.
- Produces:

```go
const (
	MaxDecisionTitle       = 120
	MaxDecisionContext     = 1000
	MinDecisionOptions     = 2
	MaxDecisionOptions     = 4
	MaxDecisionOptionLabel = 60
	MaxDecisionConsequence = 200
	MaxDecisionLinks       = 5
	MaxDecisionReference   = 64
	MaxDecisionNote        = 1000
	MaxDecisionOutcome     = 300
)

type DecisionEntryKind string

const (
	DecisionKindRaised    DecisionEntryKind = "raised"
	DecisionKindAnswered  DecisionEntryKind = "answered"
	DecisionKindDismissed DecisionEntryKind = "dismissed"
	DecisionKindDelivered DecisionEntryKind = "delivered"
	DecisionKindOutcome   DecisionEntryKind = "outcome"
	DecisionKindWithdrawn DecisionEntryKind = "withdrawn"
	DecisionKindRetired   DecisionEntryKind = "retired"
)

type DecisionAuthorKind string

const (
	DecisionAuthorPerson  DecisionAuthorKind = "person"
	DecisionAuthorSession DecisionAuthorKind = "session"
	DecisionAuthorSystem  DecisionAuthorKind = "system"
)

type DecisionStamp struct {
	WallMS  int64 `json:"wallMs"`
	Counter int64 `json:"counter"`
}

// Next is the stamp for an entry written at nowMS on a machine whose newest
// stored stamp is s. It is always greater than s.
func (s DecisionStamp) Next(nowMS int64) DecisionStamp

type DecisionEntry struct {
	ID            string
	Origin        string // host id of the machine that wrote it
	OriginSeq     int64  // 1, 2, 3 per origin, no gaps; set by the store
	Stamp         DecisionStamp // set by the store
	CallID        string
	Kind          DecisionEntryKind
	Payload       json.RawMessage
	WrittenAt     time.Time
	AuthorKind    DecisionAuthorKind
	AuthorSurface RequestSurface // person only
	AuthorSession SessionID      // session only
}

// CompareDecisionEntries orders by Stamp.WallMS, Stamp.Counter, Origin, ID.
func CompareDecisionEntries(a, b DecisionEntry) int

type DecisionOption struct {
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
	Recommended bool   `json:"recommended,omitempty"`
}

type DecisionLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type DecisionPageRef struct {
	Fingerprint string `json:"fingerprint"`
	Size        int64  `json:"size"`
}

type DecisionRaisedPayload struct {
	Title            string           `json:"title"`
	Context          string           `json:"context"`
	Options          []DecisionOption `json:"options"`
	Links            []DecisionLink   `json:"links,omitempty"`
	Reference        string           `json:"reference,omitempty"`
	Page             *DecisionPageRef `json:"page,omitempty"`
	ProjectID        ProjectID        `json:"projectId,omitempty"`
	ProjectName      string           `json:"projectName,omitempty"`
	StreamSessionID  SessionID        `json:"streamSessionId,omitempty"`
	StreamName       string           `json:"streamName,omitempty"`
	MachineLabel     string           `json:"machineLabel"`
	OwnerSessionID   SessionID        `json:"ownerSessionId"`
	OwnerSessionName string           `json:"ownerSessionName"`
}

const (
	DecisionChoiceOption = "option"
	DecisionChoiceOther  = "other"
)

type DecisionAnsweredPayload struct {
	Choice string `json:"choice"`           // DecisionChoiceOption or DecisionChoiceOther
	Option int    `json:"option,omitempty"` // 1-based position in the call's options
	Note   string `json:"note,omitempty"`
}

type DecisionDismissedPayload struct {
	Note string `json:"note,omitempty"`
}

const (
	DecisionDeliveredSent      = "sent"
	DecisionDeliveredNoSession = "no_session"
)

type DecisionDeliveredPayload struct {
	ForEntry    string    `json:"forEntry"` // the answered or dismissed entry this delivers
	Status      string    `json:"status"`
	SessionID   SessionID `json:"sessionId,omitempty"`
	SessionName string    `json:"sessionName,omitempty"`
	Fallback    bool      `json:"fallback,omitempty"` // went to the project's orchestrator
}

type DecisionOutcomePayload struct {
	Text string `json:"text"`
	Link string `json:"link,omitempty"`
}

type DecisionRetiredPayload struct {
	Retired bool `json:"retired"`
}

// DecisionRuleError names the one rule a call or answer broke.
type DecisionRuleError struct{ Rule, Message string }

func (e *DecisionRuleError) Error() string // returns Message

func ValidateDecisionRaise(p DecisionRaisedPayload) error
func ValidateDecisionAnswer(optionCount int, p DecisionAnsweredPayload) error
func ValidateDecisionNote(note string) error
func ValidateDecisionOutcome(p DecisionOutcomePayload) error

// DecisionCallURL is "ao://decisions/" plus the path-escaped call id.
func DecisionCallURL(callID string) string
```

The rules, in the order they are checked. A validator returns the first one broken, as a `*DecisionRuleError`. Lengths count characters (`utf8.RuneCountInString`) after `strings.TrimSpace`.

| Rule | Message |
| --- | --- |
| `title_required` | `A call needs a title: the question itself, in one line.` |
| `title_one_line` | `The title must be one line.` |
| `title_too_long` | `The title is %d characters; the limit is 120.` |
| `context_required` | `A call needs context: what the person needs to know to choose.` |
| `context_too_long` | `The context is %d characters; the limit is 1000.` |
| `options_count` | `A call needs two to four options; this one has %d.` |
| `option_label_required` | `Option %d has no label.` |
| `option_label_too_long` | `Option %d's label is %d characters; the limit is 60.` |
| `option_consequence_required` | `Option %d does not say what it leads to.` |
| `option_consequence_too_long` | `Option %d's consequence is %d characters; the limit is 200.` |
| `recommended_count` | `Mark exactly one option as recommended; this call marks %d.` |
| `links_count` | `A call can carry at most five links; this one has %d.` |
| `link_invalid` | `Link %d needs a label and an http or https URL.` |
| `reference_too_long` | `The reference is %d characters; the limit is 64.` |
| `choice_invalid` | `Choose one of the call's options, or "something else".` |
| `option_range` | `This call has %d options; %d is not one of them.` |
| `note_required` | `"Something else" needs a note that says what to do.` |
| `note_too_long` | `The note is %d characters; the limit is 1000.` |
| `outcome_required` | `Say what happened, in one line.` |
| `outcome_one_line` | `The outcome must be one line.` |
| `outcome_too_long` | `The outcome is %d characters; the limit is 300.` |
| `outcome_link_invalid` | `The outcome link must be an http or https URL.` |

- [ ] **Step 1: Bring the design and this plan into the repo**

Copy the spec to `docs/superpowers/specs/2026-11-14-decision-ledger-design.md` and this plan to `docs/superpowers/plans/2026-11-16-decision-ledger-core.md`. In the spec's header, change the status line to say implementation has started and delete the "When implementation starts" line.

```bash
git add docs/superpowers/specs/2026-11-14-decision-ledger-design.md docs/superpowers/plans/2026-11-16-decision-ledger-core.md
git commit -m "docs: add decision ledger design and slice 1 plan"
```

- [ ] **Step 2: Write the failing tests**

```go
// validDecisionRaise(): the fixture later tasks reuse.
DecisionRaisedPayload{
	Title:   "Which auth scheme should the proxy use?",
	Context: "The connector calls the proxy for each tenant.",
	Options: []DecisionOption{
		{Label: "OAuth client credentials", Consequence: "One app registration per tenant.", Recommended: true},
		{Label: "Per-tenant API keys", Consequence: "Customers rotate keys by hand."},
	},
	MachineLabel: "mac-mini", OwnerSessionID: "w-1", OwnerSessionName: "w-1",
}

// TestDecisionStampNextNeverGoesBackwards
s := DecisionStamp{WallMS: 1000, Counter: 2}
s.Next(1500) == DecisionStamp{WallMS: 1500}
s.Next(1000) == DecisionStamp{WallMS: 1000, Counter: 3}
s.Next(400)  == DecisionStamp{WallMS: 1000, Counter: 3} // the clock moved back

// TestCompareDecisionEntriesBreaksTiesByOriginThenID
// With the same stamp {WallMS: 5}: a = {ID "dent_b", Origin "h_a"}, b = {ID "dent_a", Origin "h_b"},
// c = {ID "dent_c", Origin "h_a"}. later = {ID "dent_0", Origin "h_0", Stamp {WallMS: 5, Counter: 1}}.
// The order is a, c, b, later, and CompareDecisionEntries(a, a) == 0.

// TestValidateDecisionRaiseNamesTheBrokenRule
// Each edit to validDecisionRaise() breaks exactly the rule named. Assert with errors.As and rule.Rule.
p.Title = "  "                                                      // title_required
p.Title = "a\nb"                                                    // title_one_line
p.Title = strings.Repeat("界", 121)                                  // title_too_long
p.Context = ""                                                      // context_required
p.Context = strings.Repeat("x", 1001)                               // context_too_long
p.Options = p.Options[:1]                                           // options_count
p.Options = append(p.Options, p.Options[1], p.Options[1], p.Options[1]) // options_count
p.Options[1].Label = " "                                            // option_label_required
p.Options[1].Label = strings.Repeat("x", 61)                        // option_label_too_long
p.Options[1].Consequence = ""                                       // option_consequence_required
p.Options[1].Consequence = strings.Repeat("x", 201)                 // option_consequence_too_long
p.Options[0].Recommended = false                                    // recommended_count
p.Options[1].Recommended = true                                     // recommended_count
p.Links = make([]DecisionLink, 6)                                   // links_count
p.Links = []DecisionLink{{URL: "https://example.com"}}              // link_invalid
p.Links = []DecisionLink{{Label: "x", URL: "file:///etc/passwd"}}   // link_invalid
p.Reference = strings.Repeat("x", 65)                               // reference_too_long

// TestValidateDecisionRaiseAcceptsTheLimits: no error for a call with
// Title strings.Repeat("界", 120) (120 characters, 360 bytes), a 1000-character context, four options,
// one link {"PR", "https://example.com/pr/1"} and a 64-character reference.

// TestValidateDecisionAnswerNoteAndOutcome: the rule each call returns, "" for no error.
ValidateDecisionAnswer(2, {Choice: "maybe"})                                          // choice_invalid
ValidateDecisionAnswer(2, {Choice: DecisionChoiceOption, Option: 3})                  // option_range
ValidateDecisionAnswer(2, {Choice: DecisionChoiceOption, Option: 0})                  // option_range
ValidateDecisionAnswer(2, {Choice: DecisionChoiceOther})                              // note_required
ValidateDecisionAnswer(2, {Choice: DecisionChoiceOption, Option: 2, Note: <1001 characters>}) // note_too_long
ValidateDecisionAnswer(2, {Choice: DecisionChoiceOption, Option: 2})                  // ""
ValidateDecisionNote(<1001 characters>)                                               // note_too_long
ValidateDecisionOutcome({Text: " "})                                                  // outcome_required
ValidateDecisionOutcome({Text: "a\nb"})                                               // outcome_one_line
ValidateDecisionOutcome({Text: <301 characters>})                                     // outcome_too_long
ValidateDecisionOutcome({Text: "Shipped", Link: "ftp://x"})                           // outcome_link_invalid
ValidateDecisionOutcome({Text: "Shipped in PR 412", Link: "https://example.com/pr/412"}) // ""

// TestDecisionCallURL
DecisionCallURL("dec_1 2") == "ao://decisions/dec_1%202"
```

- [ ] **Step 3: Run the tests and see them fail**

Run: `cd backend && go test ./internal/domain/ -run 'Decision' -v`
Expected: build failure, `undefined: DecisionStamp`.

- [ ] **Step 4: Implement `decision.go`**

Everything in the Produces block and the rule table. Messages use `fmt.Sprintf` with the values the table shows.

- [ ] **Step 5: Run the tests and see them pass**

Run: `cd backend && go test ./internal/domain/ -run 'Decision' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/domain/decision.go backend/internal/domain/decision_test.go
git commit -m "feat(decisions): add ledger entries, stamps and call rules"
```

### Task 2: A call's state from its entries

**Files:**
- Create: `backend/internal/domain/decision_reduce.go`
- Test: `backend/internal/domain/decision_reduce_test.go`

**Interfaces:**
- Consumes: Task 1.
- Produces:

```go
type DecisionCallState string

const (
	DecisionStateNeedsYou   DecisionCallState = "needs_you"
	DecisionStateInProgress DecisionCallState = "in_progress"
	DecisionStateDone       DecisionCallState = "done"
	DecisionStateDismissed  DecisionCallState = "dismissed"
	DecisionStateWithdrawn  DecisionCallState = "withdrawn"
)

// Label is the one name a state has everywhere:
// "Needs you", "In progress", "Done", "Dismissed", "Withdrawn".
func (s DecisionCallState) Label() string

type DecisionDeliveryState string

const (
	DecisionDeliveryNone      DecisionDeliveryState = ""
	DecisionDeliveryPending   DecisionDeliveryState = "pending"
	DecisionDeliverySent      DecisionDeliveryState = "sent"
	DecisionDeliveryNoSession DecisionDeliveryState = "no_session"
)

type DecisionCall struct {
	ID                string                `json:"id"`
	Origin            string                `json:"origin"` // the machine that raised it
	Raised            DecisionRaisedPayload `json:"raised"`
	RaisedAt          time.Time             `json:"raisedAt"`
	State             DecisionCallState     `json:"state"`
	StandingEntryID   string                `json:"standingEntryId,omitempty"`
	Choice            string                `json:"choice,omitempty"`
	Option            int                   `json:"option,omitempty"`
	Note              string                `json:"note,omitempty"`
	AnsweredAt        time.Time             `json:"answeredAt"`
	AnswerSurface     RequestSurface        `json:"answerSurface,omitempty"`
	Outcome           string                `json:"outcome,omitempty"`
	OutcomeLink       string                `json:"outcomeLink,omitempty"`
	OutcomeAt         time.Time             `json:"outcomeAt"`
	Retired           bool                  `json:"retired,omitempty"`
	Delivery          DecisionDeliveryState `json:"delivery,omitempty"`
	DeliveredTo       SessionID             `json:"deliveredTo,omitempty"`
	DeliveredToName   string                `json:"deliveredToName,omitempty"`
	DeliveredFallback bool                  `json:"deliveredFallback,omitempty"`
	Redelivery        bool                  `json:"redelivery,omitempty"`
	ChangedAfterClose bool                  `json:"changedAfterClose,omitempty"`
	LastStamp         DecisionStamp         `json:"lastStamp"`
}

// ReduceDecisionCall computes a call from all of its entries, in any order.
// It returns false when there is no raised entry.
func ReduceDecisionCall(entries []DecisionEntry) (DecisionCall, bool)

// ChosenLabel is the chosen option's label, "Something else" for
// DecisionChoiceOther, and "" when nothing is chosen.
func (c DecisionCall) ChosenLabel() string

// SearchText is the title, context, each option label, reference, note and
// outcome, one per line.
func (c DecisionCall) SearchText() string
```

The rules `ReduceDecisionCall` applies:

1. Sort a copy of the entries with `CompareDecisionEntries`. Skip kinds not named below and entries whose payload does not parse.
2. The first `raised` entry gives `ID`, `Origin`, `Raised` and `RaisedAt`. None: return false.
3. The standing entry is the last `answered` or `dismissed` entry. From an `answered` one take `Choice`, `Option`, `Note`, `AnsweredAt` and `AnswerSurface`. From a `dismissed` one take `Note` only.
4. The last `outcome` entry gives `Outcome`, `OutcomeLink` and `OutcomeAt`.
5. State: an outcome exists, `done`. Otherwise a `withdrawn` entry exists, `withdrawn`. Otherwise the standing entry is `answered`, `in_progress`; is `dismissed`, `dismissed`. Otherwise `needs_you`.
6. The closing entry is the first `outcome` or `withdrawn` entry. `ChangedAfterClose` is true when an `answered` or `dismissed` entry is ordered after it.
7. `Delivery` describes the standing entry. No standing entry: none. A `delivered` entry with `forEntry` equal to its id and status `sent`: sent, and the last such entry gives `DeliveredTo`, `DeliveredToName` and `DeliveredFallback`. Otherwise one with status `no_session`: no session. Otherwise pending.
8. `Redelivery` is true when `Delivery` is pending and a `delivered` entry with status `sent` exists for an earlier `answered` entry.
9. `Retired` is the value in the last `retired` entry.
10. `LastStamp` is the stamp of the last entry of any kind, known or not.

- [ ] **Step 1: Write the failing tests**

```go
// decisionEntry(id, wallMS, kind, payload): an entry for call "dec_1" from origin "h_a", stamped {WallMS: wallMS},
// written by the person from RequestSurfaceDesktop.
raised    := decisionEntry("r", 1, DecisionKindRaised, validDecisionRaise())
pick(id, wall, n)        := decisionEntry(id, wall, DecisionKindAnswered, DecisionAnsweredPayload{Choice: DecisionChoiceOption, Option: n, Note: "n" + id})
sent(id, wall, forEntry) := decisionEntry(id, wall, DecisionKindDelivered, DecisionDeliveredPayload{ForEntry: forEntry, Status: DecisionDeliveredSent, SessionID: "w-1", SessionName: "w-1"})
dismissed := decisionEntry("d", 3, DecisionKindDismissed, DecisionDismissedPayload{Note: "not now"})
outcome   := decisionEntry("o", 6, DecisionKindOutcome, DecisionOutcomePayload{Text: "Shipped in PR 412"})
withdrawn := decisionEntry("w", 2, DecisionKindWithdrawn, struct{}{})

// TestReduceDecisionCall
// For every case, shuffle the entries with six different seeds and assert the same call each time.
[raised]
	State == DecisionStateNeedsYou, Delivery == DecisionDeliveryNone, ID == "dec_1", Origin == "h_a"
[raised, pick("a1", 2, 2)]
	State == DecisionStateInProgress, Option == 2, Note == "na1", StandingEntryID == "a1", Delivery == DecisionDeliveryPending,
	!Redelivery, AnswerSurface == RequestSurfaceDesktop, ChosenLabel() == "Per-tenant API keys"
[raised, pick("a1", 2, 2), sent("s1", 3, "a1")]
	Delivery == DecisionDeliverySent, DeliveredTo == "w-1"
[raised, pick("a1", 2, 2), sent("s1", 3, "a1"), pick("a2", 4, 1)]       // the pick changed after the first was sent
	State == DecisionStateInProgress, Option == 1, StandingEntryID == "a2", Delivery == DecisionDeliveryPending, Redelivery
[raised, dismissed]
	State == DecisionStateDismissed, Note == "not now", Choice == "", Delivery == DecisionDeliveryPending
[raised, dismissed, pick("a1", 4, 1)]                                    // an answer after a dismissal reopens it
	State == DecisionStateInProgress
[raised, pick("a1", 2, 1), outcome]
	State == DecisionStateDone, Outcome == "Shipped in PR 412", !ChangedAfterClose
[raised, pick("a1", 2, 1), outcome, pick("a2", 7, 2)]                    // an answer after the outcome is kept and flagged
	State == DecisionStateDone, ChangedAfterClose, StandingEntryID == "a2", Delivery == DecisionDeliveryPending
[raised, withdrawn]
	State == DecisionStateWithdrawn, !ChangedAfterClose
[raised, withdrawn, pick("a1", 5, 1)]                                    // an answer after a withdrawal is kept and flagged
	State == DecisionStateWithdrawn, ChangedAfterClose, Delivery == DecisionDeliveryPending
[raised, pick("a1", 2, 1), a delivered entry at 3 with {ForEntry: "a1", Status: DecisionDeliveredNoSession}]
	Delivery == DecisionDeliveryNoSession
[raised, pick("a1", 2, 1), outcome, a retired entry at 8 with {Retired: true}, another at 9 with {Retired: false}]
	!Retired, LastStamp.WallMS == 9
[raised, an entry of kind "asked" at 4 with payload {"question": "why"}] // an unknown kind
	State == DecisionStateNeedsYou, LastStamp.WallMS == 4

// TestReduceDecisionCallNeedsARaisedEntry
ReduceDecisionCall([pick("a1", 2, 1)]) returns false

// TestDecisionStateLabelsAndSearchText
// Labels: needs_you "Needs you", in_progress "In progress", done "Done", dismissed "Dismissed", withdrawn "Withdrawn".
c := DecisionCall{Raised: validDecisionRaise() with Reference "SKY-12", Note: "Rotate monthly", Outcome: "Shipped in PR 412"}
c.SearchText() == "Which auth scheme should the proxy use?\nThe connector calls the proxy for each tenant.\nOAuth client credentials\nPer-tenant API keys\nSKY-12\nRotate monthly\nShipped in PR 412"
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `cd backend && go test ./internal/domain/ -run 'ReduceDecision|DecisionStateLabels' -v`
Expected: build failure, `undefined: ReduceDecisionCall`.

- [ ] **Step 3: Implement `decision_reduce.go`** following the ten rules above.

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd backend && go test ./internal/domain/ -run 'Decision' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/domain/decision_reduce.go backend/internal/domain/decision_reduce_test.go
git commit -m "feat(decisions): compute a call's state from its entries"
```

### Task 3: Work streams

**Files:**
- Create: `backend/internal/domain/decision_stream.go`
- Test: `backend/internal/domain/decision_stream_test.go`

**Interfaces:**
- Consumes: `SessionRecord` (`ID`, `ParentSessionID`, `Kind`, `DisplayName`), `KindOrchestrator`; `DecisionCall` from Task 2.
- Produces:

```go
// DecisionStream names one work stream: a manager session on one machine.
type DecisionStream struct {
	Origin    string
	SessionID SessionID
	Name      string // the name on the stream's newest call
}

// DeriveDecisionStream finds the work stream for a call raised by raiser:
// the highest session on raiser's spawn path, below the project's
// orchestrator, that has spawned other sessions. sessions is every session
// in the raiser's project.
func DeriveDecisionStream(raiser SessionID, sessions []SessionRecord) (SessionRecord, bool)

// AssignDecisionStreams returns the calls with their streams as the board
// shows them. A call recorded with no stream joins the stream that is its
// own owning session on the same machine, when one exists. Every call in a
// stream takes that stream's Name.
func AssignDecisionStreams(calls []DecisionCall, streams []DecisionStream) []DecisionCall
```

`DeriveDecisionStream`: walk from the raiser through `ParentSessionID`. Stop at a missing parent, at a session already visited, or before a session whose `Kind` is `KindOrchestrator`. Among the sessions walked, return the last one that is the `ParentSessionID` of some other session. A session whose kind is `KindOrchestrator` is never walked, so the orchestrator's own calls have no stream.

- [ ] **Step 1: Write the failing tests**

```go
// Sessions for the first test. All are KindWorker except "orch".
{ID: "orch", Kind: KindOrchestrator}
{ID: "epic", ParentSessionID: "orch", DisplayName: "Checkout epic"}
{ID: "sub",  ParentSessionID: "epic"}
{ID: "w-1",  ParentSessionID: "sub"}
{ID: "w-2",  ParentSessionID: "epic"}
{ID: "lone", ParentSessionID: "orch"}
{ID: "solo"}
{ID: "boss"}
{ID: "hand", ParentSessionID: "boss"}

// TestDeriveDecisionStream: raiser -> stream, "" for none
"w-2"  -> "epic"   // a worker under a manager
"w-1"  -> "epic"   // a worker two levels down
"sub"  -> "epic"   // a sub-manager belongs to the top manager
"epic" -> "epic"   // a manager's own call
"hand" -> "boss"   // no orchestrator in the project
"boss" -> "boss"
"lone" -> ""       // straight under the orchestrator
"solo" -> ""       // nobody above, nobody below
"orch" -> ""       // the orchestrator itself
"gone" -> ""       // not in the list

// TestDeriveDecisionStreamSurvivesBrokenLineage
// Sessions: "a" (parent "b"), "b" (parent "a"), "self" (parent "self"), "orphan" (parent "missing").
"a" -> "b";  "self" -> "";  "orphan" -> ""       // and every call returns

// TestAssignDecisionStreams, with streams = [{Origin: "h_a", SessionID: "epic", Name: "Checkout epic"}]
// call id, origin, owner, recorded stream, recorded name  ->  stream and name afterwards
"early", "h_a", "epic", "",     ""          ->  "epic", "Checkout epic"   // raised before epic spawned anyone
"old",   "h_a", "w-2",  "epic", "Checkout"  ->  "epic", "Checkout epic"   // named before a rename
"other", "h_b", "epic", "",     ""          ->  "",     ""                // the same session id on another machine
"lone",  "h_a", "lone", "",     ""          ->  "",     ""
// The slice passed in is not changed.
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `cd backend && go test ./internal/domain/ -run 'DecisionStream' -v`
Expected: build failure, `undefined: DeriveDecisionStream`.

- [ ] **Step 3: Implement `decision_stream.go`.**

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd backend && go test ./internal/domain/ -run 'Decision' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/domain/decision_stream.go backend/internal/domain/decision_stream_test.go
git commit -m "feat(decisions): work out a call's work stream from spawn lineage"
```

### Task 4: The log, the read table and the store

**Files:**
- Create: `backend/internal/storage/sqlite/migrations/<id>_decision_ledger.sql` with `npm run migration:create -- decision_ledger`
- Modify: `backend/internal/storage/sqlite/migrate_burned_versions_test.go` (append the new id and filename to `shippedMigrations`)
- Create: `backend/internal/storage/sqlite/queries/decisions.sql`
- Generated: `backend/internal/storage/sqlite/gen/` with `npm run sqlc`
- Create: `backend/internal/storage/sqlite/store/decision_store.go`
- Test: `backend/internal/storage/sqlite/store/decision_store_test.go` (`package store_test`)

**Interfaces:**
- Consumes: Tasks 1 to 3. `(*Store).inTx`, `writeMu` and `qr` as `store/report_store.go` uses them.
- Produces:

```go
// AppendDecisionEntry gives e its origin sequence and order stamp, stores it,
// and recomputes its call's row, in one transaction. now is the wall clock.
// The returned call is zero when the call has no raised entry yet.
func (s *Store) AppendDecisionEntry(ctx context.Context, e domain.DecisionEntry, now time.Time) (domain.DecisionEntry, domain.DecisionCall, error)
func (s *Store) GetDecisionCall(ctx context.Context, callID string) (domain.DecisionCall, bool, error)
// ListDecisionEntries returns a call's entries in CompareDecisionEntries order.
func (s *Store) ListDecisionEntries(ctx context.Context, callID string) ([]domain.DecisionEntry, error)
// ListDecisionCallsByOwner returns a session's calls on one machine, newest first.
func (s *Store) ListDecisionCallsByOwner(ctx context.Context, origin string, owner domain.SessionID, limit int) ([]domain.DecisionCall, error)
// ListDecisionCallsByState returns the calls in one state, oldest first.
func (s *Store) ListDecisionCallsByState(ctx context.Context, state domain.DecisionCallState, limit int) ([]domain.DecisionCall, error)
// ListDecisionCallsAwaitingDelivery returns the calls one machine raised
// whose delivery is pending, oldest first.
func (s *Store) ListDecisionCallsAwaitingDelivery(ctx context.Context, origin string) ([]domain.DecisionCall, error)
// ListDecisionStreams returns every work stream with the name on its newest call.
func (s *Store) ListDecisionStreams(ctx context.Context) ([]domain.DecisionStream, error)
```

`AppendDecisionEntry` returns an error wrapping `domain.ErrDecisionEntryInvalid` (add `var ErrDecisionEntryInvalid = errors.New("invalid decision entry")` to `decision.go`) when `ID`, `Origin`, `CallID`, `Kind` or `AuthorKind` is empty. Inside the transaction it:

1. sets `OriginSeq` to one more than the highest `origin_seq` stored for `e.Origin`;
2. sets `Stamp` to `newest.Next(now.UnixMilli())`, where `newest` is the greatest stamp among all stored entries from every origin, or the zero stamp;
3. inserts the entry, with `Payload` stored as the exact bytes given;
4. loads all of the call's entries, runs `domain.ReduceDecisionCall`, and, when it returns true, writes the call's row.

The migration:

```sql
CREATE TABLE decision_entries (
    id             TEXT      NOT NULL PRIMARY KEY,
    origin         TEXT      NOT NULL,
    origin_seq     INTEGER   NOT NULL,
    stamp_wall     INTEGER   NOT NULL,
    stamp_counter  INTEGER   NOT NULL,
    call_id        TEXT      NOT NULL,
    kind           TEXT      NOT NULL,
    payload        TEXT      NOT NULL,
    written_at     TIMESTAMP NOT NULL,
    author_kind    TEXT      NOT NULL,
    author_surface TEXT      NOT NULL DEFAULT '',
    author_session TEXT      NOT NULL DEFAULT '',
    UNIQUE (origin, origin_seq)
);
CREATE INDEX decision_entries_by_call ON decision_entries (call_id, stamp_wall, stamp_counter);
CREATE INDEX decision_entries_by_stamp ON decision_entries (stamp_wall, stamp_counter);

CREATE TABLE decision_calls (
    call_id           TEXT      NOT NULL PRIMARY KEY,
    origin            TEXT      NOT NULL,
    project_id        TEXT      NOT NULL DEFAULT '',
    stream_session_id TEXT      NOT NULL DEFAULT '',
    stream_name       TEXT      NOT NULL DEFAULT '',
    owner_session_id  TEXT      NOT NULL,
    state             TEXT      NOT NULL,
    delivery          TEXT      NOT NULL DEFAULT '',
    retired           INTEGER   NOT NULL DEFAULT 0,
    raised_at         TIMESTAMP NOT NULL,
    last_wall         INTEGER   NOT NULL,
    last_counter      INTEGER   NOT NULL,
    body              TEXT      NOT NULL,
    search_text       TEXT      NOT NULL DEFAULT ''
);
CREATE INDEX decision_calls_by_state ON decision_calls (state, raised_at);
CREATE INDEX decision_calls_by_owner ON decision_calls (origin, owner_session_id, raised_at);
CREATE INDEX decision_calls_by_delivery ON decision_calls (origin, delivery);

CREATE VIRTUAL TABLE decision_calls_fts USING fts5(
    search_text,
    content = 'decision_calls',
    content_rowid = 'rowid',
    tokenize = 'porter unicode61'
);
```

Add the three triggers that keep `decision_calls_fts` in step with `decision_calls` (`decision_calls_fts_insert`, `_update`, `_delete`). Copy their shape from `20261024194546592_conversation_messages_search.sql`, on the `search_text` column and without that file's `streaming` condition. Wrap each trigger in its own goose statement block as that file does. The `Down` section drops the triggers, the virtual table and both tables.

There are deliberately no foreign keys to `sessions` or `projects`: after slice 4 an entry can name a session on another machine. Head the migration with a comment that says so, and that `decision_entries` is append-only and `decision_calls` is rebuilt from it.

`body` is the JSON of `domain.DecisionCall`. `search_text` is `call.SearchText()`. The other columns copy the fields their names say.

- [ ] **Step 1: Create the migration and register it**

Run: `npm run migration:create -- decision_ledger`, write the SQL above into the generated file, and append its id and filename to `shippedMigrations`.

Run: `cd backend && go test ./internal/storage/sqlite/ -run 'Migrat' -v`
Expected: PASS.

- [ ] **Step 2: Write the failing store tests**

```go
// decisionRaised(entryID, origin, callID, owner, stream, streamName): a raised entry authored by the owner
// session, carrying validDecisionRaise's title, context and options plus ProjectID "shop", ProjectName "Shop",
// MachineLabel "mac-mini" and the owner and stream given.
// decisionAnswered(entryID, origin, callID, option): an answered entry by the person from RequestSurfaceCLI.
at := time.UnixMilli(1_000_000).UTC()

// TestAppendDecisionEntryAssignsSequenceAndStamp
first  := append decisionRaised("dent_1", "h_a", "dec_1", "w-1", "", "") with now = at
first.OriginSeq == 1,  first.Stamp == DecisionStamp{WallMS: 1_000_000}
second := append decisionAnswered("dent_2", "h_a", "dec_1", 1) with now = at minus one minute   // the clock moved back
second.OriginSeq == 2, second.Stamp == DecisionStamp{WallMS: 1_000_000, Counter: 1}
other  := append decisionRaised("dent_3", "h_b", "dec_2", "w-9", "", "") with now = at plus one second
other.OriginSeq == 1,  other.Stamp == DecisionStamp{WallMS: 1_001_000}

// TestAppendDecisionEntryRecomputesTheCall
// After the raised entry the returned call has State needs_you and RaisedAt equal to the entry's WrittenAt.
// After decisionAnswered(..., option 2) the returned call has State in_progress and Option 2.
// GetDecisionCall("dec_1") deep-equals that returned call. GetDecisionCall("dec_missing") returns false.

// TestDecisionEntriesKeepUnknownKindsByteForByte
// Append a raised entry, then an entry of kind "asked" whose Payload is `{"question":"why", "future" : [1,2]}`.
// The returned call is still in needs_you. ListDecisionEntries("dec_1") has 2 entries; the second has
// Kind "asked" and string(Payload) equal to the bytes given, spaces included.

// TestAppendDecisionEntryRefusesAnIncompleteEntry
append DecisionEntry{ID: "dent_1", CallID: "dec_1", Kind: DecisionKindRaised}  ->  errors.Is(err, domain.ErrDecisionEntryInvalid)

// TestDecisionCallLists. Appended in this order, one second apart:
decisionRaised("dent_1", "h_a", "dec_1", "w-1", "epic", "Checkout")
decisionRaised("dent_2", "h_a", "dec_2", "w-1", "epic", "Checkout epic")
decisionRaised("dent_3", "h_a", "dec_3", "w-2", "", "")
decisionRaised("dent_4", "h_b", "dec_4", "w-1", "", "")
decisionAnswered("dent_5", "h_a", "dec_1", 1)
decisionAnswered("dent_6", "h_a", "dec_4", 1)
ids of ListDecisionCallsByOwner("h_a", "w-1", 50)               == "dec_2,dec_1"   // newest first; the other machine's w-1 is left out
ids of ListDecisionCallsByState(DecisionStateNeedsYou, 200)     == "dec_2,dec_3"   // oldest first
ids of ListDecisionCallsAwaitingDelivery("h_a")                 == "dec_1"         // dec_4 was raised by h_b
ListDecisionStreams() == []DecisionStream{{Origin: "h_a", SessionID: "epic", Name: "Checkout epic"}}
```

- [ ] **Step 3: Run the tests and see them fail**

Run: `cd backend && go test ./internal/storage/sqlite/store/ -run 'Decision' -v`
Expected: build failure, `s.AppendDecisionEntry undefined`.

- [ ] **Step 4: Write `queries/decisions.sql`, run `npm run sqlc`, and implement `decision_store.go`**

Write the queries the seven methods need, in the annotation style of `queries/reports.sql`. Take `writeMu` and use `inTx` for the append, as `CreateReport` does. Reads use `s.qr`.

- [ ] **Step 5: Run the tests and see them pass**

Run: `cd backend && go test ./internal/storage/sqlite/... -run 'Decision|Migrat' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/storage/sqlite backend/internal/domain/decision.go
git commit -m "feat(decisions): store the entry log and its read table"
```

### Task 5: Search

**Files:**
- Create: `backend/internal/storage/sqlite/store/decision_search.go`
- Test: `backend/internal/storage/sqlite/store/decision_search_test.go`

**Interfaces:**
- Consumes: `ftsWords` in `store/search_store.go`, `domain.SearchTerms`, the `decision_calls_fts` table from Task 4.
- Produces:

```go
// SearchDecisionCalls returns the calls whose text matches query, best match
// first. Every word must match, by word form, and the last also as a prefix;
// when nothing matches and there are several words, any word may match.
func (s *Store) SearchDecisionCalls(ctx context.Context, query string, limit int) ([]domain.DecisionCall, error)
```

The SQL is hand-written and run on `s.readDB`, because sqlc cannot parse FTS5 (see the comment on `searchConversationMessagesSQL`). Build the match expression exactly as `SearchConversationMessages` does: `ftsWords(strings.Join(domain.SearchTerms(query), " "))`, each word in double quotes, `*` after the last, joined by spaces, then by ` OR ` for the fallback. A query with no words returns `nil, nil`. Order by the FTS rank.

- [ ] **Step 1: Write the failing tests**

```go
// Seed two calls from origin "h_a", owner "w-1", with context "Tenants rotate their secrets." and the options
// "Vault" (recommended) and "Environment files":
//   "dec_auth":    title "Which auth scheme should the proxy use?", reference "SKY-12"
//   "dec_cadence": title "How often should the report refresh?", then answered {Choice: other,
//                  Note: "Hourly, except weekends"}, then an outcome "Shipped the scheduler change"

// TestSearchDecisionCalls: query -> the ids found, sorted
"auth scheme"        -> "dec_auth"                // the title
"SKY-12"             -> "dec_auth"                // the reference
"vault"              -> "dec_auth,dec_cadence"    // an option label
"weekends"           -> "dec_cadence"             // the person's note
"scheduler"          -> "dec_cadence"             // the outcome
"refreshing reports" -> "dec_cadence"             // word forms
"sched"              -> "dec_cadence"             // the last word as a prefix
"auth zebra"         -> "dec_auth"                // falls back to any word
"zebra"              -> ""

// TestSearchDecisionCallsTreatsPunctuationAsText
// No error for: `"auth" OR -proxy* (scheme)`, `NEAR(auth, 2)`, `auth:scheme^`, "🙂 auth", `"`
// nil, nil for: "", "   ", `"-*()`
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `cd backend && go test ./internal/storage/sqlite/store/ -run 'SearchDecision' -v`
Expected: build failure, `s.SearchDecisionCalls undefined`.

- [ ] **Step 3: Implement `decision_search.go`.**

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd backend && go test ./internal/storage/sqlite/store/ -run 'Decision' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/storage/sqlite/store/decision_search.go backend/internal/storage/sqlite/store/decision_search_test.go
git commit -m "feat(decisions): search calls by their text"
```

---

# Part B: raising and reading from a terminal

### Task 6: Raising and reading in the service

**Files:**
- Create: `backend/internal/service/decision/service.go`
- Test: `backend/internal/service/decision/service_test.go` (`package decision`, with `sqlitetest.MustOpen`)

**Interfaces:**
- Consumes: the store methods of Tasks 4 and 5; `(*Store).GetSession`, `ListSessions`, `GetProject`; `apierr`.
- Produces:

```go
package decision

type Store interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	AppendDecisionEntry(context.Context, domain.DecisionEntry, time.Time) (domain.DecisionEntry, domain.DecisionCall, error)
	GetDecisionCall(context.Context, string) (domain.DecisionCall, bool, error)
	ListDecisionEntries(context.Context, string) ([]domain.DecisionEntry, error)
	ListDecisionCallsByOwner(context.Context, string, domain.SessionID, int) ([]domain.DecisionCall, error)
	ListDecisionCallsByState(context.Context, domain.DecisionCallState, int) ([]domain.DecisionCall, error)
	ListDecisionStreams(context.Context) ([]domain.DecisionStream, error)
	SearchDecisionCalls(context.Context, string, int) ([]domain.DecisionCall, error)
}

type Deps struct {
	Store        Store
	HostID       string                        // this machine's host id
	MachineLabel func() string                 // default: os.Hostname, else HostID
	Now          func() time.Time              // default: time.Now
	NewID        func(prefix string) string    // default: prefix + uuid.NewString()
	OnAppended   func(domain.DecisionEntry)    // optional; called after every entry the service writes
}

func New(d Deps) *Service

// Actor is who is acting. Session is set when a session issued the command.
// Otherwise the person is acting, from Surface.
type Actor struct {
	Session domain.SessionID
	Surface domain.RequestSurface
}

type RaiseInput struct {
	Title, Context string
	Options        []domain.DecisionOption
	Links          []domain.DecisionLink
	Reference      string
}

type Scope string

const (
	ScopeMine     Scope = "mine"      // the acting session's own calls, newest first, at most 50
	ScopeNeedsYou Scope = "needs_you" // every call in Needs you, oldest first, at most 200
)

type Detail struct {
	Call    domain.DecisionCall
	Entries []domain.DecisionEntry
}

func (s *Service) Raise(ctx context.Context, actor Actor, in RaiseInput) (domain.DecisionCall, error)
func (s *Service) Get(ctx context.Context, callID string) (Detail, error)
func (s *Service) List(ctx context.Context, actor Actor, scope Scope) ([]domain.DecisionCall, error)
// Search returns at most 20 calls: those in the acting session's project
// first, then those that still apply before retired ones, otherwise in the
// store's order.
func (s *Service) Search(ctx context.Context, actor Actor, query string) ([]domain.DecisionCall, error)
```

Errors are `*apierr.Error`:

| Code | Constructor | When | Message |
| --- | --- | --- | --- |
| `DECISION_SESSION_REQUIRED` | `apierr.Invalid` | `Raise`, or `List` with `ScopeMine`, with no acting session | `This needs a session. Run it from inside a session.` |
| `DECISION_HOST_UNKNOWN` | `apierr.Unavailable` | `Raise` when `HostID` is empty | `This machine has no identity yet, so it cannot record decisions.` |
| `SESSION_NOT_FOUND` | `apierr.NotFound` | the acting session is unknown | `Unknown session` |
| `DECISION_INVALID` | `apierr.Invalid` | a rule is broken; `Details` is `{"rule": <rule>}` | the rule's message |
| `DECISION_NOT_FOUND` | `apierr.NotFound` | `Get` with an unknown id | `Unknown call` |
| `DECISION_QUERY_REQUIRED` | `apierr.Invalid` | `Search` with a blank query | `Say what to search for.` |
| `DECISION_SCOPE_INVALID` | `apierr.Invalid` | `List` with any other scope | `Unknown scope.` |

`Raise` fills in what the session does not write: `ProjectID` from the session; `ProjectName` from the project's `DisplayName`, else its id; the stream from `domain.DeriveDecisionStream(actor.Session, sessions)`, where `sessions` is `ListSessions` for that project; `MachineLabel`; and the owner's and the stream's names from `DisplayName`, else the id. A session with no project raises a call with no project and no stream. The entry is authored by the session, with `Origin` set to `HostID`.

`Get`, `List` and `Search` pass their calls through `domain.AssignDecisionStreams` with `ListDecisionStreams` before returning.

- [ ] **Step 1: Write the failing tests**

```go
// newDecisionService(t): a real store from sqlitetest.MustOpen, seeded with UpsertProject and CreateSession the
// way store/store_test.go seeds them:
//   project "shop" with DisplayName "Shop"
//   sessions "orch" (KindOrchestrator, in shop); "epic" (parent "orch", DisplayName "Checkout epic");
//            "w-1" and "w-2" (parent "epic"); "loose" (no project, no parent)
// and a Service with HostID "h_a", MachineLabel returning "mac-mini", a clock that starts at
// time.UnixMilli(1_000_000) and adds one second on every call, NewID returning prefix+"1", prefix+"2" and so on,
// and OnAppended collecting entries. It returns the service, the store and the collected entries.
// validRaiseInput(): the title, context and two options of validDecisionRaise.
// apiCode(err): the Code of the *apierr.Error in err, or "".

// TestRaiseRecordsWhoWhereAndWhichStream
call, err := svc.Raise(ctx, Actor{Session: "w-1"}, validRaiseInput())
call.ID == "dec_1", call.Origin == "h_a", call.State == domain.DecisionStateNeedsYou
call.Raised: ProjectID "shop", ProjectName "Shop", MachineLabel "mac-mini", OwnerSessionID "w-1",
             OwnerSessionName "w-1", StreamSessionID "epic", StreamName "Checkout epic"
// One entry was collected: ID "dent_2", Kind raised, AuthorKind session, AuthorSession "w-1".

// TestRaiseFromASessionWithNoProject
svc.Raise(ctx, Actor{Session: "loose"}, validRaiseInput()): no error, ProjectID == "", StreamSessionID == "", OwnerSessionID == "loose"

// TestRaiseRefusals
Raise as Actor{Surface: domain.RequestSurfaceCLI}      -> DECISION_SESSION_REQUIRED
Raise as Actor{Session: "ghost"}                       -> SESSION_NOT_FOUND
Raise as "w-1" with no option recommended              -> DECISION_INVALID, Details["rule"] == "recommended_count",
                                                          Message == "Mark exactly one option as recommended; this call marks 0."
Raise as "w-1" on a Service built with no HostID       -> DECISION_HOST_UNKNOWN

// TestListAndGet
mine := raise as "w-1";  theirs := raise as "w-2";  early := raise as "epic"
List(Actor{Session: "w-1"}, ScopeMine)                          == [mine]
List(Actor{Surface: domain.RequestSurfaceCLI}, ScopeNeedsYou)   == [mine, theirs, early]   // oldest first
List(Actor{Surface: domain.RequestSurfaceCLI}, ScopeMine)       -> DECISION_SESSION_REQUIRED
List(Actor{Session: "w-1"}, Scope("everything"))                -> DECISION_SCOPE_INVALID
Get(early.ID): one entry; Call.Raised.StreamSessionID == "epic", Call.Raised.StreamName == "Checkout epic"
Get("dec_missing")                                              -> DECISION_NOT_FOUND

// TestSearchPutsTheSameProjectFirst
elsewhere := raise as "loose";  here := raise as "w-2"
Search(Actor{Session: "w-1"}, "auth scheme") == [here, elsewhere]
Search(Actor{Session: "w-1"}, "  ")          -> DECISION_QUERY_REQUIRED
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `cd backend && go test ./internal/service/decision/ -v`
Expected: build failure, `undefined: New`.

- [ ] **Step 3: Implement `service.go`.**

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd backend && go test ./internal/service/decision/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/decision
git commit -m "feat(decisions): raise, list, search and show calls"
```

### Task 7: Routes for raising and reading

**Files:**
- Create: `backend/internal/httpd/controllers/decisions.go`
- Test: `backend/internal/httpd/controllers/decisions_test.go` (`package controllers_test`)
- Modify: `backend/internal/httpd/controllers/dto.go` (new types)
- Modify: `backend/internal/httpd/apispec/specgen/build.go` (tag near line 81, `schemaNames` near line 571, `decisionOperations()` and its `append` near line 825)
- Modify: `backend/internal/httpd/api.go` (`APIDeps.Decisions` near line 56, the controller field near line 164, its construction near line 230, `a.decisions.Register(r)` near line 376)
- Modify: `backend/internal/daemon/daemon.go` (construct the service after `mobilebridge.EnsureLocalIdentity` at line 1110; pass it in `APIDeps` near line 1219)
- Generated: `backend/internal/httpd/apispec/openapi.yaml`, `frontend/src/api/schema.ts` with `npm run api`

**Interfaces:**
- Consumes: `decisionsvc.Service` from Task 6; `caller.From`; `telemetrymeta.SurfaceFromContext`; `envelope`.
- Produces: routes `POST /api/v1/decisions`, `GET /api/v1/decisions`, `GET /api/v1/decisions/{id}` with operation ids `raiseDecision`, `listDecisions`, `getDecision` under the tag `decisions`; and:

```go
// DecisionService is what the decisions routes need. Task 10 adds to it.
type DecisionService interface {
	Raise(context.Context, decisionsvc.Actor, decisionsvc.RaiseInput) (domain.DecisionCall, error)
	Get(context.Context, string) (decisionsvc.Detail, error)
	List(context.Context, decisionsvc.Actor, decisionsvc.Scope) ([]domain.DecisionCall, error)
	Search(context.Context, decisionsvc.Actor, string) ([]domain.DecisionCall, error)
}

type DecisionsController struct{ Svc DecisionService }

func (c *DecisionsController) Register(r chi.Router)
```

New types in `dto.go`. Each needs a `schemaNames` entry of the form `"ControllersRaiseDecisionRequest": "RaiseDecisionRequest"`.

```go
type DecisionOptionPayload struct {
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
	Recommended bool   `json:"recommended,omitempty"`
}

type DecisionLinkPayload struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type RaiseDecisionRequest struct {
	Title     string                  `json:"title"`
	Context   string                  `json:"context"`
	Options   []DecisionOptionPayload `json:"options"`
	Links     []DecisionLinkPayload   `json:"links,omitempty"`
	Reference string                  `json:"reference,omitempty"`
}

type DecisionCallResponse struct {
	ID                string                  `json:"id"`
	Link              string                  `json:"link"`
	State             string                  `json:"state" enum:"needs_you,in_progress,done,dismissed,withdrawn"`
	StateLabel        string                  `json:"stateLabel"`
	Title             string                  `json:"title"`
	Context           string                  `json:"context"`
	Options           []DecisionOptionPayload `json:"options"`
	Links             []DecisionLinkPayload   `json:"links"`
	Reference         string                  `json:"reference,omitempty"`
	ProjectID         string                  `json:"projectId,omitempty"`
	ProjectName       string                  `json:"projectName,omitempty"`
	StreamSessionID   string                  `json:"streamSessionId,omitempty"`
	StreamName        string                  `json:"streamName,omitempty"`
	HostID            string                  `json:"hostId"`
	MachineLabel      string                  `json:"machineLabel"`
	OwnerSessionID    string                  `json:"ownerSessionId"`
	OwnerSessionName  string                  `json:"ownerSessionName"`
	RaisedAt          time.Time               `json:"raisedAt"`
	Choice            string                  `json:"choice,omitempty" enum:"option,other"`
	Option            int                     `json:"option,omitempty"`
	ChosenLabel       string                  `json:"chosenLabel,omitempty"`
	Note              string                  `json:"note,omitempty"`
	AnsweredAt        *time.Time              `json:"answeredAt,omitempty"`
	AnswerSurface     string                  `json:"answerSurface,omitempty"`
	Outcome           string                  `json:"outcome,omitempty"`
	OutcomeLink       string                  `json:"outcomeLink,omitempty"`
	OutcomeAt         *time.Time              `json:"outcomeAt,omitempty"`
	Retired           bool                    `json:"retired"`
	Delivery          string                  `json:"delivery,omitempty" enum:"pending,sent,no_session"`
	DeliveredTo       string                  `json:"deliveredTo,omitempty"`
	DeliveredToName   string                  `json:"deliveredToName,omitempty"`
	DeliveredFallback bool                    `json:"deliveredFallback,omitempty"`
	ChangedAfterClose bool                    `json:"changedAfterClose,omitempty"`
}

type ListDecisionsQuery struct {
	Scope string `query:"scope,omitempty" enum:"mine,needs_you" description:"Which calls to list. Defaults to needs_you. Ignored when q is given."`
	Q     string `query:"q,omitempty" description:"Search words. When given, the response is the search result."`
}

type ListDecisionsResponse struct {
	Calls []DecisionCallResponse `json:"calls"`
}

type DecisionIDParam struct {
	ID string `path:"id" description:"Call identifier."`
}

// DecisionEntryResponse is one line of a call's history. Only the fields
// that its kind uses are set.
type DecisionEntryResponse struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	WrittenAt     time.Time `json:"writtenAt"`
	HostID        string    `json:"hostId"`
	AuthorKind    string    `json:"authorKind" enum:"person,session,system"`
	AuthorSurface string    `json:"authorSurface,omitempty"`
	AuthorSession string    `json:"authorSession,omitempty"`
	Choice        string    `json:"choice,omitempty"`
	Option        int       `json:"option,omitempty"`
	Note          string    `json:"note,omitempty"`
	Text          string    `json:"text,omitempty"`
	Link          string    `json:"link,omitempty"`
	Status        string    `json:"status,omitempty"`
	SessionID     string    `json:"sessionId,omitempty"`
	SessionName   string    `json:"sessionName,omitempty"`
	Fallback      bool      `json:"fallback,omitempty"`
	Retired       *bool     `json:"retired,omitempty"`
}

type DecisionDetailResponse struct {
	Call    DecisionCallResponse    `json:"call"`
	History []DecisionEntryResponse `json:"history"`
}
```

Who is acting, for every decisions route:

```go
func decisionActor(r *http.Request) decisionsvc.Actor {
	if session := caller.From(r.Context()); session != "" {
		return decisionsvc.Actor{Session: domain.SessionID(session)}
	}
	surface, _ := telemetrymeta.SurfaceFromContext(r.Context())
	return decisionsvc.Actor{Surface: surface}
}
```

`raise` decodes with `DisallowUnknownFields` and answers 201. `list` calls `Search` when `q` is not blank, otherwise `List` with `scope` defaulting to `needs_you`. `Link` is `domain.DecisionCallURL(call.ID)`, `StateLabel` is `call.State.Label()`, `HostID` is `call.Origin`. `Links` and `Options` are never null. A nil `Svc` answers `apispec.NotImplemented`, as `ReportsController` does. Errors go through `envelope.WriteErrorWithCode` with the fallback codes `DECISION_RAISE_FAILED`, `DECISION_LIST_FAILED` and `DECISION_GET_FAILED`.

In `daemon.go`, after the identity is known:

```go
decisionSvc := decisionsvc.New(decisionsvc.Deps{Store: store, HostID: hostIdentity.HostID})
```

Task 12 adds `OnAppended` to it.

- [ ] **Step 1: Write the failing tests**

```go
// decisionRequest(t, srv, method, path, body, callerSession): like doRequest in projects_test.go, and it sets
// the caller.Header header when callerSession is not empty. It returns the body and the status.
// fakeDecisionService records the actor and the input of each call. It returns sampleDecisionCall(): id "dec_1",
// origin "h_a", needs you, the two options of earlier tasks, raised at 2026-11-16T15:00:00Z. Get also returns
// one raised entry authored by session "w-1".
// newDecisionServer(t, svc): an httptest server over
// httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Decisions: svc}, httpd.ControlDeps{}).
raiseBody := `{"title":"Which auth scheme should the proxy use?","context":"The connector calls the proxy for each tenant.","options":[{"label":"OAuth client credentials","consequence":"One app registration per tenant.","recommended":true},{"label":"Per-tenant API keys","consequence":"Customers rotate keys by hand."}]}`

// TestDecisionsAPI_RaiseTakesTheSessionFromTheCallerHeader
POST /api/v1/decisions with raiseBody, caller "w-1"  ->  201
svc.actor == decisionsvc.Actor{Session: "w-1"}; svc.raise has the title sent and 2 options, the first recommended
// The response has id "dec_1", link "ao://decisions/dec_1", state "needs_you", stateLabel "Needs you",
// hostId "h_a", and links is [] and not null.

// TestDecisionsAPI_WithoutACallerThePersonIsActing
GET /api/v1/decisions, no caller  ->  200
svc.actor == decisionsvc.Actor{Surface: domain.RequestSurfaceCLI}; svc.scope == decisionsvc.ScopeNeedsYou

// TestDecisionsAPI_ListSearchAndGet
GET /api/v1/decisions?scope=mine, caller "w-1"                ->  200; svc.scope == decisionsvc.ScopeMine
GET /api/v1/decisions?q=auth+scheme&scope=mine, caller "w-1"  ->  200; svc.query == "auth scheme"
GET /api/v1/decisions/dec_1                                   ->  200; svc.id == "dec_1"; call.id "dec_1"; history has one line
                                                                  with kind "raised", authorKind "session", authorSession "w-1"

// TestDecisionsAPI_Errors
// The service returns apierr.Invalid("DECISION_INVALID", "Mark exactly one option as recommended; this call marks 0.",
// map[string]any{"rule": "recommended_count"}).
POST raiseBody                      ->  400; the body contains "DECISION_INVALID" and "Mark exactly one option as recommended"
POST `{"title":"x","surprise":1}`   ->  400; the body contains "INVALID_JSON"
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `cd backend && go test ./internal/httpd/controllers/ -run 'DecisionsAPI' -v`
Expected: build failure, `unknown field Decisions in struct literal of type httpd.APIDeps`.

- [ ] **Step 3: Implement the DTOs, the controller, the spec entries and the wiring**

- [ ] **Step 4: Regenerate the contract**

Run: `npm run api`
Expected: `openapi.yaml` and `frontend/src/api/schema.ts` change, with the three new operations.

- [ ] **Step 5: Run the tests and see them pass**

Run: `cd backend && go build ./... && go test ./internal/httpd/... ./internal/daemon/... -v 2>&1 | tail -30`
Expected: PASS, including `TestRouteSpecParity`.

Run: `npm run frontend:typecheck`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/httpd backend/internal/daemon/daemon.go frontend/src/api/schema.ts
git commit -m "feat(decisions): add routes to raise, list, search and show calls"
```

### Task 8: `ao decision raise`, `search`, `ls` and `show`

**Files:**
- Create: `backend/internal/cli/decision.go`
- Test: `backend/internal/cli/decision_test.go` (`package cli`)
- Modify: `backend/internal/cli/root.go:238` (add `root.AddCommand(newDecisionCommand(ctx))` beside the report command)

**Interfaces:**
- Consumes: the routes of Task 7; `c.getJSON`, `c.postJSON`, `writeJSON`, `usageError`. The caller header is already sent by `cli/client.go`.
- Produces: `func newDecisionCommand(ctx *commandContext) *cobra.Command`, and hand-mirrored types `decisionCallAPI`, `decisionEntryAPI`, `decisionRaiseAPIRequest`, `decisionOptionAPI` and `decisionLinkAPI` with the JSON names of Task 7's DTOs.

The commands:

| Command | Request | Prints |
| --- | --- | --- |
| `ao decision raise --title <t> --context <c> --option "<label> :: <what it leads to>" (2 to 4 times) --recommend <n> [--link "<label>=<url>"]... [--ref <id>]` | `POST decisions` | `Raised <id>`, then the link on its own line |
| `ao decision raise --file <path>` or `--file -` for standard input | `POST decisions` with the file's JSON, which has the shape of `RaiseDecisionRequest` | the same |
| `ao decision search <words...>` | `GET decisions?q=<words joined by spaces>` | one block per call, or `No decisions match.` |
| `ao decision ls [--needs-you]` | `GET decisions?scope=mine`, or `scope=needs_you` with the flag or when `callerSession()` in `cli/client.go` returns nothing | one block per call, or `No calls.` |
| `ao decision show <id>` | `GET decisions/<id>` | every field, then `History:` and one line per entry |

`raise`, `search`, `ls` and `show` take `--json`, which prints the response with `writeJSON` and nothing else.

A block is two or more lines:

```
dec_1  Needs you  Which auth scheme should the proxy use?
  Shop · Checkout epic · mac-mini · 2026-11-16
  Chose: OAuth client credentials · Note: Rotate monthly
  Outcome: Shipped in PR 412
  No longer applies
```

The second line joins the project name, the stream name, the machine label and the raised date, leaving out any that is empty. The third, fourth and fifth lines appear only when there is an answer, an outcome, or the call is retired. In `ls` without `--needs-you`, a call that is in progress also gets the line `  Outcome owed: ao decision outcome <id> --text "<what happened>"`.

Usage errors, all exit 2, all before any request:

| Misuse | Message |
| --- | --- |
| `--file` with any of `--title`, `--context`, `--option`, `--recommend`, `--link`, `--ref` | `usage: --file cannot be combined with the other call flags` |
| an `--option` with no `::` | `usage: write each --option as "<label> :: <what it leads to>"` |
| `--recommend` below 1 or above the number of options | `usage: --recommend takes the position of one of the options` |
| a `--link` with no `=` | `usage: write each --link as "<label>=<url>"` |
| `search` with no words | `usage: say what to search for` |
| `show` without exactly one id | `usage: ao decision show <id>` |

Every other rule is checked by the daemon, so the message that names it is the same from the CLI, the app and the phone. Split an option on the first `::` and trim both parts. Split a link on the first `=`.

- [ ] **Step 1: Write the failing tests**

```go
// decisionCLIServer(t, handler): the stand-in daemon. Lift the setup TestReportModesAndBoundaries has inline
// (setConfigEnv, an httptest server, the run file pointing at it) into this helper.

// TestDecisionRaiseUsage: each exits 2 before any request, and the error contains the text.
opt1, opt2 := `OAuth :: One registration per tenant`, `API keys :: Rotate by hand`
decision raise --file call.json --title x                                                       ->  "--file cannot be combined"
decision raise --title x --context y --option OAuth --option opt2 --recommend 1                 ->  "write each --option as"
decision raise --title x --context y --option opt1 --option opt2 --recommend 3                  ->  "--recommend takes the position"
decision raise --title x --context y --option opt1 --option opt2 --recommend 0                  ->  "--recommend takes the position"
decision raise --title x --context y --option opt1 --option opt2 --recommend 1 --link PR        ->  "write each --link as"
decision search                                                                                 ->  "say what to search for"
decision show                                                                                   ->  "ao decision show <id>"

// TestDecisionRaiseSendsTheCallAndPrintsItsLink, with AO_SESSION_ID=w-1. The server answers 201 with
// {"id":"dec_1","link":"ao://decisions/dec_1","state":"needs_you","stateLabel":"Needs you"}.
decision raise --title "Which auth scheme should the proxy use?" --context "The connector calls the proxy for each tenant."
    --option "OAuth client credentials :: One app registration per tenant :: no keys to rotate"
    --option " Per-tenant API keys::Customers rotate keys by hand " --recommend 1
    --link "Design=https://example.com/d?x=1" --ref SKY-12
stdout == "Raised dec_1\nao://decisions/dec_1\n"
// The request went to POST /api/v1/decisions with the header X-AO-Caller-Session: w-1, and its body decodes to:
//   the title and context given; reference "SKY-12"; links [{"Design", "https://example.com/d?x=1"}];
//   options [{"OAuth client credentials", "One app registration per tenant :: no keys to rotate", recommended},
//            {"Per-tenant API keys", "Customers rotate keys by hand"}]

// TestDecisionRaiseFromAFileAndStandardInput
doc := `{"title":"T","context":"C","options":[{"label":"A","consequence":"a","recommended":true},{"label":"B","consequence":"b"}]}`
decision raise --file <a file holding doc>                       ->  the request body has title "T" and 2 options, the first recommended
decision raise --file -  with Deps{In: strings.NewReader(doc)}   ->  the same

// TestDecisionRaiseShowsTheRuleTheDaemonNames
// The server answers 400 with an envelope.APIError: code "DECISION_INVALID",
// message "Mark exactly one option as recommended; this call marks 2."
decision raise --file -  with Deps{In: strings.NewReader(`{}`)}  ->  exit 1; the error contains that message

// TestDecisionListSearchAndShow. The server answers every list with this one call:
call := `{"id":"dec_1","link":"ao://decisions/dec_1","state":"in_progress","stateLabel":"In progress","title":"Which auth scheme should the proxy use?","projectName":"Shop","streamName":"Checkout epic","machineLabel":"mac-mini","raisedAt":"2026-11-16T15:00:00Z","choice":"option","option":1,"chosenLabel":"OAuth client credentials","note":"Rotate monthly"}`
// and GET /api/v1/decisions/dec_1 with that call and a history of two lines: raised by session "w-1", then
// answered by the person from "desktop" at 2026-11-16T15:05:00Z.
// With AO_SESSION_ID=w-1:
decision ls              ->  stdout contains "dec_1  In progress  Which auth scheme should the proxy use?",
                             "Shop · Checkout epic · mac-mini · 2026-11-16", "Chose: OAuth client credentials · Note: Rotate monthly"
                             and `Outcome owed: ao decision outcome dec_1 --text "<what happened>"`
decision ls --needs-you  ->  stdout does not contain "Outcome owed"
decision search auth scheme  ->  no error
decision show dec_1      ->  stdout contains "ao://decisions/dec_1", "History:", "raised", "answered", "person (desktop)"
decision ls --json       ->  stdout is valid JSON and contains "dec_1"
// With AO_SESSION_ID empty:
decision ls              ->  no error
// The six requests, in order: /api/v1/decisions?scope=mine, ?scope=needs_you, ?q=auth+scheme,
// /api/v1/decisions/dec_1, ?scope=mine, ?scope=needs_you.
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `cd backend && go test ./internal/cli/ -run 'Decision' -v`
Expected: build failure, `undefined: decisionRaiseAPIRequest`.

- [ ] **Step 3: Implement `decision.go` and register the command in `root.go`.**

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd backend && go test ./internal/cli/ -run 'Decision' -v`
Expected: PASS.

- [ ] **Step 5: Try it end to end**

Run the branch in the desktop lab that `AGENTS.md` describes ("Desktop lab"): an isolated worktree with `AO_DATA_DIR` pointing at scratch data, never the real `~/.ao`. Start a throwaway session, and from that session's shell run `ao decision raise` with two options, then `ao decision ls`, `ao decision search <a word from the title>` and `ao decision show <id>`.
Expected: the call is listed as `Needs you` with its project and machine, and `show` prints one history line.

- [ ] **Step 6: Commit, then open PR 1**

```bash
git add backend/internal/cli
git commit -m "feat(decisions): add ao decision raise, search, ls and show"
```

Before opening the pull request, run the checks under "Before each pull request".

---

# Part C: answering and carrying out

### Task 9: The person's actions and the session's follow-up

**Files:**
- Modify: `backend/internal/service/decision/service.go`
- Test: `backend/internal/service/decision/service_actions_test.go`

**Interfaces:**
- Consumes: Task 6.
- Produces:

```go
type AnswerInput struct {
	Choice string // domain.DecisionChoiceOption or domain.DecisionChoiceOther
	Option int
	Note   string
}

type OutcomeInput struct{ Text, Link string }

func (s *Service) Answer(ctx context.Context, actor Actor, callID string, in AnswerInput) (domain.DecisionCall, error)
func (s *Service) Dismiss(ctx context.Context, actor Actor, callID, note string) (domain.DecisionCall, error)
func (s *Service) RecordOutcome(ctx context.Context, actor Actor, callID string, in OutcomeInput) (domain.DecisionCall, error)
func (s *Service) Withdraw(ctx context.Context, actor Actor, callID string) (domain.DecisionCall, error)
func (s *Service) Retire(ctx context.Context, actor Actor, callID string, retired bool) (domain.DecisionCall, error)
```

Who may do what, and in which state:

| Action | Who | Allowed when the call is |
| --- | --- | --- |
| `Answer` | the person | needs you, in progress, or dismissed |
| `Dismiss` | the person | needs you |
| `Withdraw` | the owning session | needs you |
| `RecordOutcome` | the owning session, the session named in `DeliveredTo`, or the person | in progress |
| `Retire` | the person | done |

A session is the owning session only when `call.Origin == HostID` and `call.Raised.OwnerSessionID == actor.Session`. The same test of origin applies to `DeliveredTo`. Session ids repeat across machines.

New errors:

| Code | Constructor | When | Message |
| --- | --- | --- | --- |
| `DECISION_PERSON_ONLY` | `apierr.Forbidden` | a session calls `Answer`, `Dismiss` or `Retire` | `Only the person can answer, dismiss or retire a call. A session cannot.` |
| `DECISION_NOT_OWNER` | `apierr.Forbidden` | `Withdraw` by anyone but the owner; `RecordOutcome` by another session | `Only the session that raised this call can do that.` |
| `DECISION_WRONG_STATE` | `apierr.Conflict`, `Details` `{"state": <state>}` | the state does not allow the action | see below |

`DECISION_WRONG_STATE` messages, with `%s` the state's label:

- `Answer`: `This call is closed. Its state is "%s".`
- `Dismiss`: `Only a call in "Needs you" can be dismissed. This one is "%s".`
- `Withdraw`: `Only a call in "Needs you" can be withdrawn. This one is "%s".`
- `RecordOutcome`: `An outcome can be recorded once a call is "In progress". This one is "%s".`
- `Retire`: `Only a finished call can be marked "no longer applies". This one is "%s".`

Check in this order: the call exists (`DECISION_NOT_FOUND`), who is acting, the state, then the payload's rules (`DECISION_INVALID` from `ValidateDecisionAnswer` with the call's option count, `ValidateDecisionNote`, `ValidateDecisionOutcome`). Entries written by the person carry `AuthorKind` person and `AuthorSurface`; by a session, `AuthorKind` session and `AuthorSession`. `Withdraw` writes the payload `{}`. `Origin` is always `HostID`.

- [ ] **Step 1: Write the failing tests**

```go
person := Actor{Surface: domain.RequestSurfaceDesktop}
// raised(t, svc, session): raises validRaiseInput() as that session and returns the call.
// "option n" below is AnswerInput{Choice: domain.DecisionChoiceOption, Option: n}.

// TestAnswerIsThePersonsAlone
call := raised(t, svc, "w-1")
Answer as "w-1", as "w-2", as "orch"                         -> DECISION_PERSON_ONLY
Answer(person, option 2 with Note "Rotate monthly"): State in_progress, Option 2, Note "Rotate monthly",
    AnswerSurface desktop, Delivery pending; the entry written has Kind answered, AuthorKind person,
    AuthorSurface desktop, Origin "h_a"
Answer(person, {Choice: other, Note: "Use the vault"}): Choice other, ChosenLabel() == "Something else"

// TestAnswerRules: each is DECISION_INVALID with Details["rule"] as named
{Choice: "maybe"} -> choice_invalid;  option 3 -> option_range;  {Choice: other} -> note_required
Answer(person, "dec_missing", ...)                           -> DECISION_NOT_FOUND

// TestDismissAndReopen
Dismiss as "w-1"                                             -> DECISION_PERSON_ONLY
Dismiss(person, "Not enough to go on"): State dismissed, Note "Not enough to go on"
Dismiss(person, "") again                                    -> DECISION_WRONG_STATE, Kind == apierr.KindConflict, Details["state"] == "dismissed",
                                                                Message == `Only a call in "Needs you" can be dismissed. This one is "Dismissed".`
Answer(person, option 1): State in_progress

// TestOnlyTheOwnerOnThisMachineCanWithdraw
call := raised(t, svc, "w-1")
Withdraw as "w-2", and as the person                         -> DECISION_NOT_OWNER
// Through the store, append a raised entry for call "dec_far" with Origin "h_b" and OwnerSessionID "w-1".
Withdraw(Actor{Session: "w-1"}, "dec_far")                   -> DECISION_NOT_OWNER   // the same id on another machine
RecordOutcome(Actor{Session: "w-1"}, "dec_far", {Text: "Done"}) -> DECISION_NOT_OWNER
Withdraw(Actor{Session: "w-1"}, call.ID): State withdrawn
Answer(person, call.ID, option 1)                            -> DECISION_WRONG_STATE

// TestOutcomeAndRetire
call := raised(t, svc, "w-1")
RecordOutcome as "w-1", {Text: "Done"}                       -> DECISION_WRONG_STATE   // no answer yet
answered := Answer(person, option 1)
RecordOutcome as "w-2", {Text: "Done"}                       -> DECISION_NOT_OWNER
RecordOutcome as "w-1", {Text: "a\nb"}                       -> DECISION_INVALID, rule outcome_one_line
// Through the store, append a delivered entry with ForEntry answered.StandingEntryID, Status sent,
// SessionID "orch" and Fallback true: the owner had ended, so the answer went to the orchestrator.
RecordOutcome as "orch", {Text: "Shipped in PR 412", Link: "https://example.com/pr/412"}: State done, Outcome and OutcomeLink as given
Retire as "w-1"                                              -> DECISION_PERSON_ONLY
Retire(person, true): Retired;  Retire(person, false): !Retired
Retire(person, true) on a call still in Needs you            -> DECISION_WRONG_STATE

// TestThePersonCanRecordTheOutcome
raised as "w-1"; Answer(person, option 1); RecordOutcome(Actor{Surface: domain.RequestSurfaceCLI}, {Text: "Did it by hand"}): State done

// TestSearchPutsRetiredDecisionsLast
old := raised as "w-1", answered, its outcome recorded, then Retire(person, true)
current := raised as "w-2"
Search(Actor{Session: "w-1"}, "auth scheme") == [current, old]
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `cd backend && go test ./internal/service/decision/ -v`
Expected: build failure, `svc.Answer undefined`.

- [ ] **Step 3: Implement the five methods.**

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd backend && go test ./internal/service/decision/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/service/decision
git commit -m "feat(decisions): answer, dismiss, withdraw, record outcomes and retire"
```

### Task 10: Routes for the five actions

**Files:**
- Modify: `backend/internal/httpd/controllers/decisions.go`, `controllers/dto.go`, `apispec/specgen/build.go`
- Test: `backend/internal/httpd/controllers/decisions_test.go`
- Generated: `openapi.yaml`, `frontend/src/api/schema.ts`

**Interfaces:**
- Consumes: Tasks 7 and 9.
- Produces: `DecisionService` gains `Answer`, `Dismiss`, `RecordOutcome`, `Withdraw` and `Retire` with the signatures of Task 9. Each route answers 200 with a `DecisionCallResponse`.

| Route | Operation id | Body |
| --- | --- | --- |
| `POST /api/v1/decisions/{id}/answer` | `answerDecision` | `AnswerDecisionRequest` |
| `POST /api/v1/decisions/{id}/dismiss` | `dismissDecision` | `DismissDecisionRequest`, optional (`optionalReqBody: true`) |
| `POST /api/v1/decisions/{id}/outcome` | `recordDecisionOutcome` | `DecisionOutcomeRequest` |
| `POST /api/v1/decisions/{id}/withdraw` | `withdrawDecision` | none |
| `POST /api/v1/decisions/{id}/retire` | `retireDecision` | `RetireDecisionRequest` |

```go
type AnswerDecisionRequest struct {
	Choice string `json:"choice" enum:"option,other"`
	Option int    `json:"option,omitempty"`
	Note   string `json:"note,omitempty" maxLength:"1000"`
}

type DismissDecisionRequest struct {
	Note string `json:"note,omitempty" maxLength:"1000"`
}

type DecisionOutcomeRequest struct {
	Text string `json:"text" maxLength:"300"`
	Link string `json:"link,omitempty"`
}

type RetireDecisionRequest struct {
	Retired bool `json:"retired"`
}
```

Each operation lists the responses 200, 400, 403, 404, 409, 500 and 501. Fallback codes: `DECISION_ANSWER_FAILED`, `DECISION_DISMISS_FAILED`, `DECISION_OUTCOME_FAILED`, `DECISION_WITHDRAW_FAILED`, `DECISION_RETIRE_FAILED`.

- [ ] **Step 1: Write the failing tests**

```go
// fakeDecisionService gains the five methods and records answer, note, outcome, retired and the name of the last action.

// TestDecisionsAPI_Actions: each POST answers 200, passes the id "dec_1", and returns a body containing "id":"dec_1".
.../decisions/dec_1/answer    {"choice":"option","option":2,"note":"Rotate monthly"}, no caller
    svc.answer == decisionsvc.AnswerInput{Choice: "option", Option: 2, Note: "Rotate monthly"}; svc.actor.Session == ""
.../decisions/dec_1/dismiss   {"note":"Not enough to go on"}           svc.note == "Not enough to go on"
.../decisions/dec_1/dismiss   no body                                  svc.action == "dismiss"; svc.note == ""
.../decisions/dec_1/outcome   {"text":"Shipped in PR 412","link":"https://example.com/pr/412"}, caller "w-1"
    svc.outcome == decisionsvc.OutcomeInput{Text: "Shipped in PR 412", Link: "https://example.com/pr/412"}; svc.actor.Session == "w-1"
.../decisions/dec_1/withdraw  no body, caller "w-1"                    svc.action == "withdraw"; svc.actor.Session == "w-1"
.../decisions/dec_1/retire    {"retired":true}                         svc.action == "retire"; svc.retired

// TestDecisionsAPI_ActionRefusals: POST .../decisions/dec_1/answer when the service returns
apierr.Forbidden("DECISION_PERSON_ONLY", ...)                                    ->  403; the body contains "DECISION_PERSON_ONLY"
apierr.Conflict("DECISION_WRONG_STATE", ..., map[string]any{"state": "done"})    ->  409; the body contains "DECISION_WRONG_STATE"
apierr.NotFound("DECISION_NOT_FOUND", "Unknown call")                            ->  404; the body contains "DECISION_NOT_FOUND"
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `cd backend && go test ./internal/httpd/controllers/ -run 'DecisionsAPI' -v`
Expected: FAIL with 404 on the new paths.

- [ ] **Step 3: Implement the handlers, DTOs and spec entries; run `npm run api`.**

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd backend && go build ./... && go test ./internal/httpd/... -v 2>&1 | tail -30 && cd .. && npm run frontend:typecheck`
Expected: PASS, including `TestRouteSpecParity`; no type errors.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/httpd frontend/src/api/schema.ts
git commit -m "feat(decisions): add routes to answer, dismiss, withdraw, close and retire"
```

### Task 11: `ao decision answer`, `dismiss`, `outcome`, `withdraw` and `retire`

**Files:**
- Modify: `backend/internal/cli/decision.go`
- Test: `backend/internal/cli/decision_test.go`

**Interfaces:**
- Consumes: the routes of Task 10.
- Produces:

| Command | Request | Prints |
| --- | --- | --- |
| `ao decision answer <id> --pick <n> [--note <text>]` | `POST decisions/<id>/answer` `{"choice":"option","option":n,"note":...}` | `Answered <id>: <chosen label>` |
| `ao decision answer <id> --other --note <text>` | the same with `{"choice":"other","note":...}` | `Answered <id>: Something else` |
| `ao decision dismiss <id> [--note <text>]` | `POST decisions/<id>/dismiss` | `Dismissed <id>` |
| `ao decision outcome <id> --text <one line> [--link <url>]` | `POST decisions/<id>/outcome` | `Recorded the outcome of <id>` |
| `ao decision withdraw <id>` | `POST decisions/<id>/withdraw` | `Withdrew <id>` |
| `ao decision retire <id> [--undo]` | `POST decisions/<id>/retire` `{"retired": true}`, or `false` with `--undo` | `<id> no longer applies`, or `<id> applies again` |

Usage errors, exit 2: no id or more than one (`usage: ao decision <command> <id>`); `answer` with neither or both of `--pick` and `--other` (`usage: choose with --pick <n> or --other`); `outcome` without `--text` (`usage: --text is required`).

- [ ] **Step 1: Write the failing tests**

```go
// TestDecisionActionUsage: each exits 2 and the error contains the text.
decision answer                          ->  "ao decision <command> <id>"
decision answer dec_1                    ->  "--pick <n> or --other"
decision answer dec_1 --pick 1 --other   ->  "--pick <n> or --other"
decision outcome dec_1                   ->  "--text is required"
decision withdraw dec_1 dec_2            ->  "ao decision <command> <id>"

// TestDecisionActionsSendTheRightRequest
// The server answers {"id":"dec_1","chosenLabel":<label>,"state":"in_progress","stateLabel":"In progress"}, where
// <label> is "Something else" when the request's choice is "other" and "Per-tenant API keys" otherwise.
// Each row: AO_SESSION_ID; the command; the path; the body sent; the X-AO-Caller-Session header; stdout.
""     decision answer dec_1 --pick 2 --note "Rotate monthly"
       /api/v1/decisions/dec_1/answer    {"choice":"option","option":2,"note":"Rotate monthly"}    ""     "Answered dec_1: Per-tenant API keys\n"
""     decision answer dec_1 --other --note "Use the vault"
       /api/v1/decisions/dec_1/answer    {"choice":"other","note":"Use the vault"}                 ""     "Answered dec_1: Something else\n"
""     decision dismiss dec_1 --note "Not enough to go on"
       /api/v1/decisions/dec_1/dismiss   {"note":"Not enough to go on"}                            ""     "Dismissed dec_1\n"
"w-1"  decision outcome dec_1 --text "Shipped in PR 412" --link https://example.com/pr/412
       /api/v1/decisions/dec_1/outcome   {"text":"Shipped in PR 412","link":"https://example.com/pr/412"}  "w-1"  "Recorded the outcome of dec_1\n"
"w-1"  decision withdraw dec_1
       /api/v1/decisions/dec_1/withdraw  no body                                                   "w-1"  "Withdrew dec_1\n"
""     decision retire dec_1
       /api/v1/decisions/dec_1/retire    {"retired":true}                                          ""     "dec_1 no longer applies\n"
""     decision retire dec_1 --undo
       /api/v1/decisions/dec_1/retire    {"retired":false}                                         ""     "dec_1 applies again\n"

// TestDecisionAnswerFromASessionIsRefusedByTheDaemon, with AO_SESSION_ID=w-1
// The server answers 403 with an envelope.APIError: code "DECISION_PERSON_ONLY",
// message "Only the person can answer, dismiss or retire a call. A session cannot."
decision answer dec_1 --pick 1  ->  exit 1; the error contains "Only the person can answer"
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `cd backend && go test ./internal/cli/ -run 'Decision' -v`
Expected: FAIL, `unknown command "answer" for "ao decision"`.

- [ ] **Step 3: Implement the five commands.**

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd backend && go test ./internal/cli/ -run 'Decision' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/cli/decision.go backend/internal/cli/decision_test.go
git commit -m "feat(decisions): add ao decision answer, dismiss, outcome, withdraw and retire"
```

### Task 12: Delivering an answer into its session

**Files:**
- Modify: `backend/internal/domain/session.go` (add `ActiveOrchestrator`), test in `backend/internal/domain/session_test.go`
- Modify: `backend/internal/service/report/coordinator.go:213-228` (`activeOrchestrator` calls the new function)
- Create: `backend/internal/service/decision/message.go`, test `message_test.go`
- Create: `backend/internal/service/decision/delivery.go`, test `delivery_test.go`
- Modify: `backend/internal/daemon/daemon.go` (the messenger adapter beside `reportSemanticDelivery` near line 1602; wiring after the decision service)
- Test: `backend/internal/daemon/decision_delivery_test.go`

**Interfaces:**
- Consumes: Tasks 2, 4 and 9; `sessionmanager.ErrNotFound`, `ErrTerminated`, `ErrAgentExited`; `(*sessionmanager.Manager).SendWithOptions`.
- Produces:

```go
// domain/session.go
// ActiveOrchestrator is the newest orchestrator session that is neither
// terminated nor exited.
func ActiveOrchestrator(sessions []SessionRecord) (SessionRecord, bool)

// service/decision/message.go
// DeliveryMessage is the text sent into a session for the call's standing
// answer or dismissal. fallback is true when it goes to the project's
// orchestrator because the owning session has ended.
func DeliveryMessage(call domain.DecisionCall, fallback bool) string

// service/decision/delivery.go
var ErrSessionEnded = errors.New("decision: session can no longer take a message")

type Messenger interface {
	// DeliverAsPerson sends message into the session as the person's own
	// turn. It returns an error wrapping ErrSessionEnded when the session
	// can no longer take a message.
	DeliverAsPerson(ctx context.Context, id domain.SessionID, message string, at time.Time) error
}

type DeliveryStore interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	AppendDecisionEntry(context.Context, domain.DecisionEntry, time.Time) (domain.DecisionEntry, domain.DecisionCall, error)
	ListDecisionCallsAwaitingDelivery(context.Context, string) ([]domain.DecisionCall, error)
}

type DelivererDeps struct {
	Store      DeliveryStore
	Messenger  Messenger
	HostID     string
	Now        func() time.Time
	NewID      func(prefix string) string
	RetryDelay time.Duration // default 30 seconds
	Log        *slog.Logger
}

func NewDeliverer(d DelivererDeps) *Deliverer
func (d *Deliverer) Wake()
func (d *Deliverer) Start(ctx context.Context) <-chan struct{}
// RunDue tries every pending delivery once and returns how many are still
// pending. Tests call it directly.
func (d *Deliverer) RunDue(ctx context.Context) (int, error)
```

`RunDue`, for each call from `ListDecisionCallsAwaitingDelivery(HostID)`:

1. Load the owning session. It has ended when it is missing or `IsTerminated`.
2. Not ended: `DeliverAsPerson(owner, DeliveryMessage(call, false))`. Success: append a `delivered` entry with status `sent` naming the owner. An error wrapping `ErrSessionEnded`: treat it as ended. Any other error: log the call id, count it as still pending, go on.
3. Ended: find `domain.ActiveOrchestrator(ListSessions(project))`. Skip this when the call has no project or the orchestrator is the ended owner. Found: `DeliverAsPerson(orchestrator, DeliveryMessage(call, true))`, then append `delivered` with status `sent`, the orchestrator's id and name, and `Fallback: true`. An error wrapping `ErrSessionEnded`, or no orchestrator: append `delivered` with status `no_session`. Any other error: still pending.

A `delivered` entry has `AuthorKind` system, `Origin` `HostID`, and `ForEntry` set to `call.StandingEntryID`. If appending fails after a send, the call stays pending and is sent again on the next run.

`Start` runs `RunDue` once, then again on every `Wake`, and every `RetryDelay` while the last run left something pending. It returns a channel closed when `ctx` ends, like `report.Coordinator.Start`.

`DeliveryMessage` returns exactly these texts. `<title>` is the call's title, `<link>` is `domain.DecisionCallURL(call.ID)`, `<id>` is the call id, and the `Note:` paragraph is left out when the note is empty.

An answer:

```
Decision on your call "<title>"
<link>

I chose: <chosen label>
Note: <note>

Carry this out. When it is done, record what happened in one line:
ao decision outcome <id> --text "<what happened>"
```

- When `call.Choice` is `other`, the `I chose:` line is `I chose something else. My note says what to do.`
- When `call.Redelivery` is true, the first line is `Changed decision on your call "<title>"`, and the line `This replaces my earlier answer.` follows the link.
- With `fallback`, the first line is always `Decision on a call raised by "<owner session name>", which has ended: "<title>"`. A changed pick keeps that first line and still gets `This replaces my earlier answer.` after the link. The instruction is `Carry this out, or hand it to a worker. When it is done, record what happened in one line:`.

A dismissal:

```
I dismissed your call "<title>" without choosing.
<link>

Note: <note>

Do not wait for an answer on it. If you still need one, raise a new call that covers what was missing.
```

- With `fallback`, the first line is `I dismissed a call raised by "<owner session name>", which has ended: "<title>"`, and the last paragraph is `Nothing needs to be carried out.`

The daemon adapter:

```go
type decisionMessenger struct {
	sessions interface {
		SendWithOptions(context.Context, domain.SessionID, string, *ports.SpawnAttachment, ports.MessageDeliveryOptions) error
	}
}

func (m decisionMessenger) DeliverAsPerson(ctx context.Context, id domain.SessionID, message string, at time.Time) error
```

It calls `SendWithOptions` with `ports.MessageDeliveryOptions{InteractionAt: at, AuthoredByUser: true}` and no attachment. When the error is `sessionmanager.ErrNotFound`, `ErrTerminated` or `ErrAgentExited`, it returns `fmt.Errorf("%w: %w", decisionsvc.ErrSessionEnded, err)`.

Wiring in `daemon.go`: type-assert `sessMgr` to the adapter's interface the way the report wiring asserts `reportSemanticSession`, and return a wiring error if it fails. Give `decisionsvc.Deps` an `OnAppended` that wakes the deliverer. Start the deliverer and wait for its channel on shutdown, as the report coordinator's is.

- [ ] **Step 1: Write the failing tests**

```go
// TestActiveOrchestrator, in domain/session_test.go
// Sessions: a worker "w" created last of all; orchestrators "old" (created at t), "new" (t plus 1h),
// "dead" (t plus 2h, IsTerminated) and "exited" (t plus 3h, Activity.State ActivityExited).
ActiveOrchestrator(sessions) returns "new", true;  ActiveOrchestrator(nil) returns false

// messageCall(): id "dec_1", title "Which auth scheme should the proxy use?", owner session name "Proxy worker",
// options "OAuth client credentials" and "Per-tenant API keys", Choice option, Option 1, Note "Rotate monthly.",
// in progress.

// TestDeliveryMessageForAnAnswer
DeliveryMessage(messageCall(), false) ==
	"Decision on your call \"Which auth scheme should the proxy use?\"\n" +
	"ao://decisions/dec_1\n\n" +
	"I chose: OAuth client credentials\n" +
	"Note: Rotate monthly.\n\n" +
	"Carry this out. When it is done, record what happened in one line:\n" +
	"ao decision outcome dec_1 --text \"<what happened>\"\n"

// TestDeliveryMessageVariants: what the text contains, and after "not:" what it must not contain
no note:                  "I chose: OAuth client credentials\n\nCarry this out."                         not: "Note:"
Choice other, Note "Use the vault.":  "I chose something else. My note says what to do.\nNote: Use the vault."
Redelivery:               "Changed decision on your call \"Which auth scheme should the proxy use?\"\nao://decisions/dec_1\nThis replaces my earlier answer.\n\n"
fallback:                 "Decision on a call raised by \"Proxy worker\", which has ended: \"Which auth scheme should the proxy use?\"",
                          "Carry this out, or hand it to a worker."                                      not: "your call"
fallback and Redelivery:  "Decision on a call raised by", "This replaces my earlier answer."             not: "Changed decision"
dismissed, Note "Not enough to go on.":
                          "I dismissed your call \"Which auth scheme should the proxy use?\" without choosing.\nao://decisions/dec_1\n\nNote: Not enough to go on.\n\nDo not wait for an answer on it."
                                                                                                         not: "ao decision outcome", "I chose"
dismissed and fallback:   "I dismissed a call raised by \"Proxy worker\", which has ended:", "Nothing needs to be carried out."   not: "Do not wait"

// The delivery tests use the store and service of newDecisionService, and:
//   fakeMessenger: records each (session, text) sent; errs[session] is returned once in place of sending.
//   failingAppend: wraps the store and refuses the next delivered entry once, as if the daemon stopped between
//                  sending and recording.
//   answeredCall(t, svc, session): raises as that session, answers option 1 as the person, returns the call.
//   terminateDecisionSession(t, store, id): marks a seeded session terminated, the way the store's tests do.
//   newDeliverer(t): a Deliverer for HostID "h_a" over that store and a fakeMessenger.

// TestDelivererSendsEachAnswerOnce
call := answeredCall(t, svc, "w-1")
RunDue  ->  0 pending; one message, to "w-1", with text == DeliveryMessage(call, false)
// The stored call has Delivery sent, DeliveredTo "w-1" and no fallback. Its last entry has Kind delivered,
// AuthorKind system and Origin "h_a".
RunDue again, as after a restart  ->  still one message
Answer(person, option 2); RunDue  ->  a second message, beginning "Changed decision on your call"

// TestDelivererDeliversADismissal
raised as "w-1"; Dismiss(person, "Not enough to go on"); RunDue  ->  one message, beginning "I dismissed your call"

// TestDelivererFallsBackToTheOrchestrator
ended := answeredCall(t, svc, "w-1");  refused := answeredCall(t, svc, "w-2")
terminate "w-1";  messenger.errs["w-2"] = an error wrapping ErrSessionEnded
RunDue  ->  0 pending; two messages, both to "orch", both beginning "Decision on a call raised by"
// Both stored calls have Delivery sent, DeliveredTo "orch" and DeliveredFallback.

// TestDelivererRecordsThatNoSessionCanTakeIt
answeredCall(t, svc, "w-1");  answeredCall(t, svc, "loose");  terminate "w-1", "loose" and "orch"
RunDue  ->  0 pending; no message; both stored calls have Delivery no_session

// TestDelivererRetriesAfterAPassingFailure
answeredCall(t, svc, "w-1");  messenger.errs["w-1"] = errors.New("composer busy")
RunDue  ->  1 pending; no message; the stored call's Delivery is still pending
RunDue  ->  0 pending; one message

// TestDelivererSendsAgainWhenRecordingFailed
call := answeredCall(t, svc, "w-1");  the Deliverer's store is a failingAppend
RunDue  ->  1 pending; one message
RunDue  ->  0 pending; two messages, identical; the call has exactly one delivered entry

// TestDelivererLeavesOtherMachinesCallsAlone
answeredCall(t, svc, "w-1");  a Deliverer with HostID "h_b" over the same store
RunDue  ->  0 pending; no message

// TestDecisionMessengerSendsAsThePersonAndNamesEndedSessions, in daemon/decision_delivery_test.go, with a fake SendWithOptions
DeliverAsPerson(ctx, "w-1", "hello", at): the fake saw message "hello", options.AuthoredByUser true, options.InteractionAt equal to at
// The fake returns an error wrapping sessionmanager.ErrNotFound, ErrTerminated or ErrAgentExited:
//   errors.Is(err, decisionsvc.ErrSessionEnded) is true.
// The fake returns errors.New("composer busy"): an error that is not ErrSessionEnded.
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `cd backend && go test ./internal/domain/ ./internal/service/decision/ ./internal/daemon/ -run 'ActiveOrchestrator|Deliver|DecisionMessenger' -v`
Expected: build failures, `undefined: ActiveOrchestrator`, `undefined: DeliveryMessage`, `undefined: NewDeliverer`, `undefined: decisionMessenger`.

- [ ] **Step 3: Implement**

`ActiveOrchestrator` in `domain/session.go`, with `Coordinator.activeOrchestrator` reduced to listing the sessions and calling it. Then `message.go`, `delivery.go`, the adapter and the wiring.

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd backend && go build ./... && go test ./internal/domain/ ./internal/service/decision/ ./internal/service/report/ ./internal/daemon/ -v 2>&1 | tail -40`
Expected: PASS. The report coordinator's own tests still pass.

Run: `cd backend && go test -race ./internal/service/decision/`
Expected: PASS.

- [ ] **Step 5: Try the whole loop**

In the same desktop lab, against scratch data: from a throwaway session's shell, raise a call. From your own shell run `ao decision ls`, then `ao decision answer <id> --pick 1 --note "test"`. Open the session.
Expected: the session received a message that begins `Decision on your call`, shown as your own message. `ao decision show <id>` lists `raised`, `answered` and `delivered`. From the session's shell, `ao decision outcome <id> --text "done"` moves it to `Done`. From the session's shell, `ao decision answer <id> --pick 1` is refused with `Only the person can answer`.

- [ ] **Step 6: Commit, then open PR 2**

```bash
git add backend/internal/domain/session.go backend/internal/domain/session_test.go backend/internal/service/report/coordinator.go backend/internal/service/decision backend/internal/daemon
git commit -m "feat(decisions): deliver answers into the owning session"
```

---

# Part D: telling sessions

### Task 13: Standing instructions

**Files:**
- Modify: `backend/internal/session_manager/prompt.go` (new `decisionCallsPrompt`; `buildSystemPromptText` at line 132; two bullets in `workerSystemPrompt`)
- Test: `backend/internal/session_manager/prompt_test.go`

**Interfaces:**
- Consumes: the command names of Tasks 8 and 11 and the message openings of Task 12.
- Produces: `func decisionCallsPrompt() string`, included for every role.

In `buildSystemPromptText`, change the shared line to:

```go
sections = append(sections, decisionCallsPrompt(), userOrientationPrompt(), publishingScopePrompt())
```

`decisionCallsPrompt` returns exactly this text:

```
## Decisions for the Human

When you need the human to choose between options, raise a call in Orchestrator's decision ledger. Do not ask in passing, and do not pass the question to another session.

- Search first: `ao decision search <words>`. Try more than one phrasing. If a decision that still applies covers your question, follow it and say which one. If a matching call still needs the human, do not raise a second one; check it again later.
- Otherwise raise one call for each decision: `ao decision raise --title <the question> --context <what they need to know> --option "<label> :: <what it leads to>" --option "<label> :: <what it leads to>" --recommend <n>`. Give two to four options and recommend one, with the reason in the context.
- Raise it yourself. If another session brings you a question, tell it to raise the call. If that session has ended, raise the call yourself with the full context. Never ask the human for a ruling by id.
- Keep working on anything that does not depend on the answer.
- The answer arrives in this session as a message from the human that begins "Decision on your call" or "Changed decision on your call". Act on it, then record what happened in one line: `ao decision outcome <id> --text <what happened>`. A message that begins "I dismissed your call" means no answer is coming.
- If the session that raised a call has ended, its answer goes to the project's orchestrator and begins "Decision on a call raised by". Carry it out or hand it to a worker, then record the outcome.
- When you mention a call anywhere, use the link `ao decision raise` printed. Never use a bare id.
- After a restart or a compacted context, run `ao decision ls` to see what you asked, what was answered, and which outcomes you still owe.
- Only the human answers, dismisses or retires a call. You cannot.

Write every call for a reader who has not seen the code, the thread or the ticket:

- The title is the question itself, in one line.
- Give every internal code, id and abbreviation a plain label.
- Say what each option leads to, including what it costs or risks.
- Put the unit, the time window and the source next to every number. Mark a guess as a guess.
- When you ask them to approve a text, show the text.
- Check that the facts are still current just before you raise it.
- Never put secrets, credentials or personal data in a call.
```

In `workerSystemPrompt`, replace two bullets:

- `- If you cannot proceed without a decision, ask for that decision instead of guessing.` becomes `- If you cannot proceed without a decision, raise a call for it (see Decisions for the Human) instead of guessing.`
- `- When a decision or missing input blocks progress, run ` + "`ao report --needs-input --note <text>`" + `.` becomes `- When missing input that is not a choice between options blocks progress, such as missing access, run ` + "`ao report --needs-input --note <text>`" + `. For a choice, raise a call (see Decisions for the Human).`

- [ ] **Step 1: Write the failing tests**

```go
// TestBuildSystemPromptTellsEveryRoleHowToAskTheHumanToDecide
// Build the prompt for a worker with OrchestratorSessionID "shop-1", a standalone worker, an orchestrator, and an
// orchestrator with OrchestratorOverride "You coordinate.". Each prompt contains every one of:
"## Decisions for the Human"            // exactly once
"ao decision search <words>"
"ao decision raise --title <the question>"
`--option "<label> :: <what it leads to>"`
"Never ask the human for a ruling by id."
`begins "Decision on your call" or "Changed decision on your call"`
`begins "I dismissed your call"`
`begins "Decision on a call raised by"`
"ao decision outcome <id> --text <what happened>"
"run `ao decision ls`"
"Only the human answers, dismisses or retires a call."
"Never put secrets, credentials or personal data in a call."

// TestBuildSystemPrompt_WorkerSendsChoicesToCallsNotReports: the worker prompt contains
"raise a call for it (see Decisions for the Human) instead of guessing"
"When missing input that is not a choice between options blocks progress, such as missing access, run `ao report --needs-input --note <text>`. For a choice, raise a call (see Decisions for the Human)."
// and no longer contains
"When a decision or missing input blocks progress"
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `cd backend && go test ./internal/session_manager/ -run 'BuildSystemPrompt' -v`
Expected: FAIL. The prompt has no "## Decisions for the Human" section yet.

- [ ] **Step 3: Implement the section and the two bullet changes.**

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd backend && go test ./internal/session_manager/ -run 'Prompt' -v`
Expected: PASS. `TestBuildSystemPrompt_WorkerRequiresDurableReports` still finds `ao report --needs-input --note <text>`.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/session_manager/prompt.go backend/internal/session_manager/prompt_test.go
git commit -m "feat(decisions): tell sessions to raise calls for the human"
```

### Task 14: The command guide and the user docs

**Files:**
- Create: `backend/internal/skillassets/using-ao/commands/decision.md`
- Modify: `backend/internal/skillassets/using-ao/SKILL.md` (a catalog row after `report`)
- Modify: `backend/internal/skillassets/using-ao/commands/report.md` (one sentence under the `--needs-input` paragraph)
- Test: `backend/internal/skillassets/skillassets_test.go`
- Modify: `frontend/src/docs/content/cli.mdx` (a `### \`ao decision\`` section after `### \`ao send\`` at line 156)

**Interfaces:**
- Consumes: the commands of Tasks 8 and 11.
- Produces: the guide sessions read before using `ao decision`.

The catalog row:

```
| `decision` | Ask the human to choose between options, and look up what they decided before | A choice only the human can make; checking earlier decisions before asking | [commands/decision.md](commands/decision.md) |
```

The sentence for `report.md`: `` `--needs-input` is for a blocker that is not a choice between options, such as missing access. For a choice, raise a call: see [`ao decision`](decision.md). ``

`decision.md` has these sections, in this order, written in the voice of the other guides:

1. **Title and one line.** `# ao decision`, then: `Ask the human to choose between options, and look up what they have already decided.`
2. **Syntax.** Every command of Tasks 8 and 11, one per line.
3. **When to raise a call.** A choice between options that only the human can make. Not for a blocker that is not a choice (`ao report --needs-input`), and not for something an earlier decision already covers.
4. **Search first.** `ao decision search`, more than one phrasing, follow a decision that still applies and say which one, never raise a second call for one that still needs the human.
5. **Raising.** The flags, the `--file` form with a JSON example, one complete example with two options, the limits from Global Constraints, and that a refused call names the rule it broke.
6. **Raise it yourself.** The three sentences on not relaying, and: `Never ask the human for a ruling by id.`
7. **What happens next.** The four message openings of Task 12, and that the answer is the human's own message.
8. **Recording the outcome.** `ao decision outcome`, one line, who may record it.
9. **Withdrawing.** Only while the call still needs the human.
10. **After a restart.** `ao decision ls`, and what `Outcome owed` means.
11. **Links.** `ao://decisions/<id>`, always the link, never a bare id.
12. **Writing a call.** The seven rules from Task 13's second list, each with one sentence of why.
13. **What you cannot do.** `Only the human answers, dismisses or retires a call.`

The `cli.mdx` section is for the person: what a call is in two sentences, then `ao decision ls --needs-you`, `show`, `answer --pick` and `--other`, `dismiss`, `retire`, and one line saying sessions raise calls themselves.

- [ ] **Step 1: Write the failing test**

```go
// TestEmbeddedDecisionGuidanceCoversSearchingRaisingAndClosing, in the style of
// TestEmbeddedReportGuidanceDistinguishesDeliverablesFromDiagnostics: collapse whitespace, then match.
// using-ao/commands/decision.md contains every one of:
"# ao decision"
"ao decision search"
`--option "<label> :: <what it leads to>"`
"--recommend"
"--file"
"Never ask the human for a ruling by id."
`"Decision on your call"`
`"Changed decision on your call"`
`"I dismissed your call"`
`"Decision on a call raised by"`
"ao decision outcome"
"ao decision withdraw"
"ao decision ls"
"ao://decisions/"
"Only the human answers, dismisses or retires a call."
"Never put secrets, credentials or personal data in a call."
"ao report --needs-input"
// using-ao/SKILL.md contains "| `decision` |" and "(commands/decision.md)".
// using-ao/commands/report.md contains "For a choice, raise a call: see [`ao decision`](decision.md)."
```

- [ ] **Step 2: Run the test and see it fail**

Run: `cd backend && go test ./internal/skillassets/ -run 'DecisionGuidance' -v`
Expected: FAIL, `open using-ao/commands/decision.md: file does not exist`.

- [ ] **Step 3: Write the guide, the catalog row, the sentence in `report.md` and the `cli.mdx` section.**

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd backend && go test ./internal/skillassets/ -v`
Expected: PASS, including `TestEmbeddedSkillFrontmatterIsValidYAML`.

Run: `cd frontend && npm run typecheck`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/skillassets frontend/src/docs/content/cli.mdx
git commit -m "docs(decisions): add the ao decision guide for sessions and for the person"
```

### Task 15: A reminder on the old path

**Files:**
- Modify: `backend/internal/cli/report.go:196` (the end of `report`)
- Test: `backend/internal/cli/report_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: after a successful `ao report --needs-input`, one line on standard error:

```
Reminder: if this is a choice between options, raise a call for the human instead. See: ao decision raise --help
```

Nothing is printed for any other report, or when the request fails. Standard output stays empty.

- [ ] **Step 1: Write the failing test**

```go
// reportCLIServer(t, handler): lift the server setup TestReportModesAndBoundaries has inline into this helper,
// and use it in both tests.

// TestReportNeedsInputRemindsAboutCalls, with AO_SESSION_ID=worker-1
reminder := "Reminder: if this is a choice between options, raise a call for the human instead. See: ao decision raise --help\n"
report --needs-input --note x, the server answers 201  ->  stdout == "", stderr == reminder
report --checkpoint --note x, the server answers 201   ->  stdout == "", stderr == ""
report --needs-input --note x, the server answers 500  ->  an error; stderr does not contain "Reminder"
```

- [ ] **Step 2: Run the test and see it fail**

Run: `cd backend && go test ./internal/cli/ -run 'ReportNeedsInputReminds' -v`
Expected: FAIL. Standard error is empty where the reminder should be.

- [ ] **Step 3: Print the reminder to `c.deps.Err` after the request succeeds, when the state is `domain.ReportNeedsInput`.**

- [ ] **Step 4: Run the tests and see them pass**

Run: `cd backend && go test ./internal/cli/ -run 'Report' -v`
Expected: PASS.

- [ ] **Step 5: Commit, then open PR 3**

```bash
git add backend/internal/cli/report.go backend/internal/cli/report_test.go
git commit -m "feat(decisions): remind sessions about calls when they report needs-input"
```

---

## Before each pull request

Run all of these from the repo root and fix what fails before opening the pull request. A local pass is not a guarantee: check the remote checks too.

```bash
npm run lint                 # go test ./... and golangci-lint
cd backend && go test -race ./internal/domain/ ./internal/service/decision/ ./internal/storage/sqlite/... && cd ..
npm run frontend:typecheck
npm run sqlc && npm run api && git diff --exit-code   # generated files are committed and current
```

Expected: all pass, and `git diff --exit-code` prints nothing.

The pull request description follows `.agents/skills/pr-description/SKILL.md`, links the spec, and for PR 2 includes the terminal transcript of Task 12, Step 5 as its evidence.
