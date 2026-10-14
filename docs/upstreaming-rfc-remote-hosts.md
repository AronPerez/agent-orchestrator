## Problem

Agents are long-running and machine-bound. With a laptop and a desktop (or a VM), the only ways to see both today are SSH or pointing the whole app at one host at a time. #3853 and discussion #2855 ask for this; #3883, #4084 and #4309 each propose a slice of it.

## Proposal

Let one desktop app connect to **N** AO daemons at once — the local one plus saved remote hosts — and show every host's projects and sessions in one tree, each remote row labelled with its host. Open, watch and type into a session on any host without a mode switch.

**Mechanism (renderer-side fan-out, per-host loopback proxy):**

- Remote hosts reuse the existing opt-in LAN listener and its connection password (ADR 0001). **No daemon change is required**: the listener already accepts `Authorization: Bearer`.
- The renderer cannot set that header on `EventSource`/`WebSocket`, and `app://renderer` has no CORS standing with a remote daemon, so Electron **main** runs one loopback proxy per host: `127.0.0.1:<ephemeral>/<128-bit token>/…`, token stripped before forwarding, Bearer injected, renderer `Origin` stripped, SSE/WebSocket streamed. No token ⇒ 404 and nothing forwarded. Torn down on disconnect and on quit.
- `~/.ao/remotes.json` (mode 0600, refused if looser) is the saved-host store, shared verbatim with `ao --url`. The password never enters the renderer process.
- Every addressable thing becomes a `Ref = {host, id}`; ids are never rewritten. This is load-bearing: a project id is `filepath.Base(path)` on every machine, so bare ids collide by construction.
- Hosts connect after first paint; a sleeping host is a labelled failed section with a retry, never a blank tree.
- **Everything ships dark behind a `Remote hosts (experimental)` switch** in Settings, modelled on Developer Mode, default off. Off means no saved host is read, probed or connected — a reviewer can verify the off state from the network side.

**Trust boundary:** one operator, machines they own, a trusted network (LAN or Tailscale). Plaintext HTTP on the LAN path is unchanged from Connect Mobile; https upstreams use TLS; an `ssh -N -L` recipe and `"bind": "127.0.0.1"` take the port off the network entirely.

**Already built and verified** on two real machines in a fork, with a security review of the proxy/token/credential path (four fixes folded in) and an accessibility pass. ~2,400 tests.

## Proposed PR series (each independently mergeable, each dark behind the flag)

1. `feat(settings)`: the `remoteHosts` flag (~60 lines)
2. `feat(hosts)`: `HostId`/`Ref` primitives (30 lines)
3. `feat(remotes)`: saved-host store, authenticated request, password-free IPC
4. `feat(remotes)`: token-gated loopback proxy + registry — *requests a security reviewer*
5. `feat(hosts)`: per-host clients + flag-gated boot
6. `feat(hosts)`: add/edit/remove hosts UI
7. `feat(daemon)`: `GET /api/v1/fs/dirs` (read-only, names only, capped) + remote folder picker
8. `refactor(hosts)`: thread `Ref` through reads; host-qualified routes `/host/$hostId/…`
9. `feat(hosts)`: per-host workspace queries, SSE and terminals
10. `refactor(hosts)`: route writes by `Ref`
11. `feat(hosts)`: one tree across hosts, telemetry, hostile-daemon tests
12. `docs`: setup, trust boundary, ADR 0003

## Questions for maintainers

1. Which remote model do you want — one active workspace over SSH (#3883), one active remote over Tailscale HTTPS (#4084), or N hosts at once (this)? This decides the back half of the series.
2. Is a loopback proxy with a path-borne per-activation token acceptable as a standing mechanism? (Header injection via `webRequest` and CORS negotiation were rejected for concrete reasons — happy to write them up.)
3. Flag placement: sibling of Developer Mode (proposed) or nested under it?
4. `remotes.json` as the store shared with the CLI: accept the 0600 file, or require `safeStorage` for the app at the cost of forking the store?
5. `GET /api/v1/fs/dirs`: acceptable as an authenticated read-only endpoint, or absolute-path entry only?
6. Any objection to the `/host/$hostId/…` URL shape (user-visible, permanent)?
7. Relationship to #4309 (browser client): if it merges we contribute our rebinding/credential-gating tests to it rather than a competing design.
8. Can a maintainer own review for ~6 weeks, with a quiet window for the two mechanical `Ref` PRs?
