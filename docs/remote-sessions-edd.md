# EDD: Remote Sessions (Multi-Host Federation)

### Authors

- *(fill in)*

### Mandatory Approvers

- *(fill in — suggest: desktop/frontend owner)*
- *(fill in — suggest: security reviewer, for the LAN credential + loopback proxy model)*

### Reviewers

- *(business context)*
- *(technical domain — daemon/HTTP API owner)*
- *(manager)*
- *(platform stand-in — see note: no shared infrastructure is involved)*
- *(data stand-in — see note: no warehouse or BI impact)*

### Stakeholders

- *(fill in)*

---

# Objectives

Let one AO desktop app operate agent sessions on **several machines at once**.

**What:** the app connects to N AO daemons concurrently — the local one plus any saved
remote hosts — and presents every host's projects and sessions in a single tree. A session
can be opened, watched live, and typed into on any connected host without switching the app
into a "remote mode."

**Why:** agents are long-running and machine-bound. Work is spread across a laptop, a Mac
mini, and similar boxes, and before this the only options were SSH-ing to each machine or
pointing the whole app at one host at a time. Users could not see or compare work across
machines, and every cross-machine action meant a context switch.

**Who:** individual engineers running AO on more than one machine they own, on a trusted
network. Not multi-tenant, not multi-user.

**When:** shipped to `develop` 2026-08-14 (PRs #64–#87), verified working across two real
machines.

**Where:** entirely client-side — the Electron desktop app. No backend/daemon changes were
required for the client half.

## Goals

1. **Concurrent multi-host view.** Every connected host's projects and sessions in one tree,
each remote row labelled with its host.
2. **Act in place.** Open a remote project or session without a reload, a mode switch, or a
change to where any other action is sent.
3. **Live, not polled.** Each host gets its own event stream, so remote boards and
conversations update in real time.
4. **Interactive remote terminals.** A live terminal on each host simultaneously.
5. **Unambiguous targeting.** Every write names the machine it acts on, and destructive
prompts state the host. A session id existing on two hosts must never be confusable.
6. **Credential containment.** The connection password never enters the renderer process.
7. **Zero regression for single-host users.** With no saved hosts, behaviour is identical to
before.

## Non-Goals

- **SSH transport.** The original UI reference implied SSH hosts. AO's model is an AO daemon
over HTTP with a connection password; SSH would be a new transport, a new credential path,
and remote process supervision. Deliberately deferred (see Alternatives).
- **Daemon-to-daemon federation.** The local daemon does not connect to peers. Federation is
renderer-side only.
- **Multi-user or shared hosts.** One operator, machines they own. No per-user ACLs, no
concurrent-operator conflict resolution.
- **WAN / internet exposure.** LAN or Tailscale only. No TLS, no cert pinning, no NAT
traversal.
- **Global identity.** Project and session ids stay per-host. Ids are qualified at the
addressing boundary, never rewritten.
- **Browser-client host management.** The daemon-served web UI is already *at* one daemon by
URL; it degrades to local-only and cannot manage hosts.
- **Remote host management of the local machine.** Out of scope by symmetry.

---

# Background

**AO** is an Electron desktop app that supervises long-running coding-agent sessions. The app
owns a local **daemon** (a Go HTTP server on `127.0.0.1:3001`) that holds the project
registry, spawns sessions, streams events over SSE, and serves terminals over a WebSocket
multiplexer (`/mux`). The renderer is a React app whose origin is `app://renderer`.

Three things shipped before this work and are load-bearing here:

1. **A LAN listener** (default `:3011`), enabled per host, gated by an 8-character
**connection password**. It has per-source-address lockout against guessing
(`backend/internal/httpd/auth.go`). Loopback is ambient authority; LAN is
credential-gated.
2. **A CLI remote target** — `ao <cmd> --url http://host:3011`, authenticating with
`Authorization: Bearer <connection password>`, with saved hosts in **`~/.ao/remotes.json`**
(mode 0600, refused if looser).
3. **A `-url` correctness audit** (PRs #56–#63) that fixed nine cases where a command
accepted `-url` and acted on the wrong machine anyway.

**What reviewers need to know to review this effectively:**

- **The renderer cannot authenticate to a remote daemon by itself.** `EventSource` and
browser `WebSocket` cannot set an `Authorization` header, and `app://renderer` has no CORS
standing with a daemon that never allow-listed it. This single constraint determines the
entire design.
- **Project ids collide by construction.** The daemon derives a project id as
`strings.ToLower(filepath.Base(path))`, so `~/dev/agent-orchestrator` is
`agent-orchestrator` on *every* machine. Any design that resolves a bare id against a
merged multi-host list will target the wrong machine — this is the default case, not an
edge case.
- **This is a local desktop application.** Several sections of this template assume a
server-side SaaS with HTTP endpoints, a data warehouse, and shared infrastructure. Those
are marked **N/A** below with the reason, rather than filled with invented content.

---

# Design Details

## Overview

**Renderer-side fan-out with a per-host authenticated loopback proxy.**

```mermaid
graph TB
    subgraph Main["Electron main process"]
        Remotes["remotes.json (0600)<br/>{label, url, password}"]
        Registry[RemoteRegistry]
        ProxyA["proxy A"]
        ProxyB["proxy B"]
        Remotes --> Registry
        Registry --> ProxyA
        Registry --> ProxyB
    end

    subgraph Renderer["renderer (app://renderer)"]
        Client["clientFor(host)<br/>1 SSE stream per host<br/>1 terminal mux per host<br/>Ref = {host, id}"]
    end

    subgraph Remote["remote machines"]
        DaemonA["daemon A (LAN :3011)"]
        DaemonB["daemon B (LAN :3011)"]
    end

    Local["local daemon :3001"]

    Client -- "loopback, token-in-path" --> ProxyA
    Client -- "loopback, token-in-path" --> ProxyB
    ProxyA -- Bearer --> DaemonA
    ProxyB -- Bearer --> DaemonB
    Client -- direct --> Local
```

**Components**

| Component | File | Responsibility |
| --- | --- | --- |
| Host identity | `renderer/lib/hosts.ts` | `HostId`, `Ref = {host,id}`, `refKey`/`parseRefKey` |
| Saved-host store | `main/remotes-store.ts` | read/write `~/.ao/remotes.json`, enforce 0600 |
| Authenticated request | `main/remote-request.ts` | Bearer-injected fetch; `probeRemote` health |
| Loopback proxy | `main/remote-proxy.ts` | per-host HTTP + WebSocket proxy, token-gated |
| Proxy registry | `main/remote-registry.ts` | N live proxies keyed by URL; lifecycle |
| Per-host clients | `renderer/lib/host-clients.ts` | `clientFor(host)`, connect/disconnect, base map |
| Fan-out query | `renderer/hooks/useWorkspaceQuery.ts` | one query per host, `["workspaces", host]` |
| Per-host streams | `renderer/lib/host-events.ts` | one `EventSource` per host, host-scoped invalidation |
| Per-host terminals | `renderer/lib/terminal-mux.ts` | mux pool keyed by host |
| UI | `Sidebar`, `HostSelect`, `AddRemoteHostDialog`, `RemoteFolderPicker` | one tree, labels, filter, add/edit/remove |

**Key mechanisms**

1. **Local is a host.** `LOCAL_HOST` is a `HostId` like any other — the one whose requests
skip the proxy. No code path special-cases "is this remote?".
2. **Everything addressable is a `Ref`.** Ids are never rewritten; they are qualified only
where an action is routed. Every mutation takes a `Ref` and dispatches via
`clientFor(ref.host)`, so targeting the wrong machine is a type error rather than a
runtime accident. Routes are host-qualified (`/host/$hostId/session/$sessionId`,
`/host/$hostId/project/$projectId`) so a reload or restored window reopens the same
session on the same machine.
3. **The proxy solves the header problem.** Electron main is not a browser: it can set
`Authorization` and is not subject to CORS. The renderer talks to
`http://127.0.0.1:<ephemeral>/<128-bit token>` and the proxy forwards upstream with the
Bearer credential, stripping the token so the remote daemon and its logs never see it.
The token lives **in the URL path** — the one place `EventSource` and `WebSocket` can both
carry a credential. The proxy answers CORS preflight locally for `app://renderer`.
4. **Streams and terminals follow hosts.** One SSE connection per host, each tagged so an
event from host B invalidates only `["workspaces", B]`. The terminal mux pool is keyed by
host, and the mux URL is derived from that host's base — preserving the path prefix, which
is what carries the token.
5. **Failure is data, not an exception.** Each host carries `ready | checking | failed`. A
failed host renders as a labelled section with a retry and never blanks the tree or
discards another host's data. Hosts connect **after** first paint, so an unreachable or
sleeping host cannot block startup; probes are bounded at **5s**
(`AbortSignal.timeout`, `main/remote-request.ts`).
6. **Credential containment.** Only `{label, url}` and the loopback proxy base ever cross to
the renderer. Enforced by test.

## Rollout Plan

This is a **desktop application**, not a service: rollout is an app release, and there is no
server-side deploy, no traffic shifting, and no possibility of a partial-fleet outage.

**Safety properties that make this low-risk:**

- **Additive and opt-in.** With no entries in `~/.ao/remotes.json`, `connectedHosts()` is
empty, the app behaves exactly as before, and the entire remote path is inert. Existing
single-host users are unaffected by default.
- **No daemon change required.** The client half needs no new server endpoints, so a new app
can talk to older daemons. (One exception below.)
- **Backward compatible with older remote daemons.** Version skew is permanent and expected:
`GET /api/v1/fs/dirs` (remote folder browsing) exists only on daemons at or after PR #66.
Against an older host the picker reports *"That host did not return a folder listing. It may
be running an older build without folder browsing"* and the rest of the feature works.
Users can still register a project by absolute path.

**Stages:**

1. **Internal dogfood — complete.** Verified across two real machines on a LAN: both hosts'
projects in one labelled tree, a remote project opened in place, and live terminals on both
machines simultaneously.
2. **Release with the feature discoverable but unconfigured.** Users must add a host
deliberately (Add project → Host → Add remote host), which requires physically reading a
connection password off the other machine's Settings → Connect Mobile.
3. **Documentation** of the trust boundary — see Security & Privacy. This should not ship as
a silent capability.

**Rollback:** ship the previous app build. There is no migration and no persisted schema
change beyond `remotes.json`, which the CLI already used and which older builds still read.

## Trade Offs

| Decision | Why | Cost accepted |
| --- | --- | --- |
| **Renderer-side fan-out**, not a daemon hub | No daemon-to-daemon link; peer credentials stay in the app rather than behind a loopback socket that every local process can reach; merging logic sits where the UI's needs are visible | Every query, id and write in the renderer became host-aware — a wide (if shallow) refactor |
| **Loopback proxy** rather than header injection or CORS negotiation | `EventSource`/`WebSocket` cannot set headers; Electron `webRequest` header-mangling fights Chromium's CORS enforcement | A local listener per connected host; token-in-path complexity |
| **Token in the URL path** | The only place both stream transports can carry a credential | Tokens can leak via logs if ever printed; mitigated by stripping before forward and never logging paths |
| **Ids qualified, not rewritten** | A project keeps the same id in its own daemon, CLI and URLs; no translation layer | Composite keys (`refKey`) everywhere ids are used as map keys or React keys |
| **One host at a time was rejected** | The explicit product requirement was to work across machines without switching | Concurrency: N streams, N proxies, partial-connectivity states as a normal case |
| **Failure as per-host data** | With N hosts, "two fine, one asleep" is the normal state | Consumers must read per-host status; a regression here (#78) shipped and had to be fixed in #86 |
| **Hosts connect after first paint** | An unreachable host must never block startup | Consumers had to learn hosts arrive asynchronously; getting this wrong silently disabled live updates |

---

# Alternatives Considered

1. **Hub federation in the local daemon.** The local daemon connects to peer daemons and
re-exposes their state as its own; the renderer stays a single-daemon client and the
header problem disappears. **Rejected:** it moves peer connection passwords into the daemon,
which loopback clients reach with ambient authority — meaning any local process could
transitively reach the whole fleet. It also puts UI-shaped merging decisions in Go, and
introduces the first daemon-to-daemon link in the system. Revisit if multi-host becomes a
server-side concern (e.g. headless orchestration).
2. **Switch-to-open (one active host at a time).** Simpler: pick a host, the app reloads
pointed at it. This was actually shipped as an interim state and **rejected on use** — it
makes cross-machine comparison impossible and forces a reload per switch, which is exactly
the workflow the feature exists to remove.
3. **SSH transport.** Matches the original UI reference and would remove the plaintext-LAN
password entirely (auth by existing SSH keys, no exposed listener on the remote). **Deferred,
not rejected** — and now cheap: an SSH entry becomes `ssh -N -L` from main to the remote
daemon's loopback port, then the identical proxy path. The proxy is the seam.
4. **Browser-only remote access** (the existing Phase 1). Already shipped and retained: point a
browser at `http://<host>:3011/`. **Insufficient** — one host per tab, no cross-machine view,
and no desktop integration.
5. **Global id namespacing** (rewrite ids to `host:id` everywhere). **Rejected:** it breaks
parity with each daemon's own CLI and URLs, and the collision problem only needs solving at
the addressing boundary.

---

# Dependencies

**Technologies:** Electron main process (`node:http`, `node:net`, `node:crypto` — **no new
runtime dependencies were added**), React 19, TanStack Query/Router, `openapi-fetch`, Vitest,
Playwright.

**Internal services:** the AO daemon's existing `/api/v1` HTTP surface, its SSE stream
(`/api/v1/events`), and its terminal multiplexer (`/mux`). One new daemon endpoint was added
for remote folder browsing: **`GET /api/v1/fs/dirs`** (read-only, directory names only, no
file contents, dotfiles skipped, capped at 500 entries), declared in the code-first OpenAPI
spec and therefore covered by the existing `api-drift` CI check.

**External services / SaaS:** **none.** No new vendor relationship, so **the Vendr process is
not triggered** and there is nothing for IT/Security/Legal/Finance to onboard. All traffic is
between machines the user already owns, on their own network.

**Data pipeline:** **N/A.** This feature adds no tables or columns and removes none; it does
not write to any warehouse. See Data and Business Intelligence.

**HTTP latency guidance (<250ms) and background workers:** the `<250ms` endpoint target and
the "use non-HTTP Convox workers for long jobs" guidance apply to server-side request
handling and are **not applicable** — this feature adds no server endpoints. The one new
daemon endpoint (`fs/dirs`) is a bounded single-directory `readdir`, capped, with no
serialization of large collections. Long-running work in AO already runs as supervised agent
processes, not inside HTTP requests.

---

# Performance & Scalability

**What the complexity is proportional to: the number of *saved hosts*, which is human-scale
(typically 1–3, realistically never more than ~10).** It is *not* proportional to request
throughput, row counts, or user population — there is one operator per app instance.

Per connected host, steady state:

| Resource | Cost |
| --- | --- |
| Loopback proxy | 1 listener on `127.0.0.1`, ephemeral port, in the main process |
| REST | 1 workspace query per host per refresh (`/projects` + `/sessions`) |
| SSE | 1 long-lived `EventSource` |
| Terminals | 1 mux WebSocket per host *with an open terminal* |

**How this impacts existing endpoints and workflows:**

- **Local daemon: unchanged.** Local requests still go direct, not through a proxy. A user
with no saved hosts issues exactly the same traffic as before.
- **Remote daemons: each sees a single additional client.** The load a remote daemon
experiences is one more operator, which is the load it was designed for.
- **Renderer:** fan-out multiplies request *count* by the number of hosts, not by data volume.
Queries are per-host (`["workspaces", host]`) and run in parallel via `Promise.allSettled`;
one slow host cannot serialize the others.
- **Proxy overhead** is a loopback hop with header rewriting and `pipe()` streaming — no
buffering of response bodies, so SSE chunks and terminal frames are forwarded as they
arrive (pinned by test).

**Bounded failure costs:**

- Host probes are capped at **5s** (`AbortSignal.timeout`), so an unreachable host cannot hang
startup or a reconnect indefinitely.
- Hosts connect **after first paint**, so startup latency is independent of host count and
reachability.
- A host whose stream drops falls back to the existing polling interval rather than going
stale silently — though see Metrics for a gap here.

**Scaling limits and what to do:** the design degrades linearly with host count. If host
counts ever grew beyond a handful (not an anticipated use case), the fixes would be lazy
connection (connect on first use rather than at boot) and shared-connection pooling. Neither
is warranted now.

---

# Metrics & Monitoring

**Shipped (AO-78).** This section was the weakest part of the implementation — the remote
path had no telemetry and no logging at all, so "I expected my remote session to update and
it didn't — why?" was unanswerable from anywhere. The instrumentation below is now in place.

**Instrumentation:**

| Signal | Why | Where |
| --- | --- | --- |
| `ao.renderer.host_connect` (result: online/unauthorized/offline/not-a-daemon, duration, source: add/edit/probe) | Is adding a host succeeding, and which failure mode dominates? | `renderer/lib/host-telemetry.ts`, emitted from the add/edit dialog and the saved-host probe |
| `ao.renderer.host_stream_state` (host, connected/disconnected, reconnect count) | Detect the silent no-live-updates case | `renderer/lib/host-events.ts`, on each state transition |
| `ao.renderer.host_query_failed` (host, status) | Per-host data failures, previously invisible — remote clients are plain `openapi-fetch` clients and bypass `api-client`'s `ao.renderer.api_error` | `renderer/hooks/useWorkspaceQuery.ts`, in `fetchHostSection` |
| Proxy lifecycle logs in main (start/stop, upstream error, 502) — **never the token or password** | Debuggability for "the app can't reach my host" | `main/remote-proxy.ts` |
| `hostConnectionState` surfaced in the UI | The user is the fastest detector | Sidebar host notice: "Not receiving live updates from {host}", carried on `HostSection.streamState` |

**Alerting/pages:** **N/A** — this is a locally-installed desktop app with no on-call
surface. Detection is user-facing and telemetry is aggregate/diagnostic, not paging.

**No-secrets rules, enforced by tests:**

- The connection password is never logged, never sent to the renderer, and never crosses to
the remote daemon in the URL (the proxy token is stripped before forwarding). All three
were true before this work and remain true.
- `main/remote-proxy.ts` logs the upstream address and the *post-strip* request path only.
It must never log `req.url` — its first segment IS the proxy token — nor `entry.password`.
- A host id IS a LAN address, so no `ao.renderer.host_*` event carries one. `host_id` is
hashed to `host_id_hash` by `sanitizeRendererProperties`, exactly as project ids are, and
`host_kind` (`local`/`remote`) is the only host attribute sent in the clear. Every other
property is dropped by the allowlist.
- `ao.renderer.host_query_failed` sends a status, never the daemon's error text, which can
carry paths.

**Volume control:** `captureRendererEvent`'s per-name rate limit is shared across hosts, so
`host_query_failed` additionally collapses repeats of the same (host, status) inside a
five-minute window — a host that is simply switched off refetches every 15 seconds forever
and would otherwise spend the whole daily ceiling and hide the next host to break.
`host_stream_state` fires on transitions only, because `EventSource` calls `onerror`
repeatedly while it retries.

**Two states, not one:** `hostConnectionState` distinguishes `idle` (no stream was ever
opened — jsdom, preview surfaces) from `disconnected` (a stream that dropped). Only the
second one means the board went stale, and only it is reported or shown.

**Cost:** small — one existing telemetry helper and a handful of `console` calls. No new
infrastructure, no new vendor, no storage cost beyond existing telemetry volume.

---

# Infrastructure & Networking

- **New infrastructure: none.** No servers, no cloud resources, no managed services. The only
new listeners are ephemeral loopback HTTP proxies inside the user's own Electron process,
bound explicitly to `127.0.0.1`.
- **Modified infrastructure: none.** The daemon's LAN listener already existed.
- **Cost:** **$0.** No CSP/EISP/RI cost-control mechanisms apply — there is no cloud spend.
- **Communication:** the app talks to (a) its own local daemon over loopback, and (b) each
saved remote daemon's LAN listener over the user's own network. No third-party endpoints.
- **Networking prerequisites the user must satisfy:** the remote host's LAN bridge must be
enabled, and the two machines must be mutually reachable (same LAN, or a Tailscale tailnet
via the existing `"bind": "tailscale"` option).
- **Known platform caveat:** on macOS, **Local Network privacy** must be granted to the
terminal/app or LAN connections fail with `EHOSTUNREACH`, which surfaces as a misleading
"no route to host". Apple-signed binaries like `curl` are exempt, which makes the diagnosis
confusing. This should be documented in setup instructions.

---

# Data

- **Relationship to existing data models:** none. This feature reads existing per-daemon
data (projects, sessions) and does not create a new model. Each daemon remains the sole
owner of its own records; there is no cross-host join, replication, or merged store. The
sessions→projects join is performed strictly **within** a single host's response set.
- **Impact on KPIs:** none — no warehouse or product-analytics model is touched.
- **New persisted data:** one file, **`~/.ao/remotes.json`**, which already existed for the
CLI: `{label, url, password}` per host, mode **0600**, refused if group/other-readable. This
feature adds UI to create, edit and remove entries; the format and permission rule are
unchanged.
- **Lifecycle:** entries live until the user deletes them. Removing a host in the UI deletes
its entry and tears down its proxy. There is no server-side copy and nothing to expire.
- **Storage cost:** negligible — a small local JSON file.
- **PII:** none beyond machine labels the user chooses and a per-host connection password
(see Security).

---

# Reliability

**Dependent-service outages are the normal case here**, not an exception, and the design
treats them that way.

| Failure | Behaviour |
| --- | --- |
| A remote host is asleep/unreachable | Its section renders as `failed` with a retry. Other hosts and local are unaffected; the tree is never blanked. Probes bounded at 5s. |
| A remote host is unreachable **at startup** | Boot completes regardless — hosts connect after first paint, so startup does not depend on remote reachability. |
| A remote host's password was rotated | The host reports `unauthorized` distinctly from `offline` — "wrong password" and "could not reach" are never conflated. The user edits the saved host in place. |
| A saved URL points at something that is **not a daemon** (e.g. a mistyped port hitting a dev server that 200s everything) | Rejected at add time and at connect time by validating `/healthz` through the daemon probe, not by status code alone. Prevents a bad entry from bricking the app on every launch. |
| A remote returns a **malformed body** (HTML from a wrong-port server, or valid JSON of the wrong shape) | Validated at the parse boundary; that host fails with an honest message. Never throws into the renderer. |
| Local daemon fails while remotes are healthy | Local is an app-level error and lights up the existing error paths; remote sections keep working. |
| A single bad input | Cannot bring the app down: response bodies are validated before reaching state, and per-host failures are isolated. |

**Recovery:** all failure states are user-recoverable in-app — retry a host, edit its
credential, or remove it. No process restart is required. If a saved host cannot be
activated at boot, the app degrades to local rather than failing to start.

**Residual risk:** the silent case — a host connected but its event stream not established —
degrades to polling with no indicator. See Metrics; this is the main reliability gap.

---

# Security & Privacy

**This is the section most deserving of scrutiny, and the trust boundary should be stated
explicitly in user-facing documentation.**

**Threat model / intended trust boundary:** a single operator, machines they own, on a network
they trust (home/office LAN, or a Tailscale tailnet). It is **not** designed for hostile
networks, shared machines, or multi-tenant use.

**Security review:** the daemon's LAN listener and origin policy were reviewed when Phase 1
shipped. The client-side additions — the loopback proxy, its token, and `remotes.json` custody —
were reviewed under **AO-79 (2026-08-21)**; findings and accepted risks are recorded below.

**Attack surface and mitigations:**

| Surface | Risk | Mitigation |
| --- | --- | --- |
| LAN listener (`:3011`) | Plaintext HTTP, 8-character connection password, binds all interfaces by default | Per-source-address **lockout** on repeated failures (`auth.go`); `"bind": "tailscale"` or a specific IP narrows exposure; documented as trusted-network-only |
| **Loopback proxy** (new) | A local listener that forwards to a *different machine* with a stored credential — i.e. ambient authority for other local processes, the same model as AO's own loopback daemon | Every request must carry a **128-bit random token** in the URL path; without it the proxy 404s and forwards nothing. This limits reach to processes that can read renderer memory rather than any local process. Proxy is bound to `127.0.0.1` only, and is torn down when the host is disconnected. |
| Token leakage | A path-borne credential can leak via logs or referrers | Token is **stripped before forwarding**, so the remote daemon and its logs never see it; the proxy's lifecycle logging (AO-78) never emits `req.url`, whose first segment is the token; tokens are per-activation, not persisted |
| Stored credentials | `~/.ao/remotes.json` holds plaintext passwords | Enforced mode **0600** (except on Windows, where the CLI and the app both skip a check Node cannot make); the file is *refused* if group/other-readable; the password **never enters the renderer process** (only `{label, url}` and the loopback base cross the IPC boundary, enforced by test). No OS keychain is used — a deliberate decision, see the AO-79 review below. |
| Remote filesystem listing (`fs/dirs`) | Reveals directory names on the remote host | Read-only, **directory names only** (no contents, sizes, or mtimes), dotfiles skipped, capped at 500 entries, and behind the same credential that already authorizes spawning agents (i.e. shell access) — so it is not an escalation |
| CORS / origin | `app://renderer` has no standing with a remote daemon | The proxy answers preflight locally and strips the renderer origin before forwarding; the daemon's strict origin policy is untouched |
| Wrong-machine actions | A destructive action landing on the wrong host | Every write takes a `Ref = {host,id}` routed via that host's client; destructive prompts name the host; a colliding-id test asserts the local daemon receives **no request** when a remote action fires |

**AuthN/AuthZ added:** yes — the app now presents a per-host bearer credential on the user's
behalf and validates hosts before trusting them. There is no per-user authorization model;
possession of the connection password is full authority on that daemon (as it already was for
the CLI).

**Cryptography:** `crypto.randomBytes(16)` for the proxy token. No encryption in transit on
the LAN path — **plaintext HTTP** — which is the single largest security limitation and the
reason the trust boundary is a trusted network. Tailscale is the recommended mitigation;
TLS with TOFU pinning is the longer-term fix.

**Data logged:** nothing in the remote path today (see Metrics). Any future logging must
exclude the password and the token.

**PII deletion:** no PII is collected or transmitted by this feature. Removing a host deletes
its stored entry; there is no server-side copy to purge.

## Security review — AO-79 (2026-08-21)

**Scope:** the loopback proxy (`main/remote-proxy.ts`), the per-host token model, the
credentialled request path (`main/remote-request.ts` + the `remotes:*` IPC surface), and
`~/.ao/remotes.json` custody (`main/remotes-store.ts`). The daemon's LAN listener and origin
policy were out of scope — reviewed at Phase 1.

**The token model holds.** 128 bits from `crypto.randomBytes`, compared in constant time,
per-activation and never persisted, stripped before forwarding, bound to `127.0.0.1`, and torn
down on disconnect. Preflight is answered locally and the renderer origin never reaches the
daemon. Nothing in the remote path logs the base URL. The renderer never receives the
connection password. Those properties were re-checked against the code and hold.

### Fixed

| # | Severity | Finding |
| --- | --- | --- |
| 1 | High | **A renderer-supplied path could redirect the credential off-host.** `remoteRequest` concatenated `init.path` onto the saved host URL, so a path beginning with `@` turned the base into userinfo — `http://box:3011` + `@evil.example/` is a request *to evil.example*, carrying `Authorization: Bearer <connection password>`. `remotes:*` is a renderer→main trust boundary and the renderer is the process that renders agent output, so the origin is now asserted before the request is made. |
| 2 | High | **An `https://` host was talked to in cleartext.** The proxy computed port 443 for an https upstream but forwarded with `node:http` / `net.connect` regardless, putting the connection password on the wire unencrypted. The add-host dialog accepts `https://`, so a Tailscale Serve or reverse-proxy address reached this by typing it. The proxy now uses `https.request` / `tls.connect` for an https upstream (certificates validated by default). |
| 3 | Medium | **The proxy dropped the host's URL path prefix.** A daemon behind a reverse proxy at `http://box/ao` received every credentialled request at `/api/v1/…` instead of `/ao/api/v1/…` — delivering the Bearer credential to whatever else that vhost serves. The prefix is now restored on both the request and upgrade paths. |
| 4 | Medium | **`remotes.json` was unreadable on Windows.** Node reports `0o666` for every writable file there, so the group/other-readable check refused every file and took saved hosts down with it. The CLI already exempts win32 for this exact reason (`cli/remote.go:154`); the TS port did not. Not caught by CI — the frontend unit job is Linux-only. |

Each has a falsifying test in `frontend/src/main/*.test.ts`; all four fail on the pre-fix code.

### Accepted risks

- **Loopback is ambient authority.** Any local process can reach the proxy port; the token is
what stops it. That is the same model as AO's own loopback daemon, and the token narrows reach
from "any local process" to "a process that can read renderer memory". Accepted.
- **The token is visible in the renderer DOM.** Attachment images are `<img src>` against the
proxy base, so the token is readable by anything that can read the DOM. This is not a new
class — such an attacker can already call the IPC surface — but it means the token is *not* a
defence against a compromised renderer. Accepted; the renderer has no CSP today, which is a
separate app-wide hardening item, not a remote-sessions one.
- **Raw request-line assembly on the upgrade path.** The WebSocket upgrade is written to the
socket as a hand-built HTTP request, and `entry.password` is interpolated without validation.
Node's parser rejects CR/LF in incoming headers and request targets, so the only injection
vector is a saved password containing control characters — which an attacker can only supply
by getting the user to save *their* password against *their* host, where header injection buys
nothing. Not worth code. Accepted.
- **`cookie` and `referer` are forwarded upstream.** Deliberate: the daemon uses an `ao_conn`
cookie for browser-served preview routes, and stripping it would break that path. No document
is ever loaded *from* a proxy base, so no `Referer` carries the token.
- **Plaintext HTTP on the LAN path** remains the largest limitation, unchanged by this review.
Tailscale — now actually usable end-to-end given finding 2 — is the mitigation.
- **`remotes.json` writes are not atomic.** A crash mid-write truncates the credential file.
Reliability, not security; noted for whoever touches the store next.

### Decision: keep the 0600 file, no OS keychain

**`~/.ao/remotes.json` stays a 0600 plaintext file.** The keychain does not earn its cost here:

1. **It would fork state with the CLI.** The file is deliberately shared verbatim with
`ao --url` (`cli/remote.go`) and mirrors the mobile client's store, so "which hosts exist" has
one source of truth. Electron `safeStorage` is unreadable from Go, so a keychain move either
splits that store in two or demands a Go keychain implementation on three platforms.
2. **The protection is macOS-only in practice.** On macOS, keychain items are ACL'd to the
signed app, which is a real gain. Windows DPAPI and Linux `safeStorage` are same-user
decryptable by construction — and on Linux without libsecret/kwallet `safeStorage` degrades to
`basic_text`, i.e. encryption in name only.
3. **It does not shrink the blast radius that matters.** An attacker running as the user
already has the AO worktrees, the session store, `ao` on `PATH`, and a local daemon that can
drive every connected remote. Encrypting one credential while the process that decrypts it is
freely attachable is defence-in-depth, not a boundary.

**Revisit if** AO targets shared or multi-user machines, or if the desktop app stops sharing
the store with the CLI. The shape then is macOS `safeStorage` plus a Go keychain shim, decided
together rather than one client at a time.

---

# Vendors

**N/A.** No vendors are used, added, or affected. No SaaS relationship, no budget impact, no
SSO integration, and nothing requiring vendor security review. All communication is
machine-to-machine on infrastructure the user already owns.

---

# Accessibility

The feature has UI and should meet WCAG AA, consistent with the rest of the app.

**Addressed in the implementation:**

- **Host status is conveyed as text, not colour alone** ("Connected", "Connecting…",
"Disconnected", "Wrong password") — an explicit requirement during implementation.
- **Pending state uses a live region** (`role="status"`) rather than a spinner only, because
a multi-second host probe otherwise reads as a dead dialog to a screen-reader user.
- **Errors use `role="alert"`** and clear when the user retypes, so a stale error never sits
over corrected input.
- Unreachable hosts are **disabled** in the picker rather than selectable-then-failing.

**Audited (AO-80).** A keyboard and screen-reader pass over the host tree, filter, add/edit
dialogs and folder picker found and fixed:

- **The host picker's row actions were unreachable by keyboard.** Connect, Edit and Remove
sat inside a Radix `Select`, which calls `preventDefault()` on Tab within its listbox and
moves focus only between options — so they were mouse-only, and a listbox whose children are
buttons is not a listbox a screen reader can report faithfully. The picker is now a popover of
plain buttons: every action is a Tab stop and announces as itself. An unreachable host is
`aria-disabled` rather than `disabled`, so it stays focusable and its status can still be read.
- **The add/edit dialog could only be submitted by pointing at its button.** Its fields are now
a real `form`, so Enter saves, and the absolutely-positioned close button moved after the
fields in source order so opening no longer lands on Close.
- **The folder picker lost focus on every hop.** Stepping into a folder replaces every row, so
the focused row stopped existing. Focus now moves into the new listing, the path is a live
region, and a pending listing announces instead of leaving the dialog looking dead.
- **Duplicate accessible names.** Each unreachable host section had a button called just
"Retry"; each is now named for its host, as the picker's actions already were.
- **The host filter's effect was silent.** Changing it redraws the tree behind it, which a
screen reader has no reason to revisit; a live region now states which host is in view.

**Deliberately deferred:** the sidebar tree's own disclosure semantics (a project row carries
`aria-expanded` while its primary action navigates, with a second invisible toggle over the
folder icon owning the same state, and no `aria-controls` linking either to the session list).
That structure predates remote hosts and is shared with every local project, so it belongs to a
sidebar-wide pass rather than this one. Per-host grouping in the tree is also deferred: each
project's accessible name already carries its host via `hosts.qualified`, and there is no
visual host boundary for a `role="group"` to mirror.

---

# Business Intelligence

**N/A.** This feature touches no data model consumed by business intelligence. No Periscope
dashboards or Redshift views reference it, none need updating, and no existing metric
definition changes. If host-connection telemetry is added (see Metrics), it would be new
product-analytics events rather than a change to any existing model.

---

# Libraries

- **No new runtime dependencies** were added — the proxy uses Node built-ins (`node:http`,
`node:net`, `node:crypto`) already available in the Electron main process.
- **Java libraries: N/A** — no Java in this stack.
- **Internal contract change:** the daemon gained `GET /api/v1/fs/dirs`, declared in the
code-first OpenAPI spec (`apispec/specgen`). The generated TypeScript client
(`frontend/src/api/schema.ts`) is regenerated by `npm run api` and drift is enforced by the
existing `api-drift` CI job, so client/server contract skew is caught automatically.
- **Cross-build compatibility:** because a newer app may talk to an older daemon, the client
must tolerate missing endpoints. This is handled explicitly for `fs/dirs` and is a standing
requirement for any future endpoint.

---

# Deliverables

## Shipped (merged to `develop`, 2026-08-14, tip `1a36693db`)

| Area | PRs |
| --- | --- |
| `--url` correctness audit (nine items) | #56, #57, #58, #60, #61, #62, #63 |
| Saved-host store, authenticated requests, password-free IPC | #64 |
| Host picker UI (select, add-host dialog, live status) | #65 |
| Daemon `GET /api/v1/fs/dirs` + OpenAPI | #66 |
| Loopback proxy + activation IPC | #67 |
| Host selection in Add-a-project | #68 |
| Remote folder browsing UI | #69 |
| Host switcher, gated rebind, host-named prompts | #70 |
| Response-body validation at the parse boundary | #71 |
| Edit/remove a saved host | #72 |
| Reject a host that answers but is not a daemon | #73 |
| Federation: refs, N proxies, per-host clients | #74, #75, #76 |
| Federation: fan-out queries, per-host SSE, per-host terminals | #78, #79, #81, #83 |
| Proxy URL-prefix fix (token drop) | #80 |
| Quit-deadlock fix (upgraded-socket teardown) | #77 |
| Ref-routed writes, no global active host | #84 |
| One tree across every connected host | #85 |
| Local workspace failure visibility | #86 |
| Hostile fake daemon test harness | #87 |

**Verification performed:** full unit/component suite (~2,400 tests), typecheck and e2e
typecheck, Playwright smoke serially, `go test` + `go test -race`, `golangci-lint`, and CI
across ubuntu/macos/windows. Three independent multi-agent design reviews on the
highest-risk PR. **End-to-end verified on two real machines**, including simultaneous live
terminals.

## Remaining / recommended follow-up

| Item | Size | Priority | Parallelizable |
| --- | --- | --- | --- |
| ~~**Telemetry + proxy logging** (Metrics section) and surfacing `hostConnectionState` in the UI~~ — **done (AO-78)**, see Metrics & Monitoring | S–M | — | — |
| ~~**Security review** of the loopback proxy + token model~~ | S | Done — AO-79, 2026-08-21 (four fixes; see Security & Privacy) | — |
| **Accessibility audit** of the host tree, filter, and folder picker | S | Medium | Yes |
| **Setup documentation**: trust boundary, Tailscale, macOS Local Network privacy caveat | S | Medium | Yes |
| ~~**SSH host support** — `ssh -L` tunnel into the existing proxy seam~~ — **spiked (AO-82)**: works with no proxy change; do it, docs first. See the spike section below | S then M | Medium | Yes (step 1 is documentation) |
| **TLS + TOFU pinning** for the LAN listener — **mobile-only** after AO-82; SSH covers the desktop leg | L | Low (Tailscale covers the need) | Yes |
| Cross-platform verification (Windows/Linux daemon as a remote host) | M | Medium | Yes |

### Tasks

*Testing time is included in each estimate. The first four items are mutually independent and
can be parallelized across owners; SSH and TLS followed the security review — SSH is spiked (AO-82) and
its step 1 folds into item 4.*

1. ~~Instrument the remote path (host connect result/duration, stream state, per-host query
failure); add proxy lifecycle logging in main with an explicit no-secrets rule; surface
stream state in the UI.~~ **Done — AO-78.**
2. Security review of the loopback proxy, token handling, and `remotes.json` custody;
decide whether OS keychain storage replaces the 0600 file.
3. Accessibility pass (screen reader + keyboard) on the host tree, filter, add/edit dialogs
and folder picker.
4. User-facing setup documentation, including the trusted-network boundary, the Tailscale
bind option, and the macOS Local Network privacy failure mode.
5. ~~SSH transport spike behind the existing proxy seam.~~ **Done — AO-82, 2026-08-22.**
6. Cross-platform host verification.

## SSH transport spike — AO-82 (2026-08-22)

**Question:** can an `ssh -L` tunnel go behind the existing loopback-proxy seam, and does
that remove the plaintext-HTTP risk AO-79 accepted as "the largest limitation"?

**Answer: yes, and the proxy needs no change at all.** The expensive part is not the
transport — it is who spawns and owns the `ssh` child. **Recommendation: do it, in two
steps, documentation first.**

### What was measured

A throwaway test drives the **unchanged** `startRemoteProxy` against a real `ssh -L`
forward to a stand-in daemon: `frontend/src/main/remote-proxy.ssh-spike.test.ts` on
`ao/agent-orchestrator-84/ssh-spike`. Both a credentialled REST request and a **WebSocket
upgrade** (the terminal/SSE path) traverse renderer → proxy → ssh → far side, with
`Authorization: Bearer` injected in main and the renderer `Origin` still stripped. 2/2 pass.
The test is a **proof, not a starting point** — it shells out to a live `ssh` and is not
merge material.

### 1. Tunnel lifecycle

`ssh` is a child process of main, one per connected SSH host, spawned before the proxy and
killed after it. Three measured details shape the design:

- **OpenSSH will not allocate the local port.** `-L 0:…` and `-L 127.0.0.1:0:…` are both
rejected as "Bad local forwarding specification" (OpenSSH 10.2). The caller must bind a
socket, read the port, close it, and hand the number to `ssh` — a TOCTOU race with every
other process on the machine. `-o ExitOnForwardFailure=yes` turns losing that race into a
clean immediate exit rather than a tunnel that silently forwards nothing.
- **A unix-socket forward avoids the race entirely.** `-L /path/to.sock:127.0.0.1:3011`
works, and OpenSSH creates the socket `srw-------`, which is a tighter boundary than a
loopback port for free. Two caveats: the path is capped by `sun_path` (~104 bytes on macOS —
a 155-char path is rejected at parse time; `~/.ao/` fits comfortably), and it needs
`socketPath` support at the dial site, so it is a later hardening, not the first cut.
- **A dropped tunnel is already a handled state.** When the `ssh` child dies the forwarded
port stops listening; the proxy gets `ECONNREFUSED` and answers its existing
502 `{"error":"remote daemon unreachable"}`. A dead tunnel is indistinguishable from a dead
host, so **no new UI state is needed**. Sleep/suspend is the same path once
`ServerAliveInterval`/`ServerAliveCountMax` fire; without them a suspended peer leaves a
listening port that hangs instead of refusing, so they are not optional.

Setup cost, measured to localhost: **~200–300 ms** cold, **~75–145 ms** through an existing
`ControlMaster` via `ssh -O forward`. Cheap enough that `remotes:probe` and `remotes:add` can
each open a throwaway tunnel for their own duration rather than sharing a refcounted one with
`remotes:connect`. Two independent lifetimes beat one refcounted lifetime here.

### 2. Store shape, and how the Go CLI stays in agreement

**`RemoteEntry` is extended in place, and the CLI needs no Go change.** Verified against the
real binary: an entry with `"url": "ssh://box"` plus unknown `kind`/`ssh` fields sits in
`~/.ao/remotes.json` while `ao --url http://…` resolves its siblings normally. Three existing
properties make that safe, and all three are load-bearing:

- `lookupRemoteEntry` is the **only** reader and there is **no writer** in Go, so there is no
round-trip that could drop unknown fields.
- It already `continue`s past an entry whose URL fails `normalizeRemoteURL` — with the comment
"a hand-edited entry must not break every other one" — and that function rejects any scheme
but http/https.
- Asked for an SSH entry directly, the CLI refuses accurately rather than misbehaving:
`invalid daemon URL "ssh://box": scheme must be http or https`.

So the store stays one file with one source of truth. What the CLI *cannot* do is use an SSH
host — and it does not need to: `ssh box ao status` is strictly better than teaching the Go
CLI to spawn its own tunnel. If that answer is ever unacceptable, the honest fix is a
`ao remote` subcommand, not a second store.

### 3. What replaces the connection password

**It does not replace it — it takes it off the wire, which is exactly what AO-79 asked for.**
There are two possible targets on the far side and only one of them works:

| Forward to | Credential | Result |
| --- | --- | --- |
| **LAN listener `:3011`** | connection password, still required | **Works.** Measured through a tunnel: authenticated `GET` → 200, and a renderer-shaped write carrying `Sec-Fetch-Site: cross-site` passes origin policy because `servedOverLAN` exempts it. |
| Loopback listener `:3001` | none — "the tunnel is the auth" | **Broken, and worse than broken.** |

The loopback variant fails two ways, both measured:

1. **Every write and every terminal 403s.** `requiresStrictOrigin` applies on the loopback
listener, and `strictOriginOK` rejects both `cross-site` and `same-site`. Electron's Chromium
does attach `sec-fetch-site` to a cross-origin fetch (confirmed by echoing the renderer's own
headers back through the Browser panel: Electron 33.4.11 / Chromium 130), and
`STRIP_REQUEST_HEADERS` does not strip `sec-fetch-*`, so it arrives verbatim. `POST` and
`GET /mux` through the tunnel both return `403 ORIGIN_FORBIDDEN`.
2. **It hands out every loopback-only control route.** `lanControlBlock` exists precisely to
keep `/shutdown`, `/internal/`, `/api/v1/mobile`, `/api/v1/dev` and `/api/v1/browser` off a
network-reachable socket. Through a tunnel to `:3001` they are all reachable with **no
credential** — `GET /api/v1/mobile/status` returns the daemon's connection password in
cleartext (measured; value not recorded here). On the LAN listener the same request correctly
returns `404 ROUTE_LOOPBACK_ONLY`.

   **That list grows, which is the real argument.** The upstream sync merged as #110 added a
sixth prefix — `/api/v1/system/install`, "host-mutating installer routes" — days after this
spike was scoped. Every loopback-only route anyone adds from here silently widens what a
tunnel to `:3001` would expose, and nothing in the client would notice. A design whose blast
radius is defined by a list someone else keeps appending to is the wrong design.

Neither is fatal in isolation — an SSH login is already shell access on that box — but making
it work means weakening the loopback listener's origin boundary for *every* local caller, not
just the tunnel. That is a daemon policy change, decided on its own merits. **Drop the
loopback variant.**

The complement is free and worth pairing with this: `bind` already accepts a literal address
(`BindAddress` → `net.ParseIP`), so `"bind": "127.0.0.1"` in `~/.ao/mobile/config.json` binds
the LAN listener to loopback only. **LAN listener on loopback + SSH tunnel = the password is
kept, and the port is on no network at all.** No code.

### 4. Host-key verification

**Delegate to OpenSSH; invent nothing.** The `ao-phone-proxy` prior art named in the task does
not apply: that proxy is **retired** (ADR 0001), and its "TOFU" was IP trust-on-first-connect,
not host-key pinning — there is no cryptographic policy here to inherit.

Spawned non-interactively with `BatchMode=yes`, all three `StrictHostKeyChecking` modes behave
correctly and, critically, **fail fast rather than hang on an absent tty** (measured):
`yes` and the default `ask` both exit immediately with `Host key verification failed`;
`accept-new` silently trusts and connects. Use `BatchMode=yes` with the user's own
`~/.ssh/known_hosts` and default strictness, and surface the failure verbatim with "run
`ssh <host>` once in a terminal to verify its key". A first connection the user has already
made in their own shell is a better trust event than one an Electron app makes for them.

### 5. Does this subsume the TLS follow-up?

**No — they are independent, and neither blocks the other.** SSH covers desktop→desktop only:
the app can spawn `ssh`, so it never needs TLS for that leg. The mobile client cannot spawn
`ssh`, so the phone→desktop leg keeps exactly the exposure it has today, and TLS (or the
`tailscale` bind) remains its only answer. What SSH does change is the *priority*: TLS stops
being the fix for the desktop path and becomes a mobile-only item, which is the lower-traffic
one.

### 6. Windows

Assume nothing; two things are known and two need verification on a real Windows host:

- **Known:** the unix-socket forward is macOS/Linux only, so Windows takes the TCP-port form
and inherits the port race. And any new state file must repeat the existing win32 exemption —
Node reports `0o666` for every writable file there (the AO-79 finding #4 in `remotes-store.ts`,
mirroring `cli/remote.go:154`).
- **Needs verification:** `ssh.exe` ships with Windows 10+ but as an *optional feature*, so its
presence must be probed and its absence must be a real message rather than an `ENOENT`; and
`child.kill()` on win32 maps to `TerminateProcess`, which should drop the forward (ssh has no
descendants here) but has not been measured.

Precedent exists either way: main already owns a long-lived child process with platform
divergence (`daemon-owner.ts`).

### Recommendation

**Do it — documentation first, then the small version. Size: S then M.**

1. **Ship the recipe, not the feature (S, no code).** Because the proxy needs no change and the
CLI ignores SSH entries, *the entire transport benefit is available today*: the user runs
`ssh -N -L 3011:127.0.0.1:3011 box` and saves `http://127.0.0.1:3011` as an ordinary host. Fold
that into the setup documentation follow-up, together with `"bind": "127.0.0.1"`. Users get
encrypted transport this week, and it doubles as the acceptance test for step 2.
2. **Then let the app spawn it (M).** An optional `ssh` field on `RemoteEntry`, a tunnel manager
in main, and one `await dialable(entry)` at the four call sites that consume an entry
(`remotes:add`, `remotes:probe`, `remotes:request`, `remotes:connect`), plus an SSH mode in the
add-host dialog. Nothing in `remote-proxy.ts` changes.
3. **Drop** the loopback-listener variant, for the two measured reasons above.
4. **Unblock** the TLS item as mobile-only rather than sequencing it after this.

The one honest argument against doing step 2 at all: `"bind": "tailscale"` already delivers
encrypted transport with zero AO code, and the EDD already names it. Step 2 buys ergonomics for
people who have SSH and do not want Tailscale — real, but it is ergonomics, not a new security
property. Step 1 is what actually retires the accepted risk.
