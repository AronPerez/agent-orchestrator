# 1. A second, authenticated, plaintext LAN listener for mobile access

Date: 2026-07-07
Status: Accepted

## Context

The daemon binds `127.0.0.1` only. AGENTS.md carries a hard rule: _"The daemon is
a loopback-only sidecar. Do not make the bind host configurable or expose it beyond
`127.0.0.1`."_ That rule keeps the Loopback Listener safe **without authentication**
— the OS guarantees nothing off-box can reach it.

We want a physical phone to use the app over the local network. The only prior
mechanism was a standalone Node proxy (`ao-phone-proxy.js`) run by hand, with
IP trust-on-first-connect and no password. The user rejected the proxy approach and
asked for an in-app "Connect Mobile" feature.

Two forces collide: exposing anything to the LAN removes the loopback safety
guarantee, and the target mobile app is **Expo/React Native**, where trusting a
self-signed TLS cert (fingerprint pinning) requires native modules across three
transports (`fetch`, the `/mux` WebSocket, and the xterm WebView) — a large, risky
effort at odds with the desired scope.

## Decision

Add a **second HTTP listener inside the daemon**, bound to the LAN, gated by auth.
The Loopback Listener is left byte-for-byte unchanged (desktop/CLI stay
unauthenticated). This **overrides the AGENTS.md loopback-only hard rule**, by
explicit user decision on 2026-07-07; AGENTS.md should be amended to scope that rule
to the Loopback Listener.

Security posture:

- **On-demand.** The LAN Listener does not exist until Connect Mobile is enabled;
  disabling closes the socket. Default off — zero standing LAN surface.
- **Single rotating Connection Password**, 8-char alphanumeric, stored only as a
  hash, compared constant-time. Sent as `Authorization: Bearer <password>` on both
  REST and the RN WebSocket (RN's WebSocket header option). Rotating drops the
  current phone.
- **Per-source Lockout** after 5 failed attempts (not global — a hostile device
  must not be able to lock out the real phone).
- **App API only** on the LAN Listener; daemon-control routes keep their existing
  loopback-only guard (`localControlRequest`) with no change.
- **Plaintext transport (HTTP), accepted.** No TLS. The feature is
  **home-network-only** and the UI says so. The Pairing QR therefore carries only
  host+port (non-secret); the Connection Password is delivered out-of-band (read off
  the desktop screen, typed into the phone), so a captured QR alone cannot connect.
- State persists to `~/.ao/mobile/config.json` (atomic write), honoring the
  "all state under `~/.ao`" rule. The listener re-binds on the default port with an
  ephemeral fallback; the QR always reflects the actually-bound port.

## Consequences

- The daemon gains a network-facing, authenticated attack surface whenever Connect
  Mobile is on. Loopback behaviour is unaffected, so desktop/CLI carry no regression
  risk.
- On untrusted networks the Connection Password and all traffic are exposed to
  sniffers. This is an accepted, stated limitation, not an oversight.
- TLS is deliberately deferred. A future upgrade (TLS listener + a `fingerprint`
  field in the Pairing QR + RN cert pinning) is additive: it does not require
  reworking the auth, lifecycle, or persistence chosen here.
- AGENTS.md must be updated so the loopback-only rule reads as scoped to the
  Loopback Listener, or future agents will (correctly) flag this code as a violation.

## Amendment — 2026-08-08: the LAN Listener now serves a UI, and the proxy is retired

The decision above stands unchanged; this records what the listener grew into.
Three changes landed on `develop` (`ff9ea706c`, `ae3394e9f`, `2399595db`):

- **The LAN Listener serves the web UI as well as the app API**, from its own
  origin (`backend/internal/httpd/webui`). The static shell is served _without_ the
  Connection Password — it is the password prompt itself, and a browser cannot send
  a credential it has not been asked for yet. Every data route stays gated. The
  bypass is scoped by the router: a request is treated as UI only when the router
  has no handler for it, and the static handler is served directly rather than
  through the router, so no router middleware runs on the unauthenticated path.
- **UI and API are same-origin.** That is what retires `AO_ALLOWED_ORIGINS` for
  this flow: origin trust is now carried by host-equality (`Origin` host:port ==
  request `Host`), so a daemon-served page needs no allowlist entry and no
  configuration at all. `AO_ALLOWED_ORIGINS` remains load-bearing **only for a
  separately hosted UI** — a Vite dev server, or a build served from another host.
- **A third credential channel, the `ao_conn` cookie**, minted by
  `POST /api/v1/auth/login` (204 + `HttpOnly; SameSite=Strict; Path=/`). It exists
  because a browser cannot put a header on everything: `EventSource` sends cookies
  and nothing else. Because a cookie is the one credential a browser attaches to
  requests a hostile page initiates, both minting it and authenticating with it
  require a strict origin — unlike `Authorization: Bearer` and the `ao.bearer.*`
  subprotocol, which no page can forge and which stay exempt.

**`ao-phone-proxy` is retired**, together with the `lan-web` Vite service. The
proxy existed to launder `Origin` headers so a browser could reach a loopback-only
daemon — the approach this ADR's Context records the user rejecting. It is now
unnecessary rather than merely unwanted: the LAN Listener binds the port itself
(`:3011` by default), and its Connection Password plus per-source lockout replaces
the proxy's IP trust-on-first-connect. Note the property that changes: TOFU pinned
exactly one device forever, whereas any device holding the password may now connect
— which is the pairing model this ADR chose, not a regression introduced by the
retirement.

**Debugging trap worth knowing:** `POST /api/v1/auth/login` is answered by
`authMiddleware`, which sits _outside_ `requestLogger`. A login — successful or
failed — leaves **no entry in the daemon access log**. Do not read that silence as
"the request never arrived"; instrument the handler or look at the client.

TLS remains deferred exactly as decided above. The bind host may now be _narrowed_
(`bind: all | tailscale | <ip>` in `~/.ao/mobile/config.json`); binding the
Tailscale interface yields WireGuard-encrypted transport without any TLS work,
which is a mitigation for the plaintext consequence above, not a replacement for
the deferred TLS decision.
