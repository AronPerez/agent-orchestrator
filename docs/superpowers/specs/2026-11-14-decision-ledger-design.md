# Decision ledger: design

- **Status:** approved by the person on 2026-11-16. Implementation started on 2026-11-17. Section 13 (asking for more from a call) is still a proposal.
- **Date:** 2026-11-14
- **Source:** brainstorming session `ao://sessions/agent-orchestrator/agent-orchestrator-7` (the rendered mockups are in that chat; a standalone copy of the final one is [decision-ledger-mockup.html](decision-ledger-mockup.html)).
- **Also informed by:** a team call on 12 November 2026, in which a second developer who is about to start using Orchestrator described the same problem with their own fleet. What it added is marked "from the team call".

## Summary

Sessions often need a decision from the person running them. Today that person tracks those questions in a hand-built page published from a Claude account, outside Orchestrator. This design brings that into Orchestrator as a **decision ledger**: a session raises a question with options, the person is notified and picks one, the session carries it out and records what happened, and the whole history stays searchable by the person and by sessions on every machine.

Every machine running Orchestrator keeps a full copy of the ledger and syncs it with the other machines directly. Nothing depends on an outside account or service.

A call that rests on a lot of evidence can carry a full story page, which is copied to every machine with it.

Raising a call is one short command. The session that has the question raises it itself, written so a person can read it cold, and the app draws the board. No session relays the question, and no agent maintains a page.

## Terms

| Term | Meaning |
| --- | --- |
| Call | A question a session raises for the person to decide. It has a plain-language title, short context, and two to four options with one recommended. |
| Decision | The person's answer to a call: one of the options, or "something else" with a note. |
| Ledger | The complete, permanent record of every call and what happened to it. |
| Entry | One fact appended to the ledger, for example "raised", "answered" or "outcome". Entries are never edited or deleted. |
| Machine | One computer running Orchestrator. It is identified by the host id Orchestrator already assigns to its data directory (`h_<uuid>`, `backend/internal/mobilebridge/identity.go`). |
| Link, peer | A link tells one machine to sync its ledger with another. The other machine is its peer. |
| Owning session | The session that raised a call and is expected to carry out the decision. |
| Work stream | One line of work inside a project: a manager session and the sessions it spawned. Orchestrator works it out from who spawned whom. |
| The person | The single human who owns all the machines. There is exactly one. |

## Why

The current solution is a published page called Fleet Decision Queue. Sessions add cards to it, the person picks an option on a card, the owning session notices within about 15 minutes, acts, and writes the outcome back.

The card model works and is kept. What is wrong, in the person's words:

1. **It lives outside Orchestrator.** It is a separate page to remember to open. Nothing on the board or in notifications says a new call has arrived.
2. **It is tied to a Claude account,** because it is a published artifact. The replacement must not depend on any outside account, and must work with any agent.

The team call added four more:

3. **Questions are relayed until they mean nothing.** A worker's question passes up through manager sessions, and the person ends up being asked for "a ruling on D75" with no context, options or recommendation attached. They then have to ask for all of it.
4. **The context is in another stream.** With several streams of work running, the person needs the decisions from all of them in one place and, on opening one, the context of that stream and no other.
5. **What sessions write is written for other agents.** A session's notes and artifacts are hard for a person to read. A readable page fixes that, but building one for every question costs minutes and tokens. Being readable has to be the default, and cheap.
6. **A ledger kept by an agent is slow.** The agent that maintains the page spends tokens on it and updates it slowly.

## Goals

1. A session running any agent can raise a call, and the person can answer it, entirely inside Orchestrator.
2. The person hears about a new call without going to look: in the desktop notification bell and as a phone push.
3. The person can see which state every call is in (needs them, in progress, done) on one page, in plain language, across all projects, streams of work and machines. Opening a call shows the context of its own stream.
4. The person can answer from the desktop app or from the phone.
5. Every call and its outcome stays in a history the person can search.
6. A session on any machine can search every earlier decision from any project and any machine before it asks, so the person is not asked the same thing twice and new work stays consistent with earlier decisions.
7. Every machine holds the full ledger and keeps working from its own copy when it cannot reach the others. A person with one machine gets the whole feature with nothing to set up.
8. A call that rests on a lot of evidence can carry a full story page that opens on any device. Small calls stay short text.
9. Raising a call is cheap: one short command, with no page to build. The app draws the board, and no agent maintains it.
10. A call reaches the person from the session that has the context, with the context attached. No session relays or rewords another session's question.

## Non-goals

- Other kinds of items from the old page: human-only steps and the initiatives status table. (Calls an agent made on the person's behalf, "Watching", were a non-goal until 2026-11-18, when the [autonomy-levels design delta](2026-11-18-autonomy-levels-design.md) added them as `decided` entries.)
- More than one person. No teammates, no sharing, no permissions.
- Sessions that run in Orchestrator's cloud sandboxes. The ledger covers sessions run by a machine's own Orchestrator.
- Agreement between machines that distrust each other. All machines belong to one person, so there is no consensus protocol, signing or tamper-proofing.
- Deleting or editing entries.
- Bringing the old page's items into the ledger.
- A separate "inbox" page listing only the calls that need the person. The notification bell already provides that list. It can be added later and would reuse the same call panel.
- Orchestrator building story pages itself, or depending on any particular skill or agent to build them.
- A general memory, task list or log for agents. Sessions already keep their own notes and plans. The ledger holds only decisions asked of the person.

## Anti-goals

These would make the feature a failure even if it technically works:

- The person is asked something they already decided, because the session never searched or the search missed it.
- A session follows a decision that no longer applies, or one from an unrelated context, without saying so.
- A card shows a state that is not true, for example "In progress" when nothing was delivered to any session.
- An agent answers a call in the person's name.
- The same call notifies the person once per machine.
- The ledger needs an outside account, a hosted service, or one particular machine to be up.
- A call reaches the person as a bare id or a second-hand summary, without its context, options and recommendation.
- Raising a call costs enough time or tokens that sessions avoid it.

## What exists today

A builder should read these before planning. None of them is the feature, but each is a seam it attaches to.

| Existing piece | Where | Relevance |
| --- | --- | --- |
| "Needs input" notification | `backend/internal/domain/notification.go`, `backend/internal/notify/` | Fires when an agent is stopped at a prompt. Carries no question text and resolves itself when the session moves on. The new notification kind follows its "stays until resolved" behaviour. |
| Worker reports with a `needs_input` state | `backend/internal/domain/report.go`, `backend/internal/service/report/coordinator.go` | A short note delivered to the project's orchestrator session, not to the person. No options, answer or outcome. Its durable outbox is the pattern for delivering answers. |
| Sessions board | `packages/product-ui/src/SessionsBoardView.tsx`, `backend/pkg/contract/kanban.go` | Columns only, no swimlanes. One card is one session, placed from PR and check facts. The decisions board borrows its lane and card language but is a separate page. |
| Notification bell | `frontend/src/renderer/components/NotificationCenter.tsx` (mounted in `PageTopbar.tsx`) | Already merges notifications from every connected machine. |
| Phone push | `backend/internal/push/dispatcher.go` | Sends a push for each new notification to paired devices. |
| Several machines in one app | `docs/remote-sessions-edd.md`, `frontend/src/renderer/lib/hosts.ts` | The app connects to many machines at once. Machines do not talk to each other today. |
| A machine's authenticated listener | `backend/internal/httpd/lan_listener.go`, `auth.go`, `docs/adr/0001-lan-listener-for-mobile.md` | The address and credential the app, phone and command line already use to reach another machine. Ledger sync uses the same listener. |
| Who is calling | `backend/internal/caller/caller.go`, `backend/internal/httpd/surface_middleware.go` | The daemon can tell a command issued from an agent's shell from one issued by the desktop, the phone, or the person's own shell. |
| Message delivery into a session | `backend/internal/service/session/service.go` (`Send`, `SendWithOptions`), `domain.WrapSessionDelivery` | How an answer reaches the owning session. |
| Standing instructions and command guides for sessions | `backend/internal/session_manager/prompt.go`, `backend/internal/skillassets/using-ao/commands/` | Where sessions learn to search and raise calls. |
| Full-text search | migration `20261024194546592_conversation_messages_search.sql` | Precedent for an FTS5 index. |
| Live change stream | `backend/internal/cdc/`, DB triggers into `change_log` | How new ledger rows reach an open app without polling. |
| Session artifacts and the page viewer | `backend/internal/sessionartifacts/`, `backend/internal/renderpage/`, the artifact views in the app and on the phone | Where a story page comes from, and how a page written by an agent is displayed safely. |
| Project rules | `backend/internal/session_manager/prompt.go` (`projectRulesConfig`) | How the person tells their own sessions to use a particular skill, without Orchestrator knowing about it. |
| In-app links | `frontend/src/renderer/lib/session-links.ts` | Links of the form `ao://sessions/{project}/{session}` open a session, or one of its artifacts, from chat. There is no other kind of in-app link today. A call needs one of the same family. |
| Spawn lineage and the Fleet view | `ParentSessionID` in `backend/internal/domain/session.go`, `frontend/src/renderer/lib/session-fleet.ts` | Which session spawned which. The Fleet view already treats a session that has spawned others as a manager. Work streams are worked out the same way. |

## Constraints

From `AGENTS.md`, `CLAUDE.md` and `DESIGN.md`. These are not negotiable within this feature.

- All ledger state, including peer addresses and credentials, lives under `~/.ao`.
- The primary listener stays bound to `127.0.0.1` and unauthenticated. No new network-facing listener is added. Sync is served by the existing authenticated listener and its existing tunnel addresses, behind the existing auth middleware.
- The command line stays a thin client of the local daemon. It never opens the database.
- Schema changes are new migrations created with `npm run migration:create`; generated sqlc code is regenerated, not edited.
- Live updates come from DB triggers into `change_log`, not from manual event emission.
- The daemon API is code-first: edit `controllers/dto.go` and the spec registry, then run `npm run api`.
- Session display status is derived at read time and never stored.
- UI follows `DESIGN.md`: sentence case, semantic colour only, lanes as one continuous grid with shared dividers, shadcn primitives, existing tokens, every string through the translation files. Add the new page to the guide's decisions log when it is built.
- Peer credentials never reach the renderer, logs or telemetry. Call text never goes to telemetry.

## Approaches considered

### Where the ledger lives

| Approach | What it is | Trade-off | Outcome |
| --- | --- | --- | --- |
| Home machine | The always-on machine holds the ledger; other machines pass requests through it. | Smallest build and one source of truth, but searches from other machines fail while the home is unreachable. | Recommended by the designer; not chosen. |
| **Synced copy on every machine** | Every machine holds the whole ledger and exchanges changes with its peers. | Works offline and no machine is special, but needs sync, ordering rules and much more testing. | **Chosen by the person.** |
| Private git repository | One file per decision in a repository every machine pulls and pushes. | Readable without Orchestrator, but adds a git host as a dependency, lags, and sits outside how Orchestrator stores state. | Not chosen. |
| Each machine keeps its own, searches ask the others live | No copies. | A search silently misses any machine that is asleep. | Dropped: works against goal 6. |
| Orchestrator's cloud service | Ledger behind the Orchestrator account. | Another outside account. | Dropped: works against the reason for the feature. |

The person asked for "a mesh, like Bitcoin: everyone has a ledger and can replicate or sync". The architectural name is **local-first, peer-to-peer replication** (multi-leader replication with eventual consistency, synced by gossip). It keeps Bitcoin's "everyone holds the full, append-only history" and leaves out mining and consensus, which exist for parties that distrust each other.

### How it looks in the app

| Approach | What it is | Trade-off | Outcome |
| --- | --- | --- | --- |
| Inbox page | A list of calls by state with the selected call open beside it. | Fastest to answer and maps directly to the phone, but a weaker overview. | Recommended by the designer as the only surface; not chosen as a page. |
| **Swimlane board** | A page with a column per state and rows for the work being decided. | Best picture of where everything stands; answering needs a card opened. | **Chosen, as the place to manage all calls.** |
| On the sessions board | Calls shown on session cards, no new page. | Nothing new to learn, but one status line per card, no cross-project view, and history still needs its own place. | Not chosen. A small indicator is kept as an optional later slice. |

The person's refinement, adopted: **the notification bell and phone push announce each new call; the swimlane board is where all calls are managed.**

For the rows, three ways were considered: a row per project, a row per work stream, and a stream named by the session that raises the call. The person chose **project first, then a row per work stream inside it.** Streams named by sessions were dropped because names drift and would split one line of work across several rows.

## Design

### 1. The life of a call

1. **Search first.** Before asking, a session searches the ledger. Each result shows what was chosen, the person's note, the outcome, when, in which project, and whether it still applies. If one covers the question, the session follows it and does not ask.
2. **Raise.** The session that has the question raises the call itself (section 7, "Raise at the source"). It may keep working on anything that does not depend on the answer.
3. **Arrive.** The person gets a notification, and the call appears under "Needs you".
4. **Answer.** The person picks an option, or "Something else", and may add a note. The call moves to "In progress".
5. **Deliver.** Orchestrator delivers the answer into the owning session as a message: within seconds when that session's machine is reachable, otherwise when it next syncs.
6. **Carry out.** The session acts and records a one-line outcome. The call moves to "Done".
7. **Keep.** The call stays in the ledger permanently.

Other things that can happen:

| Event | Who | Effect |
| --- | --- | --- |
| Change the pick | The person, any time before the outcome | A new answer replaces the old one and is delivered again, marked as updated. |
| Dismiss | The person, while the call is in Needs you | Closes the call without a pick. The owning session is told. |
| Withdraw | The owning session, while the call is in Needs you | Closes the call. It leaves the board and stays in the history. |
| Mark "no longer applies" | The person, on a finished call | Sessions that find it in a search see that it must not be followed. Can be undone. |

**When a call is not enough to decide from.** The person opens the owning session and asks there, or dismisses the call with a note saying what was missing, and the session raises a fuller one. Asking from the call itself is a proposal, in section 13.

### 2. What a call holds

Written by the session:

| Field | Rule |
| --- | --- |
| Title | One question a person can read cold, with no ticket ids or branch names needed to understand it. At most 120 characters. |
| Context | What the person needs to know to choose. Plain text, at most 1000 characters (the existing report note limit). |
| Options | Two to four. Each has a label (at most 60 characters) and a one-line consequence (at most 200). Exactly one is marked recommended. |
| Links | Optional, up to five labelled URLs (a PR, a ticket, a document). |
| Reference | Optional short external id such as a ticket number. Searchable. |
| Story page | Optional. One self-contained page that tells the full story behind the call, for a decision that rests on a lot of evidence. See section 2a. |
| Urgency, kind, prediction | Optional. Blocking or can wait; the kind of decision; and, in a project in observe mode, what the session would have decided on its own. Added by the [autonomy-levels design delta](2026-11-18-autonomy-levels-design.md). |

Filled in by Orchestrator, never by the session: the project (id and name), the work stream (section 2c), the machine (host id and label), the owning session (id and display name at the time), and the time. The machine's label is its own hostname when the entry is written, so every device shows the same name for it. A session with no project may still raise a call; it is grouped under "No project".

Written by the person: the chosen option or "Something else", and a note of at most 1000 characters. "Something else" requires the note.

Written by the owning session afterwards: the outcome, one line, with an optional link.

A call cannot be edited once it is raised. To change its wording, options or page, the session withdraws it and raises a new one.

### 2a. Story pages

Some decisions rest on more evidence than fits in a short paragraph: several reviews, numbers, recent work. For those, a session can attach one story page to the call when it raises it.

- **A snapshot.** The page is fixed at the moment the call is raised and does not change afterwards.
- **It travels with the ledger.** The page is copied to every machine alongside the call, so it opens on any device, including offline.
- **Stored once.** A page is identified by a fingerprint of its content. Several calls can point at the same page, which is how one page that ends in three decisions becomes three calls.
- **Size cap.** About 1 MB. A larger page is refused with a message that says so.
- **Self-contained.** One HTML or Markdown file. Anything it loads from the internet may be missing when it is read offline.
- **Shown safely.** The page was written by an agent. It is displayed in the sandbox Orchestrator already uses for artifact pages and pages rendered in chat, with no access to the app, its data or its credentials. Links open in the browser.
- **The call still stands on its own.** The title, context and options must be enough to decide from, because that is what the notification, the phone and search show. The page adds depth and never replaces them.
- **Search covers the call's text,** not the page.

Orchestrator does not build these pages and does not depend on any tool to build them. The person's own setup does that: their project rules tell their sessions to build the page with their `readable-story` skill, to save it as a session artifact instead of publishing it through an outside account, and to raise one call for each decision the page ends with. Example text for a project's rules:

> When a decision for the human rests on more evidence than fits in a short paragraph, build the story with `/readable-story` first. Save the page as a session artifact; do not publish it anywhere else. Then raise one call for each decision the page ends with, attaching the page to each.

### 2b. Writing a call

These rules go into the standing instructions and the command guide (section 7). They name no tool and apply to every call, with or without a page. They are adapted from the plain-language rules of the person's `readable-story` skill.

- Write for a reader who has not seen the code, the thread or the ticket. They should be able to choose without opening anything else.
- Make the title the question itself, not a topic.
- Give every internal code, id and abbreviation a plain label. A bare ticket or PR number explains nothing.
- Use literal sentences, with no metaphors.
- State what each option leads to, including what it costs or risks, not only its name.
- Give the recommendation a "because".
- Put the unit, the time window and what it is a share of next to every number, and say where the number came from.
- Say what is not known, and mark a guess as a guess.
- When the call asks the person to approve a text, such as a prompt, a policy or a message, show that text: in the context when it is short, in the story page when it is long.
- Check that the facts are still current just before raising.
- When you mention a call anywhere else, such as in chat or in a report, use its link. Never refer to a call by a bare id or number.
- Never put secrets, credentials or personal data in a call or its page. The ledger is permanent, copied to every machine, and searchable by every session.

### 2c. Work streams

A project often has several lines of work running at once, each led by a manager session with workers under it. The board groups calls by these, so the decisions for one line of work sit together.

- **Worked out, not typed.** Orchestrator works out a call's stream from who spawned whom, the same way the Fleet view decides which sessions are managers. Sessions do not name their stream, so names cannot drift.
- **The rule.** Start at the session that raises the call and walk up through the sessions that spawned it, stopping below the project's orchestrator session. The highest session on that path that has spawned other sessions is the stream.
  - A worker under a manager belongs to the manager's stream. So does a worker two levels down.
  - A manager's own calls belong to its own stream.
  - A session with no manager above it and no sessions under it has no stream. Neither does the project's orchestrator session. Their calls sit in the project's own row.
- **Recorded when raised.** The `raised` entry carries the stream's session id and that session's name at the time. Other machines cannot see the owning machine's sessions, and sessions are deleted eventually, so the stream cannot be worked out later.
- **Named after its manager session.** If that session is renamed, the row shows the name from the stream's newest call.
- **A manager's early calls.** A session can raise a call before it has spawned anyone, so that call is recorded with no stream. Once later calls name that session as their stream, its earlier calls are shown in that stream too. This is worked out from the ledger alone, so every machine agrees.
- **A stream lives on one machine.** The same project on two machines is one group on the board, with each machine's streams as separate rows.

### 3. States

A call's state is computed from its entries. It is never stored as a fact of its own, so every machine that holds the same entries shows the same state.

Each state has one name, used everywhere: as the board's column header, in search results, and in what sessions see when they look a call up.

| State | On the board | Rule |
| --- | --- | --- |
| Needs you | Column | Raised, and no answer, dismissal or withdrawal yet. |
| In progress | Column | The latest of its answers and dismissals is an answer, and there is no outcome. |
| Done | Column | It has an outcome. |
| Dismissed | Not shown; history only | The latest of its answers and dismissals is a dismissal, and there is no outcome. |
| Withdrawn | Not shown; history only | The session withdrew it before any answer. |

Rules for the awkward orders that syncing makes possible:

- When two answers exist, the later one stands. Both stay in the history.
- An outcome or a withdrawal closes the call. An answer that arrives later does not reopen it, but it is kept and the card is flagged "changed after it closed".
- **Every answer and dismissal is delivered to the owning session whatever the call's state.** The state rules decide only what the card shows. This is what keeps a late or crossed answer from being lost.

"In progress" has three truthful sub-states, shown on the card:

- "Sent to *session*" once that machine has recorded the delivery.
- "Not delivered yet" while the owning machine has not synced the answer.
- "No session to carry this out" when the owning session has ended and nothing could take it (see section 6).

### 4. The ledger as a log of entries

The ledger is an append-only log. Each entry records one fact:

| Part of an entry | Purpose |
| --- | --- |
| Id | Globally unique. Receiving an entry a machine already holds is a no-op. |
| Origin | The host id of the machine that wrote it. |
| Origin sequence | 1, 2, 3 and so on per origin, with no gaps. Lets a machine ask a peer for "everything from origin X after number N" and notice a gap. |
| Order stamp | A logical or hybrid clock value used to order entries the same way on every machine. |
| Call id | The call the entry belongs to. |
| Kind | `raised`, `answered`, `dismissed`, `delivered`, `outcome`, `withdrawn`, `retired` (with a true or false value), and later `reassigned`, `asked` and `replied` if section 13 is built, and `decided` and `overruled` from the [autonomy-levels design delta](2026-11-18-autonomy-levels-design.md). |
| Payload | The kind's data, as JSON. |
| Written at | Wall-clock time, for display only. |
| Author | The person (with the surface: desktop, phone or their own shell) or a session (machine and session id). |

Requirements on ordering:

- Every machine orders the same set of entries identically, however and whenever they arrived.
- An entry written by a machine that had already seen another entry always orders after it.
- "Later" should match real time when machine clocks are roughly right, because the rule "the later answer stands" is read by a person. A hybrid logical clock meets all three; the exact mechanism is the builder's choice.

Each machine also keeps a derived, rebuildable table of calls (current state, chosen option, note, outcome, delivery sub-state) computed from the log, and a full-text index over titles, context, option labels, references, notes and outcomes. Both are local conveniences. Only the log and the story pages it refers to are replicated. A `raised` entry refers to its page by fingerprint and size; the page's content is not inside the entry. It also carries the call's work stream (section 2c).

**Entries are written in this final shape from the first slice,** even before sync exists, so that adding sync later changes no meaning.

### 5. Syncing between machines

**Links.** A machine syncs with the peers it has been linked to. A link is created explicitly, one machine at a time; a machine never starts syncing with another just because the app has it saved.

- From the command line on any machine: give the peer's address and credential (the same address and credential a device uses to reach that machine).
- From the desktop app: for a machine already saved in the app, a switch to sync decisions with it. The app's main process hands the saved address and credential to the local daemon. The renderer never sees the credential.

A link is configured on one side but each exchange moves entries in both directions. A laptop that can reach the always-on machine therefore takes full part, even though nothing can reach the laptop.

**Relay.** A machine passes on entries it received from others, not only its own. Linking every machine to the always-on machine is therefore enough for all of them to converge. That machine is not special: any machine can be linked to any other, and every machine answers searches from its own copy.

**The exchange.** The initiating machine tells the peer the highest origin sequence it holds for each origin. The peer returns the entries the initiator lacks and its own list. The initiator then sends the entries the peer lacks. Requirements:

- Idempotent, and safe to repeat or interrupt.
- Paged, so a machine that was off for weeks catches up without one huge request.
- Each received entry is validated (shape, size limits, sequence continuity) before it is stored.
- A peer that is unreachable is skipped with a bounded timeout and never blocks anything else.
- Story pages that entries refer to are transferred too, by fingerprint, and checked against it on arrival. Until a call's page has arrived, a machine shows the call without it and says the page is still syncing.

**When.** Shortly after any local write, on a timer of about a minute per peer, when the daemon starts, and on request.

**Different versions.** Machines can run different Orchestrator versions. A machine must store and pass on entry kinds and payload fields it does not understand, and ignore them when computing states. A newer machine must never be prevented from syncing by an older one.

**Freshness.** Each machine records, per peer, when the last exchange succeeded and whether both sides ended up with the same entries. The Decisions page shows this in plain words, for example "In sync with mac-mini and laptop · vm-2 last synced 2h ago".

**Trust.** Holding a peer's credential allows writing any entry, including answers. That is acceptable only because every machine belongs to the one person. It must be stated in the setup documentation.

### 6. Delivering an answer to a session

The machine that hosts the owning session is responsible for delivery.

- It watches for answers and dismissals on calls it owns that have no matching `delivered` entry. This applies whether the answer was written locally or arrived by sync.
- It sends the session a message through the existing session message path, marked as coming from Orchestrator on the person's behalf. The message states the call's title, the chosen option and the note, and reminds the session to record the outcome.
- When the message is accepted, the machine writes a `delivered` entry naming the session that received it. One answer is delivered once, including across daemon restarts. A durable outbox like the report coordinator's is the expected pattern.
- A changed pick is a new answer and is delivered as an update.

**When the owning session has ended:** "ended" means the session is terminated, or the message path reports that it cannot take a message. A session that is merely idle or busy is not ended; the existing message path already queues for a busy session. Orchestrator never restarts a terminated session to deliver an answer.

In that case the answer goes to the project's active orchestrator session on that machine, the same recipient that already receives that worker's reports. If there is none, the card shows "No session to carry this out". The decision is still recorded and searchable. The person can record the outcome themselves to close the call, for example when they did the work by hand or it no longer needs doing. Handing the call to another session is a later slice.

### 7. What sessions can do

A small command group, a thin client over the local daemon, usable by any agent and working offline against the machine's own copy:

| Action | Notes |
| --- | --- |
| Search decisions | Keywords. Results list the title, state, chosen option, note, outcome, date, project, work stream, machine, and whether it still applies. Same-project results come first; decisions that still apply come before retired ones. Has machine-readable output. |
| Raise a call | Only from a session. One command. Orchestrator fills in project, work stream, machine and session. Can attach a story page from the session's artifacts. Returns the call's id and its link. A call that breaks a rule in section 2 is refused with a message that names the rule. |
| List calls | The session's own, or all that need the person. The session's own list marks the answers it has not yet recorded an outcome for. |
| Show a call | Everything about one call, including its history. |
| Record the outcome | Only the owning session, or the person. |
| Withdraw a call | Only the owning session. |

The same command group gives the person, from their own shell, what the app gives them: answer, dismiss (with an optional note), record an outcome, mark "no longer applies", and manage links (add, list, remove, sync now). These are refused from an agent's shell (section 8). Removing a link stops syncing with that peer; entries already copied stay on both machines.

If a search finds a matching call that still needs the person, the session does not raise a second one. It notes the existing call and checks it again later, because the answer is delivered only to the session that raised it.

**Raise at the source.** The session that has the question raises the call. A manager or orchestrator session that is brought a question by a worker does not pass it up in its own words. It tells the worker to raise a call, or, if the worker has ended, raises one itself with the full context. No session asks the person for a ruling by id. The answer goes straight back to the session that raised the call, not down through the sessions above it. (From the team call: relayed questions were reaching the person as a bare id.)

**Links.** Every call has an in-app link, in the same family as session links, that opens the Decisions page with that call open. Sessions use it whenever they mention a call.

**After losing context.** A session can forget a call when its context is compacted or it restarts. Sessions are told to list their own calls when they resume, so they see what they asked, what was answered, and which outcomes they still owe.

**A reminder on the old path.** When a worker files a "needs input" report, the command prints one line reminding it to raise a call instead if the blocker is a choice between options. (From the team call: agents are slow to ask for help in the right place.)

Sessions also need to be told to use them. The standing instructions that every worker and orchestrator session receives gain a short section: search before asking; follow a decision that still applies and say which one; otherwise raise a call with a plain-language question, context, options and a recommendation; carry on with work that does not depend on it; when the answer arrives, act on it and record the outcome in one line. It also covers raising at the source, using a call's link, and listing your own calls after a restart, and it carries the writing rules in section 2b. A command guide is added beside the existing ones.

The existing "needs input" report stays for blockers that are not choices, such as missing access. The instruction that currently sends decisions there is changed to point at calls.

### 8. Who may answer

Only the person answers, dismisses or retires a call.

- Those actions are accepted from the desktop app, the phone, and the person's own shell.
- A request that carries a calling session, which is how a command issued from an agent's shell arrives, is refused with a clear message.
- Each answer records the surface it came from, and the history shows it.

This is a guard rail, not a security boundary. The local listener is unauthenticated by design, so an agent with a shell could forge a request. The guard stops accidents and well-behaved agents, and the recorded surface makes a forged answer visible afterwards.

### 9. Notifications

- A new notification kind, shown as "Decision needed · *project*", followed by the work stream when the call has one, with the call's title as its text.
- It stays unresolved until the call leaves "Needs you", whichever machine or device the answer came from.
- Opening it goes to the Decisions page with that call open (desktop) or to the call screen (phone).
- **One notification per call per device,** even though every machine in the mesh learns of the call. A device that can reach any machine in the mesh should hear about every call. Suggested approach: every machine raises the notification for each call in Needs you that it learns of, keyed by the call id, so the desktop merges by key and the phone push uses the call id as its collapse key. This needs a short spike on real devices; see Risks.
- No notification when a call is answered or finished. The board shows that.

### 10. The Decisions page (desktop)

A new page for all calls from every project and machine. It reads the local machine's copy of the ledger and writes answers to the local machine, which syncs them out.

```
 Search         │ Decisions                             [ Search all decisions ]  (bell)
 Automations    ├──────────────┬─────────────────┬─────────────────┬────────────┬──────────────────────────────────────
 Decisions   3  │              │ ● Needs you 3   │ ● In progress 2 │ ● Done 2   │ Which auth scheme for
 ─────────────  ├ ▾ skyvern-cloud ───────── 2 need you · 1 in progress · 2 done ┤ the Nango proxy?
 Projects       │              │                 │ ┌ Retry policy  │            │ skyvern-cloud · Power BI and Nango
  skyvern-cloud │              │                 │ └ Sent to 5498  │            │ mac-mini · worker 5512 · waiting 3h
  agent-orch…   ├──────────────┼─────────────────┼─────────────────┼────────────┤
                │ Skyplane     │ ┌ Ship Skyplane │                 │ ┌ Where to │ The Power BI connector needs to
                │ connectors   │ │ dark or flag? │                 │ │ store …  │ call Nango for each tenant …
                │              │ └ Waiting 1d    │                 │ └ Migrated │
                ├──────────────┼─────────────────┼─────────────────┼────────────┤ (•) OAuth client credentials
                │ Power BI     │ ┌ Which auth    │                 │ ┌ Power BI │     Recommended
                │ and Nango    │ │ scheme …      │                 │ │ refresh  │ ( ) Per-tenant API keys
                │              │ └ Waiting 3h    │                 │ └ PR #412  │ ( ) Defer until Power BI ships
                ├ ▾ agent-orchestrator ──────────── 1 needs you · 1 in progress ┤ ( ) Something else
                │              │ ┌ Drop Node 18? │ ┌ Rename flag?  │            │ [ note for the session ]
                │              │ └ Waiting 20m   │ └ Sent to 4     │            │ [Send decision]  Open session  Dismiss
                ├──────────────┴─────────────────┴─────────────────┴────────────┤
                │ In sync with mac-mini and laptop · vm-2 synced 2h ago         │
```

- **Getting there.** A "Decisions" row in the sidebar beside Search and Automations, with the number of calls in Needs you, in the needs-you colour.
- **Columns.** The three state names: "Needs you", "In progress", "Done". Each header has its semantic dot, sentence-case name and count. "Done" shows the last 7 days.
- **Rows.** Grouped by project, then by work stream (section 2c).
  - Each project is a band across the board with its name and its count per state. A band can be collapsed.
  - Inside a band, each work stream is a row with the stream's name at its left. Calls with no stream sit in the project's own row, which comes first and has no name.
  - The same project on two machines shares one band. The machine is on the card.
  - Bands with calls in Needs you come first, and so do rows inside a band. A project or stream with nothing to show is not drawn.
  - When the pane is too narrow for the name column, a stream's name moves above its row.
- **Cards.** Title first. Then one line of who or what: the machine and session while it needs the person, the chosen option afterwards. Then one status line: how long it has waited, the delivery sub-state, or the outcome. Needs you is oldest first; the other columns newest first. Only Needs you uses the needs-you colour.
- **Call panel.** Selecting a card opens it beside the board, in the shell's inspector position. Its heading names the project, the work stream, the machine and the session, so the person knows which line of work the call belongs to before reading it.
  - Needs you: context, links, options with the recommended one marked, "Something else", a note field, then Send decision, Open session and Dismiss.
  - In progress: the same, with the chosen option selected and the delivery sub-state. Sending again changes the pick. The person can also record the outcome here, which closes the call.
  - Done: the chosen option, note and outcome, and the "no longer applies" action.
  - Always: the call's history, with who did what, when, and from which device.
  - Dismiss takes an optional note. Open session works only when the app is connected to the machine that hosts the owning session, matched by host id; otherwise it is disabled and says why.
  - When the call has a story page, the panel shows "Read the story", which opens the page in Orchestrator's existing page viewer.
- **Search.** Covers every call ever raised, including dismissed, withdrawn and old finished ones. Results replace the board with a list that shows each call's state, project and work stream.
- **Footer.** Sync freshness in plain words. With no links, a short prompt to link machines.
- **Loading, empty and error views.** Loading, nothing in Needs you, nothing at all yet (with one line on how sessions raise calls), the local daemon unreachable, and a peer that has not synced.
- **Keyboard.** Arrow keys move between cards, Enter opens the panel, Escape closes it. Focus is visible throughout.

### 11. Phone

- A push for each new call. Tapping it opens the call: context, options, note, Send decision.
- A Decisions entry that lists the calls in Needs you and recently answered ones, grouped by project, with each call's work stream on its row. There is no board on the phone.
- A call's story page opens in the phone's existing page viewer.
- The phone reads and writes through whichever machine it is connected to. Each holds a full copy.

### 12. Indicator on the sessions board (optional, last)

A session with a call in Needs you could show that it needs the person on its card on the sessions board. It must be derived at read time from the ledger, not stored, and it must fit the one status line a card has. It is deferred because it touches the board's carefully derived status rules.

### 13. Asking for more from a call (proposed, not yet chosen)

In the design above, when a call is not enough to decide from, the person opens the owning session and asks there, or dismisses the call with a note. This section proposes a third way. The person has not chosen it. It is its own slice and can be left out without touching the rest.

The reason for it is the flow described in the team call: the person gets a short ask, and says "do a readable story on it" only when they need more. It also reaches a session on a machine the app is not connected to, which opening the session cannot.

- **Ask.** From the call panel or the phone, the person types a question or presses "Ask for the full story". It is recorded on the call and delivered to the owning session the way an answer is (section 6), with the same fallback when that session has ended.
- **Reply.** The owning session replies on the same call with text of at most 1000 characters, a story page (section 2a), or both.
- **State.** The call stays in Needs you. Its card says "You asked for more" until the reply arrives.
- **Told again.** When the reply arrives, the person is notified again.
- **Kept.** Questions and replies stay in the call's history, and their text is searchable.
- **Not a gate.** The person can answer the call at any point, with or without a reply, and can ask more than once.
- **Agent-neutral.** "Ask for the full story" sends a plain request for a story page. How the page gets built is up to the person's project rules, as in section 2a.

## Decided by the person

| # | Decision |
| --- | --- |
| 1 | The feature lives inside Orchestrator, needs no outside account, and works with any agent. |
| 2 | It serves one person: on the desktop, on the phone, and across several machines. No teammates. |
| 3 | The ledger holds one kind of item: a call with options. |
| 4 | The record is a history the person can search and that sessions can read. |
| 5 | A session must be able to find every decision, from any project and any machine. |
| 6 | One of the person's machines is always on. |
| 7 | Every machine holds a full copy and they sync peer to peer, in preference to a single home machine. |
| 8 | The notification bell and phone push announce new calls. A swimlane board page is where all calls are managed. No separate inbox page. |
| 9 | The life of a call and its edge rules, as in section 1. |
| 10 | A call's states are named Needs you, In progress and Done. The board's columns use those same names; there is no separate column wording. |
| 11 | A call can carry an attached story page when the decision rests on a lot of evidence; small calls stay short text. The plain-language rules of the `readable-story` skill apply to every call. Orchestrator stays agent-neutral, and the person's project rules tell their sessions to use the skill. |
| 12 | The board groups calls by project first, then by work stream inside each project. |
| 13 | Autonomy levels (2026-11-18): per project, the person reserves kinds of decision; sessions record what they decide for the rest, with how to undo it, and the person can overrule. It ships observe-only first, adds urgency to calls, and makes an answer recorded in the ledger binding on every session. See the [autonomy-levels design delta](2026-11-18-autonomy-levels-design.md). |

**Still open for the person:** whether to add asking for more from the call itself (section 13). Until it is chosen, it is a proposal and not part of the agreed design.

## Calls the designer made

The person approved the overall shape; these details were filled in by the designer and should be checked while reading.

| # | Call | Reason |
| --- | --- | --- |
| a | When the owning session has ended, the answer goes to the project's orchestrator session. In conversation this was described as "the session that spawned it". | The repo treats spawn lineage as history, not as a reporting line (`domain/session.go`), and already sends a worker's reports to the project's orchestrator. |
| b | Exactly one option must be marked recommended. | A session that asks should have a view; the person is free to ignore it. |
| c | "Something else" is always offered and requires a note. | The person often wants an answer outside the options. |
| d | Every answer is delivered to the owning session even if the call has closed. | A crossed or late answer must not be lost. |
| e | The Done column shows 7 days; search reaches everything. | Keeps the board short without hiding history. |
| f | A project's band is shared across machines by project id. A work stream belongs to the one machine its sessions run on. | A decision is about the project, not the machine it was raised on, but a manager and its workers run on one machine. |
| g | The desktop reads its own machine's copy, not a live merge across machines. | Every machine has the whole ledger, so a merge adds complexity and nothing else. |
| h | Linking is explicit per machine. | A saved machine is not necessarily one the person wants the ledger on. |
| i | No notification when a call is answered or finished. | The bell is for what needs the person. |
| j | Raising a call does not also file a report to the orchestrator. | Orchestrators can list the calls in Needs you when they need to. |
| k | "No longer applies" can be undone. | Mistakes happen and the log makes it cheap. |
| l | The old page's items are not imported. | The person did not ask for it; say so if it is wanted. |
| m | The sessions-board indicator is deferred to the last, optional slice. | It touches derived status rules that deserve their own change. |
| n | Call text is kept out of telemetry. | Decisions can be sensitive. |
| o | A story page travels with the ledger to every machine, as a snapshot capped at about 1 MB. | A big call is the one where the person most needs the page in front of them, on any device. It also matches "every machine holds the full ledger". |
| p | A call cannot be edited after it is raised; the session withdraws it and raises a new one. | Keeps entries simple and the history honest. |
| q | Search covers a call's text, not its page. | The call's text has to stand on its own anyway. Page search can come later. |

### Added from the team call

| # | Call | Reason |
| --- | --- | --- |
| r | A work stream is the highest session above the raising session, below the project's orchestrator, that has spawned other sessions. It is recorded when the call is raised. | It is how the Fleet view already identifies managers, it needs nothing typed by a session, and it survives sync and deleted sessions. |
| s | Calls with no stream sit in the project's own unnamed row, not in one row per session. | A fleet with no managers would otherwise get a row per worker. |
| t | Managers and orchestrators never relay a question. The session with the context raises the call, and the answer goes straight back to it. | Relayed questions were reaching the person as a bare id. |
| u | Every call has an in-app link, and sessions use it instead of an id. | The person should land on the call, with its context, in one click. |
| v | A malformed call is refused with a message that names the broken rule. | Workers often run on cheaper models. The command keeps calls well formed, not the model. |
| w | Filing a "needs input" report prints a one-line reminder about calls. | Agents are slow to ask for help in the right place. |
| x | Sessions list their own calls when they resume. | A compacted or restarted session would otherwise forget what it asked and what it owes. |
| y | The ledger is not a general memory or log for agents. | Sessions already keep their own notes. The gap is the few questions that need the person. |
| z | Asking for more from the call is written up as a proposal (section 13), not as part of the agreed design. | The person has not chosen between it and the two ways the design already has. |

## Left to the builder

- Names of tables, routes, commands and flags, and exact request and response shapes.
- The clock mechanism, within the ordering requirements in section 4.
- Whether a sync exchange is one request or two, page sizes, timers and backoff.
- Where peer credentials are stored, within the custody rule (under `~/.ao`, protected like `remotes.json`, never in the renderer, logs or telemetry).
- How a machine that only accepts per-device credentials issues one to a peer machine.
- Full-text search details and result ranking beyond "same project first, still-applies first".
- Component breakdown, keyboard shortcuts beyond those listed, and empty-state wording.
- The exact wording of the standing instructions and the command guide.
- Test structure.
- The accepted page types and the exact size cap, within "one self-contained page of about 1 MB".
- How pages are stored on disk and moved during sync.
- The exact form of a call's link, within "the same family as session links".
- The wording of the reminder printed by the "needs input" report, and of the messages for a refused call.

## Suggested implementation slices

Each slice is useful on its own and leaves the product working.

| # | Slice | Delivers | Depends on |
| --- | --- | --- | --- |
| 1 | Ledger core on one machine | The entry log in its final shape, the derived calls table, search index, daemon routes, the session command group with its refusal messages, the work stream on each call, the answer guard, delivery of answers into sessions with the orchestrator fallback, the standing instructions and guide, and the reminder on the "needs input" report. Usable end to end from a terminal. | Nothing |
| 2 | Notification | The "Decision needed" kind, its resolution, how it reads in the bell, and its push text. | 1 |
| 3 | Decisions page | The sidebar row, the board grouped by project and work stream, the call panel, opening a call from its link, search, history, and all states, for the local machine. | 1 |
| 4 | Mesh sync | Links (command line and the app's switch), the exchange, relay, version tolerance, freshness, and the page footer. | 1 |
| 5 | Phone | The call screen, the Needs you list, the notification deep link, and opening a call from its link. | 2 |
| 6 | Story pages | Attaching a page when raising, storing it once by fingerprint, "Read the story" in the call panel and on the phone, and carrying pages in sync. | 1 and 3; carrying pages between machines needs 4 |
| 7 | Ask for more (proposed) | Section 13: the person's question from the call panel and the phone, its delivery, the session's reply, the "You asked for more" line on the card, and the second notification. | 1, 2 and 3; replying with a page needs 6 |
| 8 | Later, optional | Handing a call to another session, the sessions-board indicator, and an inbox page if it turns out to be wanted. | 3 |

Slices 1 to 3 replace the published page on a single machine. Slice 4 extends it to every machine. Slices 2, 3 and 4 do not depend on each other and can be built in parallel once slice 1 has landed.

The writing rules (section 2b) ship with slice 1. The `raised` entry carries its optional page reference from slice 1, so slice 6 adds storage and display without changing what an entry means. It carries the work stream from slice 1 too.

## Testing notes

- **State computation:** feed the same entries in every order and assert the same result. Cover two answers, an answer after an outcome, an answer after a withdrawal, and retire then un-retire.
- **Sync:** three machines in a line (A linked to B, B linked to C) converge; a machine that was off catches up in pages; repeating or interrupting an exchange changes nothing; an unknown entry kind is stored, passed on and ignored.
- **Auth:** the sync route is refused without a credential on the authenticated listener.
- **Delivery:** one answer is delivered once across a daemon restart; a changed pick is delivered as an update; an ended session falls back to the orchestrator; no orchestrator yields the "no session" sub-state.
- **Answer guard:** a request with a calling session is refused; the surface is recorded.
- **Notifications:** one per call per device with two machines connected; resolves when the call is answered from the other machine.
- **UI:** dark and light themes, a narrow centre pane, long titles, keyboard-only use, and a change arriving while the panel is open.
- **Story pages:** a page over the cap is refused; two calls sharing a page store it once; a page arrives intact on a second machine and matches its fingerprint; a call shows without its page until the page arrives; the page cannot reach the app from its sandbox.
- **Work streams:** a worker under a manager lands in the manager's row, and so does a worker two levels down; a lone worker and the project's orchestrator land in the project's own row; a manager's early call joins its stream once the stream exists; a second machine shows the same rows after sync.
- **Raising:** each broken rule is refused with a message that names it; the printed link opens the call.

## Risks

| Risk | Why it matters | What to do |
| --- | --- | --- |
| One push per call across machines | Each machine's push dispatcher is independent, so two machines can each send one. | Spike collapse keys on real iOS and Android devices early in slice 2. If they do not hold, let only the machine that raised the call send the push and accept that a phone connected to other machines misses it. |
| Notifications assume a local session | Today a notification record points at a session and project on the same machine. A call synced from another machine has neither locally. | Decide in slice 2 how the record refers to a call from elsewhere before building the bell row; do not invent a placeholder session. |
| Keyword search misses a paraphrase | The session asks again, which is the main anti-goal. | Tell sessions to search with more than one phrasing; put same-project decisions first; review real misses before considering anything heavier. |
| A stale decision misleads a session | The session follows something that no longer applies. | Results always show date, project and the "no longer applies" flag; the instructions tell sessions to say which decision they followed. |
| Version skew between machines | An older machine could drop or reject newer entries. | The store-and-pass-on rule in section 5, with a test. |
| Two machines sharing one identity | A copied data directory would write two different entries under one origin and sequence. | Detect "same origin and sequence, different entry", stop syncing that origin, and show the error. |
| Machines that require per-device credentials | The simple "address and password" link does not work for them. | Design the link flow to obtain a device credential; note the limitation if it lands later. |
| The answer guard is not a boundary | An agent could forge an answer through the local listener. | Record and show the surface of every answer. Do not describe the guard as security. |
| Story pages make the ledger grow | Pages are kept for good on every machine. | The size cap, and storing each page once. Look at real sizes before considering any cleanup. |
| Story pages are written by agents | A page is untrusted HTML, and it now reaches every machine. | Always display it in the existing sandbox. Never give it access to the app. |
| Sessions still do not ask, or ask in the wrong place | A question that never becomes a call is invisible to the person. | The standing instructions, the rule against relaying, and the reminder on the "needs input" report. Review real misses after slice 1 before adding anything heavier. |
| Work streams need Orchestrator sessions | A fleet run as sub-agents inside one session raises every call as that one session, so there are no streams to show. | Say so in the guide. Those calls sit in the project's own row. A stream named by the session can be added later if it is needed. |
