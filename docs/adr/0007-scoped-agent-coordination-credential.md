# 7. A scoped credential for cross-machine agent coordination

Date: 2026-11-17
Status: Accepted

## Context

The desktop already reaches several AO daemons and the CLI can point one
invocation at a remote daemon. Cross-machine agent coordination lets a session
use those same saved hosts to list sessions, start a worker, and send it a
message.

Giving a headless agent the existing saved-host password, account token, or
ordinary device token would give it the entire app API. Those credentials can
terminate sessions, change projects, inspect unrelated data, and perform many
other actions that coordination does not need. Desktop credentials are also
encrypted with the OS keychain where available, while a headless CLI cannot ask
Electron's `safeStorage` to decrypt one.

This work must not turn AO daemons into peers. The remote-sessions design keeps
fan-out in a client, and the upstream session-transfer and cross-machine-search
work follows the same boundary: a desktop or CLI contacts each daemon; daemons
do not contact each other.

## Decision

Add an `agent-coordination` capability to device credentials. A password,
account token, or ordinary unscoped device token may exchange its existing
authority once for a new coordination token. The host returns the token once
and persists only its hash in `~/.ao/mobile/access-devices.json`.

The authenticated LAN listener enforces the capability before the shared app
router. A coordination token is allowed only these method/path shapes:

- `GET /api/v1/projects` and `GET /api/v1/projects/{id}`;
- `POST /api/v1/agents/readiness/ensure`;
- `GET|POST /api/v1/sessions`;
- `GET /api/v1/sessions/{id}` and `GET /api/v1/sessions/{id}/pr`;
- `POST /api/v1/sessions/{id}/send`;
- `POST /api/v1/sessions/{id}/conversation/steer-or-send`.

Every other path and method returns `403 CREDENTIAL_SCOPE_DENIED`. The token
cannot issue another credential, kill or delete a session, mutate a project, or
reach loopback-only control routes. Empty device scopes retain the existing full
app behavior for backward compatibility. An unknown non-empty scope fails
closed.

The client stores the limited coordination token in the owner-only
`~/.ao/remotes.json` record so a headless agent can use it. Full desktop
credentials continue to use OS-keychain sealing. Requests still carry
`X-AO-Expected-Host-ID`; a saved label or URL is not machine identity.

## Consequences

An agent that can read `remotes.json` can act on another machine, but only
inside the reviewed coordination surface. The token remains a secret and the
file remains mode `0600`; plaintext storage is acceptable here only because the
route allowlist is the actual authority boundary.

The allowlist is deliberately operational, not a broad semantic scope. New
coordination behavior does not inherit access automatically. The later reports
path, for example, must name its exact read routes and amend this decision when
that slice is built.

The destination daemon still performs all normal spawn and messaging
validation. The origin CLI performs the fan-out and records host-qualified
lineage; neither daemon opens a connection to the other. Sleeping and
unreachable hosts therefore fail at the initiating client without adding a
daemon mesh or durable remote transport in these first two steps.
