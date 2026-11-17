# 7. A route-filtered credential for cross-machine agent coordination

Date: 2026-11-17
Status: Accepted

## Context

The desktop already reaches several AO daemons and the CLI can point one
invocation at a remote daemon. Cross-machine agent coordination lets a session
use those same saved hosts to list sessions, start a worker, and send it a
message.

Giving a headless agent the existing saved-host password, account token, or
ordinary device token would give it the entire app API directly. A coordination
client needs only a small HTTP surface, and a direct route filter prevents
accidental use of unrelated endpoints. Desktop credentials are also encrypted
with the OS keychain where available, while a headless CLI cannot ask
Electron's `safeStorage` to decrypt one.

That filter is not an authority sandbox. Starting an agent or sending one a
prompt delegates to a process running with the user's OS authority. Such an
agent can reach the intentionally unauthenticated loopback daemon or operate on
the host directly. A coordination credential therefore carries full transitive
host authority even though its own HTTP requests are route-filtered.

This work must not turn AO daemons into peers. The remote-sessions design keeps
fan-out in a client, and the upstream session-transfer and cross-machine-search
work follows the same boundary: a desktop or CLI contacts each daemon; daemons
do not contact each other.

## Decision

Add an `agent-coordination` route capability to device credentials. A password,
account token, or ordinary unscoped device token may exchange its existing
authority once for a new coordination token. The host returns the token once
and persists only its hash in `~/.ao/mobile/access-devices.json`.

Coordination tokens use the distinct `aoc_` prefix. Existing daemons recognize
only full device tokens with the `aod_` prefix, so a rollback cannot accept a
coordination token as a full device credential. Current daemons also require
the prefix and persisted scope metadata to agree: `aoc_` with missing scopes,
or `aod_` with scopes, fails authentication.

The authenticated LAN listener enforces the capability before the shared app
router. A coordination token is allowed only these method/path shapes:

- `GET /api/v1/projects` and `GET /api/v1/projects/{id}`;
- `POST /api/v1/agents/readiness/ensure`;
- `GET|POST /api/v1/sessions`;
- `GET /api/v1/sessions/{id}` and `GET /api/v1/sessions/{id}/pr`;
- `POST /api/v1/sessions/{id}/send`;
- `POST /api/v1/sessions/{id}/conversation/steer-or-send`.

Every other direct path and method returns `403 CREDENTIAL_SCOPE_DENIED`. The
token cannot directly issue another credential, kill or delete a session,
mutate a project, or reach loopback-only control routes. Empty device scopes
retain the existing full app behavior for backward compatibility. An unknown
non-empty scope fails closed.

`GET /api/v1/projects/{id}` is projected at the HTTP boundary for this
credential. It returns project identity, kind, and the configured worker and
orchestrator harness selectors only. Environment variables, scripts, prompts,
rules, paths, repository URLs, and the rest of project configuration are not
returned. Full credentials retain the existing response.

The client stores the coordination token in the owner-only
`~/.ao/remotes.json` record so a headless agent can use it. Full desktop
credentials continue to use OS-keychain sealing. The token is protected as a
full-authority host secret: route filtering is defense against accidental
direct API use, not permission to disclose it more broadly. Requests still
carry `X-AO-Expected-Host-ID`; a saved label or URL is not machine identity.

## Consequences

An agent that can read `remotes.json` can direct agents on another machine and
therefore holds that machine's transitive agent/OS authority. The token remains
a secret and the file remains mode `0600`. Plaintext storage is an explicit
delegation to same-user headless agents, not a claim that the route allowlist
contains what a prompted agent can subsequently do.

The direct allowlist is deliberately operational, not a broad semantic scope.
New coordination behavior does not inherit direct API access automatically.
The later reports path, for example, must name its exact read routes and amend
this decision when that slice is built.

The destination daemon still performs all normal spawn and messaging
validation. The origin CLI performs the fan-out and records host-qualified
lineage; neither daemon opens a connection to the other. Sleeping and
unreachable hosts therefore fail at the initiating client without adding a
daemon mesh or durable remote transport in these first two steps.
