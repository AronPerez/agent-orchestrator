# Spec: Event-triggered session creation (AO-2)

**Linear:** [AO-2 — Event-triggered session creation (Linear, Slack, GitHub)](https://linear.app/starstruck/issue/AO-2/event-triggered-session-creation-linear-slack-github)
**Status:** proposed — no implementation in this document
**Related:** [`docs/architecture.md`](../architecture.md), [`docs/remote-sessions-edd.md`](../remote-sessions-edd.md)

---

## Problem

Sessions are created by hand. Events that *should* start work — a Linear ticket
assigned to you, a Slack thread handed to you, a Dependabot alert — do not.

### What already exists (and is easy to miss)

`backend/internal/observe/trackerintake` is a **shipped, opt-in issue-intake
observer**. Per project, when `trackerIntake.enabled` is set, it:

- polls `ports.Tracker.List` every **60s** for open issues matching
  `trackerIntake.assignee` (`*` = any assignee, `none` = unassigned),
- spawns one `KindWorker` session per unseen issue with a prompt built from the
  issue's title/URL/labels/assignees/body, capped at 4096 bytes,
- dedups against `sessions.issue_id`, stored canonically as `provider:native`,
- backs a failing project off for **5 minutes** rather than hammering it.

So "GitHub issue assigned to me → session" already works. AO-2 is not a
greenfield feature; it is four gaps in an existing loop.

### The four real gaps

1. **Only GitHub is reachable.** `domain.TrackerIntakeConfig.Validate()` accepts
   `provider: "gitlab"` and a full GitLab tracker adapter exists — but
   `startTrackerIntake` hardcodes `SingleTrackerResolver{Provider: github}`
   (`backend/internal/daemon/tracker_intake_wiring.go:28`). A GitLab project that
   configures intake validates fine, fails to resolve an adapter, and drops into the
   five-minute failure backoff on every sweep — forever. This is a latent bug,
   not just a missing feature.
2. **No Linear adapter.** No `domain.TrackerProviderLinear`, no adapter package.
3. **Dependabot and code-scanning alerts are not issues.** They live behind
   separate GitHub APIs and have no assignee, so neither the current adapter nor
   the current eligibility rule can express them.
4. **Slack has no assignment primitive at all.** There is no Slack object that
   maps onto `ports.Tracker` without inventing one.

And one structural gap underneath all four: intake scope is derived from the
project's **git origin** (`trackerRepo` → `parseRepoNative`). That is correct for
GitHub/GitLab and meaningless for Linear (team-scoped) and Slack (channel-scoped).

### The constraint that decides the transport

The AO daemon binds loopback, with an optional LAN listener that is
credential-gated and documented as trusted-network-only (see the remote-sessions
EDD). It has **no public URL**. Linear, Slack, and GitHub cannot deliver webhooks
to it without a tunnel AO does not own and should not grow.

**Therefore: polling is the transport.** Every provider below is a poll adapter
behind the existing `ports.Tracker` port. This is not a compromise to apologise
for — a 60-second sweep is well inside the latency budget of "an agent starts
working on my ticket", and it costs no inbound attack surface. Anything that
genuinely requires push delivery is out of scope for the desktop daemon and
belongs to a future hosted relay, which does not exist and is not proposed here.

---

## Proposed design

One intake loop. New providers behind the port that already exists. No event
bus, no webhook receiver, no second observer.

### Phase 1 — Make the existing loop honest (near-zero new concepts)

Replace `SingleTrackerResolver` with the existing `adapters/tracker/multi`
dispatch in `tracker_intake_wiring.go`, and give each provider its own lazy token
source following the `trackerTokenSource` pattern already there. GitLab intake
starts working; every later provider plugs into the same resolver.

Ship the safety rails in the same phase (see Risks — they are why this phase is
first):

- **Per-tick spawn cap** per project, and a **max concurrent intake sessions**
  cap. The current loop has no cap because a human assignee filter bounded it
  implicitly; that assumption dies the moment alerts arrive.
- **A durable "handled" record.** `seenIssueIDs` only counts *non-terminated*
  sessions (`observer.go`), so killing an intake session for a still-open issue
  makes it respawn within 60 seconds. Today that is a sharp edge; with more
  providers it is a loop. Record that an issue was taken, independently of
  whether its session still lives.

### Phase 2 — Linear provider

New `backend/internal/adapters/tracker/linear/` implementing `ports.Tracker`
over Linear's GraphQL API, reusing `adapters/tracker/httpkit` for pagination,
rate-limit errors, and the preflight cache. The GitHub and GitLab adapters are
~500 lines each; expect the same order.

Mapping decisions, all of which are contract-level and belong in review:

| AO concept | Linear |
| --- | --- |
| `TrackerID.Native` | issue identifier, e.g. `AO-6` |
| `TrackerRepo.Native` | **team key**, e.g. `AO` — not a repo |
| `TrackerID.Host` | unused (Linear is single-tenant SaaS) |
| `Issue.State` | workflow state *type* → normalized: `backlog`/`unstarted` → `open`, `started` → `in_progress`, `completed` → `done`, `canceled` → `cancelled` |
| `ListOpen` | anything not `completed`/`canceled` |
| `Issue.Assignees` | assignee **email** — it must be a value a user can type into the `assignee` config field |
| Auth | `AO_LINEAR_API_KEY` (env only; Linear has no `gh auth token` equivalent) |

`trackerRepo()` must **not** fall back to the git origin for Linear: a repo URL
cannot name a Linear team. Require an explicit `trackerIntake.repo` and fail the
project's config validation without one, rather than silently polling nothing.

### Phase 3 — GitHub Dependabot / code-scanning alerts

Alerts are not issues, so this is a **separate provider id** (`github-alerts`),
not a mode of the GitHub tracker. Keeping them separate is what lets a project
take issue intake without also draining its alert backlog.

- Project an alert into `domain.Issue`: `Native = "owner/repo!dependabot/42"`,
  severity as a label, advisory summary as the body. Canonical id dedup works
  unchanged.
- **Eligibility cannot be `assignee`** — alerts have none. This provider needs
  its own rule (minimum severity, optionally an ecosystem filter), which means
  `TrackerIntakeConfig.Validate()`'s current unconditional "assignee is required
  when enabled" must become per-provider. That rule exists to stop an accidental
  backlog drain; the alerts provider must replace it with an equivalent, not
  drop it.
- **One provider per project is the current shape.** Wanting issues *and* alerts
  on one project means `trackerIntake` becomes a list. That is a JSON config
  change with no DB migration (project config is stored as JSON), and it should
  accept both the object and array forms on read. Do it only when someone
  actually wants both; note the limitation in the meantime.

A cheaper alternative worth offering in the docs regardless: GitHub can already
open issues from Dependabot alerts natively, and the existing GitHub intake will
pick those up with **zero AO code**. Some users will prefer that.

### Phase 4 — Slack: recommend the CLI, not an adapter

Slack has no "thread assignment" object. The options are:

- **(a) A Slack workflow or bot that shells out to `ao spawn`.** Zero AO code.
  The CLI already exists, already takes `--prompt`, and can target a specific
  host via `--url` with the LAN connection password. The user defines what
  "assigned" means for their team.
- **(b) A real Slack tracker adapter** polling for a marker (an emoji reaction,
  a bot mention) via `conversations.history`. This means a Slack app, OAuth
  scopes, token custody, and a channel-scoped notion of `TrackerRepo` — the most
  expensive provider here, for the least well-defined trigger.
- **(c) A generic inbound trigger endpoint on the daemon.** Rejected: it is a new
  authenticated write surface on a loopback daemon whose trust model the
  remote-sessions EDD deliberately keeps narrow, and it duplicates the CLI.

**Recommendation: ship (a) as a documented recipe.** Revisit (b) only if a
concrete team tries (a) and it is genuinely too clumsy. Building a Slack adapter
before that evidence exists is building a trigger nobody has defined yet.

---

## Touched components

| Area | File | Change |
| --- | --- | --- |
| Domain | `backend/internal/domain/tracker.go` | `TrackerProviderLinear`, `TrackerProviderGitHubAlerts`; per-provider eligibility validation; spawn caps |
| Intake loop | `backend/internal/observe/trackerintake/observer.go` | Spawn caps, durable "handled" record, provider-aware scope resolution (no git-origin fallback for non-SCM providers) |
| Prompt building | `backend/internal/observe/trackerintake/observer.go` (`BuildIssuePrompt`) | Mark issue-sourced text as untrusted data (see Risks) |
| Wiring | `backend/internal/daemon/tracker_intake_wiring.go` | `multi` resolver; per-provider lazy token sources |
| New adapters | `backend/internal/adapters/tracker/linear/`, `.../githubalerts/` | New packages implementing `ports.Tracker` over `httpkit` |
| Dispatch | `backend/internal/adapters/tracker/multi/tracker.go` | Register new providers |
| Storage | `backend/internal/storage/sqlite/` | Durable intake-handled record (new table or a column on the existing intake path) |
| API contract | `backend/internal/httpd/apispec`, `frontend/src/api/schema.ts` | Regenerate (`npm run api`); enforced by `api-drift` CI |
| Settings UI | `frontend/src/renderer/components/ProjectSettingsForm.tsx`, `CreateProjectAgentSheet.tsx`, `frontend/src/renderer/i18n/*.json` | Provider select, scope field, per-provider eligibility inputs |
| Docs | `frontend/src/landing/content/docs/configuration/projects.mdx` | Provider reference, scope semantics, the Slack `ao spawn` recipe |

---

## Risks

**Runaway spawn is the headline risk.** Every intake session is a git worktree, an
agent process, and a token budget. A repo with 200 open Dependabot alerts, or a
Linear team filter typed as `*`, spawns 200 sessions on the first tick. The
current loop has no cap. Caps and the durable handled-record are therefore
Phase 1, before any new provider — a provider added first would ship the
detonator without the fuse.

**Prompt injection is the security risk, and it is already live.**
`BuildIssuePrompt` interpolates issue title and body verbatim into a worker's
initial task, and worker sessions often run in bypass permission mode. Anyone
who can file or edit an issue in a watched tracker can therefore write
instructions into an autonomous agent's prompt. Expanding to Linear and Slack
widens who "anyone" is. Positions worth deciding explicitly in review: intake
sessions should not default to bypass permissions, and the prompt should fence
tracker-sourced content as untrusted data rather than presenting it as the
operator's instructions.

**Duplicate sessions across machines.** With multi-host federation, two laptops
running intake on the same project both spawn for the same Linear issue — `seen`
is per-daemon and the EDD explicitly rules out daemon-to-daemon federation, so
there is no shared dedup to lean on. No clean fix inside this design. Mitigation
is honesty: intake is opt-in per project *per machine*, and the docs must say
that enabling it on two machines doubles the sessions.

**Credential custody.** A Linear API key (and any Slack token) would live in the
environment. AO has no OS keychain integration — the remote-sessions EDD records
this as a known limitation for `remotes.json`, and this feature inherits it. New
secrets must at minimum be excluded from logs, consistent with the existing
no-secrets rule in the remote path.

**Rate limits and API budgets.** `httpkit.RateLimitError` exists and the GitHub
adapter uses it. Linear's API is complexity-budgeted rather than request-counted,
so a naive `List` that over-fetches fields can exhaust the budget in a way a
request counter would not predict. Budget-aware queries are an adapter-level
requirement, not an afterthought.

**Provider state-mapping drift.** `NormalizedIssueState` is a deliberately closed
five-value vocabulary — adding a value is a port-level decision every adapter
must implement. Linear's per-team custom workflow states must be mapped through
their state *type*, never their name, or a renamed state silently changes
eligibility.

**Config surface growth.** `trackerIntake` currently means "GitHub issues".
After this it means five different things depending on `provider`, with fields
that apply to some providers and not others. The UI has to make invalid
combinations unreachable rather than merely invalid, or the failure mode is a
project that validates and quietly never polls.

---

## Suggested PR breakdown

| # | PR | Size | Notes |
| --- | --- | --- | --- |
| 1 | Intake safety rails: per-tick spawn cap, max concurrent intake sessions, durable "handled" record | M | Fixes existing respawn-after-kill behaviour; ships alone, no new providers |
| 2 | Prompt-trust hardening: fence tracker text as untrusted, decide the default permission mode for intake sessions | S | Security item; applies to the GitHub intake already shipped |
| 3 | `multi` resolver wiring + per-provider token sources | S | Makes the already-validated `provider: gitlab` actually work |
| 4 | Provider-aware config: scope semantics, per-provider eligibility validation, no git-origin fallback for non-SCM providers | M | Unblocks 5 and 7 |
| 5 | Linear tracker adapter + tests | M | Mirrors `adapters/tracker/gitlab` in shape and test depth |
| 6 | Linear wiring + settings UI + docs | S | |
| 7 | `github-alerts` provider (+ `trackerIntake` as a list, only if both-at-once is actually wanted) | M | Depends on 1 for the caps |
| 8 | Slack: document the `ao spawn` recipe | XS | No code |

**Verification per PR:** `go test ./...` and `go test -race`; `golangci-lint`;
`npm run api` with `api-drift` green for any contract change; frontend tests and
typecheck for UI PRs. PRs 1 and 7 additionally need a manual check against a
repository with a deliberately large open backlog — the cap is the whole point,
and a cap that is never exercised is a cap that does not work.
