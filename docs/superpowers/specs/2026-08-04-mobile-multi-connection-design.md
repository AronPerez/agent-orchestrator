# Mobile multi-connection (multi-node) switcher — approved design

**Package:** `packages/mobile` (Expo/React Native iOS app)

## Problem

The app pairs with exactly one daemon. The user runs ~10 nodes, each with its own `ao`
instance, and wants to toggle between them. All persistence funnels through
`packages/mobile/lib/config.ts`, so the change is contained: turn the single stored
server into a list plus an active id, and keep the exported function signatures so no
other call site has to move.

## Storage

Only `lib/config.ts` knows about persistence.

| Key                      | Where        | Contents                                                        |
| ------------------------ | ------------ | --------------------------------------------------------------- |
| `ao.servers`             | AsyncStorage | `[{ id, label, host, httpPort, muxPort, secure }]` — no secrets |
| `ao.activeServerId`      | AsyncStorage | active node's id                                                |
| `ao.serverPassword:<id>` | SecureStore  | one password per node                                           |

Passwords must **never** reach AsyncStorage — preserve the reasoning in the existing
`config.ts` comments.

**Migration on first load:** if legacy `ao.serverConfig` exists and `ao.servers` does
not, convert it into a single entry (label defaults to the host) and move the legacy
`ao.serverPassword` SecureStore value to the per-id key. Already-paired phones keep
working with no user action. Keep the existing legacy path that moved a password out of
the AsyncStorage blob.

## API

Keep these signatures exactly as they are, so `pair.tsx`, `ManualConnectSheet.tsx`,
`store.tsx`, `session/[id].tsx` and `settings.tsx` need no changes to their existing
calls:

- `loadConfig()` — returns the active node's `ServerConfig`, or `DEFAULT_CONFIG` when
  there is none.
- `saveConfig(cfg)` — **upsert by `host:port`**, then make it active. This one rule is
  what makes pairing a second node _add_ a node instead of overwriting the first, with
  zero edits in `pair.tsx` / `ManualConnectSheet.tsx`. Re-pairing an existing
  `host:port` edits that entry in place.
- `clearConfig()` — removes the **active** entry and its password, then activates the
  next remaining node (or none). `lib/disconnect.ts` `forgetServer()` keeps working
  unchanged; keep its ordering guarantees and its existing tests passing.

New exports: `listServers()`, `switchServer(id)`, `removeServer(id)`,
`renameServer(id, label)`.

## Switch flow

- `switchServer(id)` then `reloadConfig()`. The store already restarts its REST poll
  when config changes — do not add a second poll mechanism.
- Re-run push registration for the newly active node. **Push scope is active node only**
  (decided): `push.ts` already unregisters the previous daemon on a config change —
  reuse that, do **not** build multi-daemon registration.
- Reset the persisted active project (`ao.activeProject`) to `all` on switch, because
  project ids are per-node.

## UI — Settings only

No board-header switcher (decided).

- A `Servers` section in `app/(tabs)/settings.tsx`: saved nodes as `label · host:port`,
  checkmark on the active one, tap to switch, a delete action per row, and an
  `Add server` button that opens the existing connect sheet (`app/sheets/connect.tsx`).
- `lib/ManualConnectSheet.tsx` gains **one** optional `Name` field defaulting to the
  host — with 10 nodes, raw Tailscale IPs are unreadable. QR-paired nodes label from
  the host.
- Follow `DESIGN.md` and reuse the existing settings row primitives. Do not invent new
  visual patterns.

## Edge case

If a session terminal screen is open when the active host changes, that session id does
not exist on the new node — pop back to the board. `app/session/[id].tsx` already loads
config; a small effect watching the active host is enough.

## Tests

Add `lib/config.test.ts` (vitest already runs in this package — see
`lib/disconnect.test.ts` for the mocking style). Cover:

1. legacy single-config migration,
2. upsert-dedupe by `host:port`,
3. `switchServer`,
4. `removeServer` / `clearConfig` on the active node falling back to the next.

Keep the existing disconnect tests green.

## Out of scope — do not build

Unified all-nodes view, per-node push registration, board-header switcher, node
health/online indicators, drag reordering, import/export.
