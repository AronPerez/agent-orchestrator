# `ao browser` for Remote Sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `ao browser <cmd>` run inside a worker session on a **remote host** (a federated daemon the desktop app is attached to) drives the same visible Browser panel the user sees for that session in the desktop app, exactly like local sessions do today.

**Architecture:** Today the browser bridge is: worker CLI → its own daemon's loopback HTTP (`/api/v1/browser/commands`, capability-checked) → `browserruntime.Broker` → Electron over a **local unix socket** (`browserruntime.Listen`). A remote host's daemon has no Electron connected to that socket, so every command fails with `BROWSER_RUNTIME_UNAVAILABLE`. The fix keeps the CLI and the daemon's authorization path 100% unchanged and adds a **second transport into the same broker**: the desktop app dials each connected remote daemon with a plain HTTP/1.1 `Upgrade: ao-browser-runtime` request routed through the app's existing per-host loopback proxy (which injects `Authorization: Bearer <password>`); the daemon authenticates it with the normal LAN auth middleware, hijacks the connection, and hands the raw socket to the broker. The existing newline-JSON protocol (`browser-runtime-link.ts` ↔ `broker.go`) runs over it unchanged. On the app side, remote commands execute against the same `browserViewHost`, with the persistent-profile key **scoped per host** (project ids collide across hosts by construction).

**Tech Stack:** Go (chi, `net/http` hijack, stdlib only — no new deps), TypeScript/Electron main (`node:http` upgrade client, `node:net`, `node:crypto` — no new deps), vitest, `go test`.

## Global Constraints

- No new Go or npm dependencies. The daemon side uses `http.Hijacker` + stdlib; the app side uses `node:http`/`node:net`/`node:crypto`.
- The endpoint is mounted at root (`/browser-runtime`), like `/mux` — **not** under `/api/v1` — so the OpenAPI spec and `apispec` parity test (`backend/internal/httpd/apispec/parity_test.go` only walks `/api/v1/*`) are untouched. Do not add it to `openapi.yaml`.
- CLI (`backend/internal/cli/browser.go`) behavior is unchanged except help text: the worker always talks to its own daemon over loopback; that already works on remote hosts.
- The daemon's authorization model is unchanged: per-session capability (`X-AO-Browser-Capability`) is still validated by `service/browser` before any command reaches the broker. The new endpoint authenticates the **app**, not sessions: LAN listener → `authMiddleware` (Bearer password, injected by the app's per-host proxy); loopback listener → ambient authority, consistent with AO's model (any local process already has full daemon control).
- Local-wins policy: a remote runtime connection must never displace a connected **local** (unix-socket) runtime; a local connection displaces anything; a remote connection displaces another remote. This prevents two desktop apps (one on the daemon's own machine, one attached over LAN) from flapping the bridge.
- Persistent-profile partition invariant: the string the **renderer** passes as the session's host and the string the **main-process remote link** uses as its host scope must be identical for the same daemon (both are the remote entry's `url`; `"local"`/`undefined` means the local daemon and stays unscoped so existing on-disk partitions are preserved).
- Scoped profile keys must satisfy `PERSISTENT_PROFILE_KEY_PATTERN` (`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`, no `..`) in `frontend/src/main/browser-view-host.ts` — hence hash-based scoping, never URL concatenation.
- Backend verification: `cd backend && go test ./...` and `npm run lint` (repo root; runs go test + golangci-lint). Frontend: `cd frontend && npm run typecheck && npm test` (vitest). Note for AO worktrees: if `frontend/node_modules` is missing, symlink it from the main checkout (`ln -s ~/dev/agent-orchestrator/frontend/node_modules frontend/node_modules`) rather than reinstalling.
- Commit style: conventional commits like the existing history (`feat(...)`, `fix(...)`, `chore(...)`).
- Branch off `develop` (the fork's work branch).

## Out of scope (do not build)

- `ao --url <remote> browser …` from a human terminal: `ao browser` requires `AO_SESSION_ID` + `AO_BROWSER_CAPABILITY`, which only exist inside worker sessions. The existing usage error is correct.
- Mirroring one session's live page into two desktop apps at once; a standby/promotion queue for multiple connected apps (last-wins among remotes, local-wins overall, is the whole policy).
- TLS/`wss` remotes: the app's per-host proxy speaks plain HTTP/1.1 to the upstream today (`remote-proxy.ts` uses `node:http` + `netConnect`); this feature inherits that, it does not extend it.
- The browser-only LAN web client (`:3000`): it has no `window.ao`, so the native panel and this bridge do not apply.
- Session-id collision handling across hosts: browser targets stay keyed by bare `sessionId` (ULID-unique in practice); only the profile key is host-scoped because *that* collides by construction.

## Architecture reference (for any task's implementer)

Existing pieces you will touch or must not break:

| Piece | File | Role |
|---|---|---|
| CLI | `backend/internal/cli/browser.go` | reads `AO_SESSION_ID`/`AO_BROWSER_CAPABILITY`, POSTs `/api/v1/browser/commands` to its own daemon |
| AuthZ | `backend/internal/service/browser/service.go` | capability + session liveness + action allowlist + `profileKey` (raw project id) |
| Broker | `backend/internal/browserruntime/broker.go` | single active runtime conn; newline-JSON `hello`/`command`/`result`/`cancel` frames, protocol version 2 |
| Local listener | `backend/internal/browserruntime/listen_unix.go`, `backend/internal/daemon/daemon.go:447-461` | unix socket + per-launch token (stdin handoff) |
| Router | `backend/internal/httpd/router.go:48-78` | shared by loopback and LAN listeners; `/mux` is the long-lived-conn precedent (`terminal_mux.go`) |
| LAN auth | `backend/internal/httpd/auth.go` | Bearer password middleware on the LAN listener |
| App local link | `frontend/src/main/browser-runtime-link.ts`, `frontend/src/main.ts` (`establishBrowserRuntimeLink`, ~line 1001) | net.Socket client speaking the broker protocol; `execute` runs `browserViewHost.execute(sessionId, action, args, signal, profileKey)` |
| Per-host proxy | `frontend/src/main/remote-proxy.ts` | loopback proxy per remote; injects `Authorization` on requests **and** upgrades; `base` = `http://127.0.0.1:<port>/<token>` |
| Registry | `frontend/src/main/remote-registry.ts`, wired in `frontend/src/main.ts` ~line 1947 | one proxy per connected host, keyed by remote `url` |
| Panel binding | `frontend/src/main/browser-view-host.ts` (`ensureSession`, `persistentPartition`, `browser:ensure` IPC), `frontend/src/preload.ts` (~line 272), `frontend/src/renderer/hooks/useBrowserView.ts`, `frontend/src/renderer/hooks/usePersistentBrowserProfile.ts`, `frontend/src/renderer/components/SessionView.tsx` | renderer ensures a view per `sessionId`, optional `profileKey` |
| Host identity | `frontend/src/renderer/lib/hosts.ts` (`HostId` = `"local"` or the remote url), `frontend/src/renderer/lib/host-clients.ts` | every renderer object is addressed by `{host, id}` |

Wire protocol (unchanged, now also over the upgrade transport): client sends `{"type":"hello","version":2,"token":...}\n` then answers `{"type":"command",...}` frames with `{"type":"result","requestId":...,"ok":true,"result":{...}}\n`; server may send `{"type":"cancel","requestId":...}`. Remote conns send hello **without** a token (the HTTP layer already authenticated them).

---

### Task 1: Broker accepts pre-authenticated remote connections (local-wins)

**Files:**
- Modify: `backend/internal/browserruntime/broker.go`
- Test: `backend/internal/browserruntime/broker_test.go`

**Interfaces:**
- Consumes: existing `Broker`, `serveConn`, `wireMessage`.
- Produces: `func (b *Broker) ServeRemoteConn(ctx context.Context, conn net.Conn)` — blocks until the connection dies; skips the runtime-token check; never displaces a connected local runtime. Task 2's HTTP handler calls exactly this. `Serve` (unix path) behavior is unchanged.

- [ ] **Step 1: Read the existing broker tests** (`broker_test.go`) to mirror their helpers (how they build listeners/conns, write frames, wait for `Status().Connected`). Reuse those helpers; do not invent a parallel harness.

- [ ] **Step 2: Write the failing tests**

Add to `broker_test.go` (adapt helper names to what Step 1 found — the assertions below are the contract):

```go
// A remote conn authenticates at the HTTP layer, so hello carries no token
// even when the broker holds one. It must still connect and execute.
func TestServeRemoteConnAcceptsTokenlessHelloAndExecutes(t *testing.T) {
	b := New(testLogger(t), "launch-secret")
	client, server := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() { b.ServeRemoteConn(context.Background(), server); close(done) }()

	writeFrame(t, client, `{"type":"hello","version":2}`)
	waitConnected(t, b)

	go func() {
		frame := readFrame(t, client) // the command frame
		var msg map[string]any
		_ = json.Unmarshal([]byte(frame), &msg)
		writeFrame(t, client, fmt.Sprintf(
			`{"type":"result","requestId":%q,"ok":true,"result":{"url":"http://x"}}`,
			msg["requestId"]))
	}()

	res, err := b.Execute(context.Background(), "s1", "open",
		map[string]any{"url": "http://x"}, "")
	if err != nil {
		t.Fatalf("execute over remote conn: %v", err)
	}
	if res.Value.(map[string]any)["url"] != "http://x" {
		t.Fatalf("unexpected result: %#v", res.Value)
	}
	client.Close()
	<-done
}

// Wrong protocol version is still rejected on the remote path.
func TestServeRemoteConnRejectsWrongVersion(t *testing.T) {
	b := New(testLogger(t), "")
	client, server := net.Pipe()
	go b.ServeRemoteConn(context.Background(), server)
	writeFrame(t, client, `{"type":"hello","version":1}`)
	expectClosed(t, client) // read returns EOF/err promptly
	if b.Status().Connected {
		t.Fatal("wrong-version remote conn must not connect")
	}
}

// Local wins: while a local (token-checked) runtime is connected, a remote
// conn is closed without replacing it; when local disconnects, a remote
// retry connects.
func TestRemoteConnNeverDisplacesLocal(t *testing.T) {
	b := New(testLogger(t), "tok")
	local := connectLocal(t, b, "tok") // helper: listener + Serve + hello with token
	waitConnected(t, b)

	remoteClient, remoteServer := net.Pipe()
	go b.ServeRemoteConn(context.Background(), remoteServer)
	writeFrame(t, remoteClient, `{"type":"hello","version":2}`)
	expectClosed(t, remoteClient)
	if !b.Status().Connected {
		t.Fatal("local conn must survive a remote attempt")
	}

	local.Close() // local goes away
	waitDisconnected(t, b)

	retryClient, retryServer := net.Pipe()
	go b.ServeRemoteConn(context.Background(), retryServer)
	writeFrame(t, retryClient, `{"type":"hello","version":2}`)
	waitConnected(t, b) // remote now owns the bridge
}

// Local displaces remote (the machine that owns the daemon gets its panel back).
func TestLocalConnDisplacesRemote(t *testing.T) {
	b := New(testLogger(t), "tok")
	remoteClient, remoteServer := net.Pipe()
	go b.ServeRemoteConn(context.Background(), remoteServer)
	writeFrame(t, remoteClient, `{"type":"hello","version":2}`)
	waitConnected(t, b)

	_ = connectLocal(t, b, "tok")
	expectClosed(t, remoteClient) // old remote conn is closed by replacement
	waitConnected(t, b)
}
```

If `broker_test.go` lacks equivalents of `writeFrame`/`readFrame`/`waitConnected`/`waitDisconnected`/`expectClosed`/`connectLocal`, write them once in the test file (poll `b.Status()` with a deadline; `expectClosed` = set a read deadline and require a read error).

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/browserruntime/ -run 'TestServeRemote|TestRemoteConn|TestLocalConnDisplaces' -v`
Expected: FAIL — `b.ServeRemoteConn undefined`.

- [ ] **Step 4: Implement**

In `broker.go`:

```go
// connClass distinguishes how a runtime reached the broker. Local is the
// desktop app on this machine (unix socket, per-launch token); remote is a
// desktop app on another machine (HTTP upgrade, authenticated by the LAN
// credential before the broker ever sees the connection).
type connClass int

const (
	classLocal connClass = iota
	classRemote
)
```

Add the field to `Broker` (next to `conn`): `connClass connClass`.

Change `Serve` to pass the class:

```go
		go b.serveConn(ctx, conn, classLocal)
```

Add:

```go
// ServeRemoteConn adopts one already-authenticated connection from the HTTP
// upgrade endpoint and blocks until it closes. The HTTP layer (LAN credential,
// or loopback ambient authority) is the authenticator, so the hello token is
// not checked. A remote runtime never displaces a connected local one: the
// machine that owns the daemon keeps its browser panel, and the remote app's
// reconnect loop takes over only after the local app disconnects.
func (b *Broker) ServeRemoteConn(ctx context.Context, conn net.Conn) {
	b.serveConn(ctx, conn, classRemote)
}
```

Change `serveConn(ctx context.Context, conn net.Conn)` → `serveConn(ctx context.Context, conn net.Conn, class connClass)`:

1. Hello validation: token is only checked for local conns —

```go
	if !scanner.Scan() ||
		json.Unmarshal(scanner.Bytes(), &hello) != nil ||
		hello.Type != "hello" ||
		hello.Version != ProtocolVersion ||
		(class == classLocal && !validRuntimeToken(b.token, hello.Token)) {
		_ = conn.Close()
		return
	}
```

2. Adoption block gains the policy (replace the existing lock section):

```go
	b.mu.Lock()
	if class == classRemote && b.conn != nil && b.connClass == classLocal {
		b.mu.Unlock()
		_ = conn.Close()
		b.log.Info("browser runtime: remote runtime rejected; local runtime is connected")
		return
	}
	old := b.conn
	b.conn = conn
	b.connClass = class
	b.connectedAt = time.Now().UTC()
	pending := b.takePendingLocked()
	b.mu.Unlock()
```

`disconnect` needs no class reset (it only clears when `b.conn == conn`), but for hygiene set `b.connClass = classLocal` when clearing `b.conn = nil`.

- [ ] **Step 5: Run the tests to verify they pass, plus the whole package**

Run: `cd backend && go test ./internal/browserruntime/ -v`
Expected: PASS, including all pre-existing tests (the `Serve` path must be behaviorally identical).

- [ ] **Step 6: Commit**

```bash
git add backend/internal/browserruntime/broker.go backend/internal/browserruntime/broker_test.go
git commit -m "feat(browserruntime): accept pre-authenticated remote runtime conns with local-wins policy"
```

---

### Task 2: `/browser-runtime` upgrade endpoint + daemon wiring

**Files:**
- Create: `backend/internal/httpd/browser_runtime_bridge.go`
- Create: `backend/internal/httpd/browser_runtime_bridge_test.go`
- Modify: `backend/internal/httpd/api.go` (APIDeps)
- Modify: `backend/internal/httpd/router.go` (mount, next to `mountTerminalMux` at line ~70)
- Modify: `backend/internal/daemon/daemon.go` (pass the broker into the deps struct where `Browser: browserService` is set, ~line 434)

**Interfaces:**
- Consumes: `(*browserruntime.Broker).ServeRemoteConn(ctx context.Context, conn net.Conn)` from Task 1.
- Produces: HTTP endpoint `GET /browser-runtime` with headers `Connection: Upgrade`, `Upgrade: ao-browser-runtime` → `101 Switching Protocols`, then the raw broker wire protocol on the socket. Task 3's app-side dial targets exactly this. New exported type `httpd.BrowserRuntimeBridge` and field `APIDeps.BrowserRuntime`.

- [ ] **Step 1: Write the failing tests**

`backend/internal/httpd/browser_runtime_bridge_test.go` (mirror logger/config helpers from `terminal_mux_test.go`):

```go
package httpd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
)

// dialUpgrade performs the raw client half of the handshake and returns the
// conn positioned after the 101 response headers.
func dialUpgrade(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(conn,
		"GET /browser-runtime HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: Upgrade\r\nUpgrade: ao-browser-runtime\r\n\r\n")
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("expected 101, got %q", status)
	}
	for { // drain headers
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	if reader.Buffered() > 0 {
		t.Fatal("server must not send frames before the client hello")
	}
	return conn
}

func TestBrowserRuntimeBridgeRoundTrip(t *testing.T) {
	broker := browserruntime.New(testLogger(t), "local-token")
	r := chi.NewRouter()
	mountBrowserRuntimeBridge(r, broker, testLogger(t))
	srv := httptest.NewServer(r)
	defer srv.Close()

	conn := dialUpgrade(t, srv.Listener.Addr().String())
	defer conn.Close()
	fmt.Fprintf(conn, "{\"type\":\"hello\",\"version\":2}\n")

	deadline := time.Now().Add(2 * time.Second)
	for !broker.Status().Connected {
		if time.Now().After(deadline) {
			t.Fatal("broker never saw the upgraded conn")
		}
		time.Sleep(10 * time.Millisecond)
	}

	go func() {
		scanner := bufio.NewScanner(conn)
		if !scanner.Scan() {
			return
		}
		var msg map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &msg)
		fmt.Fprintf(conn,
			"{\"type\":\"result\",\"requestId\":%q,\"ok\":true,\"result\":{\"url\":\"http://x\"}}\n",
			msg["requestId"])
	}()

	res, err := broker.Execute(context.Background(), "s1", "open",
		map[string]any{"url": "http://x"}, "")
	if err != nil {
		t.Fatalf("execute over upgraded conn: %v", err)
	}
	if res.Value.(map[string]any)["url"] != "http://x" {
		t.Fatalf("unexpected result: %#v", res.Value)
	}
}

func TestBrowserRuntimeBridgeRequiresUpgradeHeaders(t *testing.T) {
	broker := browserruntime.New(testLogger(t), "")
	r := chi.NewRouter()
	mountBrowserRuntimeBridge(r, broker, testLogger(t))
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/browser-runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("plain GET: want 426, got %d", resp.StatusCode)
	}
}

func TestBrowserRuntimeBridgeNotMountedWithoutBridge(t *testing.T) {
	r := chi.NewRouter()
	mountBrowserRuntimeBridge(r, nil, testLogger(t))
	srv := httptest.NewServer(r)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/browser-runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("nil bridge: want 404, got %d", resp.StatusCode)
	}
}
```

If `terminal_mux_test.go` has no reusable `testLogger`, use `slog.New(slog.NewTextHandler(io.Discard, nil))` inline.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/httpd/ -run TestBrowserRuntimeBridge -v`
Expected: FAIL — `mountBrowserRuntimeBridge undefined`.

- [ ] **Step 3: Implement the bridge**

`backend/internal/httpd/browser_runtime_bridge.go`:

```go
package httpd

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// browserRuntimePath is where the desktop app's browser runtime attaches for
// REMOTE daemons. Mounted at root like /mux: it is a long-lived upgraded
// connection, deliberately outside the /api/v1 OpenAPI surface and the
// per-request timeout middleware. On the LAN listener it sits behind
// authMiddleware (the app's per-host proxy injects the connection password);
// on the loopback listener it is ambient authority like every other loopback
// route — a local process that could abuse it already owns the daemon.
const browserRuntimePath = "/browser-runtime"

// browserRuntimeUpgradeToken names the protocol in the Upgrade header. The
// wire protocol and its version live in the hello frame (browserruntime
// ProtocolVersion), not here.
const browserRuntimeUpgradeToken = "ao-browser-runtime"

// BrowserRuntimeBridge adopts an authenticated, upgraded connection as the
// daemon's Electron browser runtime. Implemented by *browserruntime.Broker.
type BrowserRuntimeBridge interface {
	ServeRemoteConn(ctx context.Context, conn net.Conn)
}

// mountBrowserRuntimeBridge registers the upgrade endpoint. A nil bridge means
// the daemon has no browser broker; the route is simply absent.
func mountBrowserRuntimeBridge(r chi.Router, bridge BrowserRuntimeBridge, log *slog.Logger) {
	if bridge == nil {
		return
	}
	r.Get(browserRuntimePath, browserRuntimeHandler(bridge, log))
}

// hijackedConn lets the broker's scanner see bytes the server's request reader
// buffered past the handshake before handing us the raw connection.
type hijackedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c hijackedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func browserRuntimeHandler(bridge BrowserRuntimeBridge, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), browserRuntimeUpgradeToken) ||
			!headerListContains(r.Header.Values("Connection"), "upgrade") {
			envelope.WriteAPIError(w, r, http.StatusUpgradeRequired, "upgrade_required",
				"BROWSER_RUNTIME_UPGRADE_REQUIRED",
				"This endpoint speaks the ao-browser-runtime upgrade protocol", nil)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal",
				"BROWSER_RUNTIME_HIJACK_UNSUPPORTED",
				"Connection cannot be upgraded on this listener", nil)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			log.Warn("browser runtime bridge: hijack failed", "err", err)
			return
		}
		if _, err := rw.WriteString(
			"HTTP/1.1 101 Switching Protocols\r\nUpgrade: " + browserRuntimeUpgradeToken +
				"\r\nConnection: Upgrade\r\n\r\n"); err != nil {
			_ = conn.Close()
			return
		}
		if err := rw.Flush(); err != nil {
			_ = conn.Close()
			return
		}
		// Blocks for the life of the runtime connection, exactly like the
		// terminal mux handler. The broker closes the conn on ctx cancellation
		// or replacement; process exit covers daemon shutdown.
		bridge.ServeRemoteConn(r.Context(), hijackedConn{Conn: conn, reader: rw.Reader})
	}
}

// headerListContains reports whether any comma-separated element of the given
// header values equals token, case-insensitively ("keep-alive, Upgrade").
func headerListContains(values []string, token string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 4: Run the bridge tests**

Run: `cd backend && go test ./internal/httpd/ -run TestBrowserRuntimeBridge -v`
Expected: PASS.

- [ ] **Step 5: Wire router and daemon**

1. `backend/internal/httpd/api.go` — add to `APIDeps` (next to `Browser controllers.BrowserService`):

```go
	// BrowserRuntime lets a remote desktop app attach as this daemon's browser
	// runtime over the HTTP upgrade bridge. Nil leaves the route unmounted.
	BrowserRuntime BrowserRuntimeBridge
```

2. `backend/internal/httpd/router.go` — in `NewRouterWithControl`, directly after `mountTerminalMux(r, termMgr, log)`:

```go
	mountBrowserRuntimeBridge(r, deps.BrowserRuntime, log)
```

Check `normalizeAPIDeps` (api.go): if it zeroes unknown fields or has per-field handling, leave `BrowserRuntime` untouched (nil-safe by construction).

3. `backend/internal/daemon/daemon.go` — in the deps literal that already contains `Browser: browserService` (~line 434), add:

```go
		BrowserRuntime: browserBroker,
```

- [ ] **Step 6: Run the full backend suite**

Run: `cd backend && go test ./internal/httpd/... ./internal/daemon/... ./internal/browserruntime/...`
Expected: PASS — including the apispec parity test (the route is at root, so `/api/v1` parity is untouched). If any route-inventory test in `httpd` snapshots root routes, update its expectation to include `/browser-runtime`.
Also run: `cd backend && go test ./...` before committing.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/httpd/browser_runtime_bridge.go backend/internal/httpd/browser_runtime_bridge_test.go backend/internal/httpd/api.go backend/internal/httpd/router.go backend/internal/daemon/daemon.go
git commit -m "feat(httpd): mount /browser-runtime upgrade bridge for remote desktop apps"
```

---

### Task 3: App link supports a dialed transport (`upgradeDial`)

**Files:**
- Modify: `frontend/src/main/browser-runtime-link.ts`
- Test: `frontend/src/main/browser-runtime-link.test.ts`

**Interfaces:**
- Consumes: the daemon endpoint from Task 2 (only in the integration sense; tests stub it).
- Produces, used verbatim by Task 5:
  - `type BrowserRuntimeDial = () => Promise<{ socket: net.Socket; head?: Buffer }>`
  - `BrowserRuntimeLinkOptions` gains `dial?: BrowserRuntimeDial` and `backoffMaxMs?: number` (default stays 2000)
  - `connectBrowserRuntime(address: string | net.TcpNetConnectOpts | null, options)` — `address` may be `null` when `dial` is provided
  - `export function upgradeDial(baseUrl: string): BrowserRuntimeDial` — performs `GET ${baseUrl}/browser-runtime` with `Connection: Upgrade` / `Upgrade: ao-browser-runtime` via `node:http` and resolves with the upgraded socket + `head` bytes
- Reconnect/backoff/framing/cancel logic is shared between transports — no duplication.

- [ ] **Step 1: Read the existing link tests** (`browser-runtime-link.test.ts`) to see how they spin `net.Server`s and assert frames. Mirror those patterns.

- [ ] **Step 2: Write the failing tests**

Add:

```ts
import { createServer as createHttpServer } from "node:http";

describe("upgradeDial", () => {
	it("dials through an HTTP upgrade and hands over head bytes", async () => {
		const server = createHttpServer();
		server.on("upgrade", (req, socket) => {
			expect(req.url).toBe("/browser-runtime");
			expect(req.headers.upgrade).toBe("ao-browser-runtime");
			socket.write(
				"HTTP/1.1 101 Switching Protocols\r\nUpgrade: ao-browser-runtime\r\nConnection: Upgrade\r\n\r\n" +
					'{"type":"command","requestId":"r1","sessionId":"s1","action":"open"}\n',
			);
		});
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const port = (server.address() as import("node:net").AddressInfo).port;

		const seen: string[] = [];
		const handle = connectBrowserRuntime(null, {
			dial: upgradeDial(`http://127.0.0.1:${port}`),
			execute: async (command) => {
				seen.push(command.requestId);
				return { ok: true };
			},
		});
		await vi.waitFor(() => expect(seen).toEqual(["r1"]));
		handle.dispose();
		server.close();
	});

	it("rejects a non-101 response and the link retries", async () => {
		const server = createHttpServer((_req, res) => {
			res.writeHead(401);
			res.end();
		});
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		const port = (server.address() as import("node:net").AddressInfo).port;
		const logs: string[] = [];
		const handle = connectBrowserRuntime(null, {
			dial: upgradeDial(`http://127.0.0.1:${port}`),
			execute: async () => ({}),
			log: (message) => logs.push(message),
		});
		await vi.waitFor(() =>
			expect(logs.some((entry) => entry.includes("dial failed"))).toBe(true),
		);
		expect(handle.connected).toBe(false);
		handle.dispose();
		server.close();
	});
});
```

Also add one assertion that `backoffMaxMs` is honored if the existing suite already tests backoff timing; if it does not, skip timing tests (they flake) — the option is exercised implicitly.

- [ ] **Step 3: Run to verify failure**

Run: `cd frontend && npx vitest run --config vite.renderer.config.ts src/main/browser-runtime-link.test.ts`
Expected: FAIL — `upgradeDial` is not exported / `null` address rejected.

- [ ] **Step 4: Implement**

In `browser-runtime-link.ts`:

1. Types:

```ts
export type BrowserRuntimeDial = () => Promise<{ socket: net.Socket; head?: Buffer }>;
```

Extend `BrowserRuntimeLinkOptions` with `dial?: BrowserRuntimeDial; backoffMaxMs?: number;`. Change `connectBrowserRuntime`'s first parameter type to `string | net.TcpNetConnectOpts | null`.

2. Backoff: in `scheduleReconnect`, replace `BACKOFF_MAX_MS` with `options.backoffMaxMs ?? BACKOFF_MAX_MS`.

3. `connect()` — support async dial while preserving epoch semantics (epoch is captured after `destroySocket()`, exactly as today):

```ts
	function connect() {
		if (disposed) return;
		destroySocket();
		buffer = "";
		decoder = new StringDecoder("utf8");
		const epoch = ++connectionEpoch;
		if (!options.dial) {
			if (address === null) throw new Error("connectBrowserRuntime requires an address or a dial");
			attach(net.connect(address as string | net.TcpNetConnectOpts), epoch, undefined, false);
			return;
		}
		void options
			.dial()
			.then(({ socket: dialed, head }) => {
				if (disposed || connectionEpoch !== epoch) {
					dialed.destroy();
					return;
				}
				attach(dialed, epoch, head, true);
			})
			.catch((error) => {
				log(`browser-runtime-link: dial failed: ${String(error)}`);
				if (!disposed && connectionEpoch === epoch) scheduleReconnect();
			});
	}
```

4. Extract today's `next.on("connect", …)` body plus listener wiring into `attach`:

```ts
	function attach(next: net.Socket, epoch: number, head: Buffer | undefined, alreadyConnected: boolean) {
		socket = next;
		const onReady = () => {
			if (disposed) {
				next.destroy();
				return;
			}
			connected = true;
			backoff = BACKOFF_INIT_MS;
			void send({ type: "hello", version: PROTOCOL_VERSION, token: options.token }, next, epoch).catch((error) => {
				log(`browser-runtime-link: hello failed: ${String(error)}`);
				next.destroy();
			});
			log("browser-runtime-link: connected");
		};
		next.on("data", (chunk) => consume(chunk, next, epoch));
		next.on("error", (error) => log(`browser-runtime-link: error: ${error.message}`));
		next.on("close", () => {
			if (socket !== next || connectionEpoch !== epoch) return;
			connected = false;
			socket = null;
			connectionEpoch += 1;
			cancelConnectionCommands();
			if (!disposed) scheduleReconnect();
		});
		if (alreadyConnected) {
			onReady();
			if (head && head.length > 0) consume(head, next, epoch);
		} else {
			next.on("connect", onReady);
		}
	}
```

5. The dial helper (same file, bottom):

```ts
import { request as httpRequest } from "node:http";

// Dials the daemon's /browser-runtime upgrade endpoint. baseUrl is the
// per-host loopback proxy base (http://127.0.0.1:<port>/<token>) — the proxy
// injects the remote credential on upgrade requests, so no auth is handled
// here and the password never enters this module.
export function upgradeDial(baseUrl: string): BrowserRuntimeDial {
	return () =>
		new Promise((resolve, reject) => {
			const request = httpRequest(`${baseUrl}/browser-runtime`, {
				headers: { connection: "Upgrade", upgrade: "ao-browser-runtime" },
			});
			request.on("upgrade", (_response, socket, head) => {
				socket.on("error", () => undefined); // owner attaches real handlers next tick
				resolve({ socket, head });
			});
			request.on("response", (response) => {
				response.resume();
				reject(new Error(`upgrade refused: HTTP ${response.statusCode}`));
			});
			request.on("error", reject);
			request.end();
		});
}
```

- [ ] **Step 5: Run the full link suite**

Run: `cd frontend && npx vitest run --config vite.renderer.config.ts src/main/browser-runtime-link.test.ts`
Expected: PASS, all pre-existing tests included (the unix/tcp path must be behaviorally identical).

- [ ] **Step 6: Commit**

```bash
git add frontend/src/main/browser-runtime-link.ts frontend/src/main/browser-runtime-link.test.ts
git commit -m "feat(desktop): browser runtime link over HTTP upgrade dial"
```

---

### Task 4: Host-scoped persistent profile keys

**Files:**
- Modify: `frontend/src/main/browser-view-host.ts` (next to `persistentPartition`, ~line 417)
- Test: `frontend/src/main/browser-view-host.test.ts`

**Interfaces:**
- Consumes: `PERSISTENT_PROFILE_KEY_PATTERN` / `persistentPartition` (unchanged).
- Produces: `export function scopedProfileKey(host: string | undefined, profileKey: string | undefined): string | undefined` — identity for local (`undefined`, `""`, `"local"`) hosts and for empty keys; for a remote host returns `` `r${sha256(`${host}\n${key}`).hex.slice(0, 40)}` `` (41 chars, always satisfies the partition pattern). Tasks 5 and 6 both import it — this single function is the partition invariant.

- [ ] **Step 1: Write the failing tests**

Add to `browser-view-host.test.ts` next to the `persistentPartition` tests:

```ts
describe("scopedProfileKey", () => {
	it("passes local and empty keys through untouched", () => {
		expect(scopedProfileKey(undefined, "agent-orchestrator")).toBe("agent-orchestrator");
		expect(scopedProfileKey("local", "agent-orchestrator")).toBe("agent-orchestrator");
		expect(scopedProfileKey("http://10.0.0.5:3001", "")).toBe("");
		expect(scopedProfileKey("http://10.0.0.5:3001", undefined)).toBeUndefined();
	});

	it("scopes a remote key deterministically per host", () => {
		const a = scopedProfileKey("http://10.0.0.5:3001", "agent-orchestrator");
		const b = scopedProfileKey("http://10.0.0.6:3001", "agent-orchestrator");
		expect(a).not.toBe("agent-orchestrator");
		expect(a).not.toBe(b); // same project id on two hosts → two cookie jars
		expect(a).toBe(scopedProfileKey("http://10.0.0.5:3001", "agent-orchestrator"));
	});

	it("always produces a partition-safe key", () => {
		const scoped = scopedProfileKey("http://10.0.0.5:3001", "a".repeat(64));
		expect(scoped && persistentPartition(scoped)).toMatch(/^persist:ao-browser-r[0-9a-f]{40}$/);
	});
});
```

- [ ] **Step 2: Run to verify failure**

Run: `cd frontend && npx vitest run --config vite.renderer.config.ts src/main/browser-view-host.test.ts -t scopedProfileKey`
Expected: FAIL — `scopedProfileKey` is not exported.

- [ ] **Step 3: Implement**

In `browser-view-host.ts` (extend the existing `node:crypto` import that provides `randomUUID` with `createHash`):

```ts
// A remote host's profileKey is a project id on THAT machine, and project ids
// collide across hosts by construction (the same project checked out on two
// machines). A partition name is a cookie jar: two hosts silently sharing one
// is exactly the isolation failure persistentPartition refuses. So a remote
// key is scoped by host before partition naming. Hashed rather than
// concatenated: a URL can never pass PERSISTENT_PROFILE_KEY_PATTERN, and the
// result must stay within the 64-char rule for any host URL and key length.
// Local ("local"/undefined) stays unscoped so existing on-disk partitions keep
// their logins.
export function scopedProfileKey(
	host: string | undefined,
	profileKey: string | undefined,
): string | undefined {
	if (!profileKey?.trim()) return profileKey;
	if (!host || host === "local") return profileKey;
	return `r${createHash("sha256").update(`${host}\n${profileKey}`).digest("hex").slice(0, 40)}`;
}
```

- [ ] **Step 4: Run the suite**

Run: `cd frontend && npx vitest run --config vite.renderer.config.ts src/main/browser-view-host.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/main/browser-view-host.ts frontend/src/main/browser-view-host.test.ts
git commit -m "feat(desktop): host-scoped persistent browser profile keys"
```

---

### Task 5: Per-host runtime links owned by RemoteRegistry, shared executor in main

**Files:**
- Modify: `frontend/src/main/remote-registry.ts`
- Modify: `frontend/src/main.ts` (`establishBrowserRuntimeLink` ~line 1001-1055, `const registry = new RemoteRegistry(...)` ~line 1947)
- Test: `frontend/src/main/remote-registry.test.ts`

**Interfaces:**
- Consumes: `connectBrowserRuntime(null, { dial, backoffMaxMs, execute, log })` + `upgradeDial(base)` from Task 3; `scopedProfileKey(host, key)` from Task 4; `BrowserRuntimeLinkHandle` (existing: `{ connected; dispose() }`).
- Produces: `RemoteRegistry` constructor signature `new RemoteRegistry(start: StartProxy, startRuntime?: (entry: RemoteEntry, proxy: ActiveProxy) => BrowserRuntimeLinkHandle)`; a runtime link starts on `connect` and is disposed on `disconnect`/`closeAll`. `main.ts` exports nothing new — behavior only.

- [ ] **Step 1: Write the failing registry tests**

In `remote-registry.test.ts` (mirror existing fakes for `StartProxy`):

```ts
it("starts one runtime link per connected host and disposes it on disconnect", async () => {
	const disposed: string[] = [];
	const started: string[] = [];
	const registry = new RemoteRegistry(fakeStartProxy, (entry, proxy) => {
		started.push(`${entry.url}|${proxy.base}`);
		return {
			connected: true,
			dispose: () => disposed.push(entry.url),
		};
	});
	await registry.connect(entryFor("http://h1:3001"));
	await registry.connect(entryFor("http://h1:3001")); // reuse — no second link
	expect(started).toHaveLength(1);

	await registry.disconnect("http://h1:3001");
	expect(disposed).toEqual(["http://h1:3001"]);
});

it("disposes every runtime link on closeAll", async () => {
	const disposed: string[] = [];
	const registry = new RemoteRegistry(fakeStartProxy, (entry) => ({
		connected: true,
		dispose: () => disposed.push(entry.url),
	}));
	await registry.connect(entryFor("http://h1:3001"));
	await registry.connect(entryFor("http://h2:3001"));
	await registry.closeAll();
	expect(disposed.sort()).toEqual(["http://h1:3001", "http://h2:3001"]);
});
```

- [ ] **Step 2: Run to verify failure**

Run: `cd frontend && npx vitest run --config vite.renderer.config.ts src/main/remote-registry.test.ts`
Expected: FAIL — constructor rejects the second argument / links never disposed.

- [ ] **Step 3: Implement the registry change**

`remote-registry.ts`:

```ts
import type { ActiveProxy } from "./remote-proxy";
import type { BrowserRuntimeLinkHandle } from "./browser-runtime-link";
import type { RemoteEntry } from "./remotes-store";

export type ConnectedHostView = {
	label: string;
	url: string;
	base: string;
};

type StartProxy = (entry: RemoteEntry) => Promise<ActiveProxy>;
type StartRuntimeLink = (entry: RemoteEntry, proxy: ActiveProxy) => BrowserRuntimeLinkHandle;

export class RemoteRegistry {
	private readonly live = new Map<
		string,
		{ view: ConnectedHostView; proxy: ActiveProxy; runtime?: BrowserRuntimeLinkHandle }
	>();

	constructor(
		private readonly start: StartProxy,
		// Attaches this app as the host's browser runtime so `ao browser` inside
		// that host's workers reaches the panel here. Optional so tests that only
		// care about proxies stay unchanged.
		private readonly startRuntime?: StartRuntimeLink,
	) {}

	async connect(entry: RemoteEntry): Promise<ConnectedHostView> {
		const existing = this.live.get(entry.url);
		if (existing) return existing.view;

		const proxy = await this.start(entry);
		const view = { label: entry.label, url: entry.url, base: proxy.base };
		const runtime = this.startRuntime?.(entry, proxy);
		this.live.set(entry.url, { view, proxy, runtime });
		return view;
	}

	async disconnect(url: string): Promise<void> {
		const entry = this.live.get(url);
		if (!entry) return;
		this.live.delete(url);
		entry.runtime?.dispose(); // stop reconnect attempts before the proxy dies
		await entry.proxy.close();
	}

	views(): ConnectedHostView[] {
		return [...this.live.values()].map(({ view }) => view);
	}

	async closeAll(): Promise<void> {
		const entries = [...this.live.values()];
		this.live.clear();
		for (const { runtime } of entries) runtime?.dispose();
		await Promise.all(entries.map(({ proxy }) => proxy.close()));
	}
}
```

- [ ] **Step 4: Run the registry tests**

Run: `cd frontend && npx vitest run --config vite.renderer.config.ts src/main/remote-registry.test.ts`
Expected: PASS (old tests too — the second constructor arg is optional).

- [ ] **Step 5: Wire main.ts**

1. Extend the imports from `./main/browser-runtime-link` (~line 152) with `upgradeDial` and the command type if not already imported, and import `scopedProfileKey` from `./main/browser-view-host`.

2. Extract the shared executor. In `establishBrowserRuntimeLink` (~line 1036) the local link currently holds an inline `execute` closure — replace it with a call to a new module-level function, defined near `establishBrowserRuntimeLink`:

```ts
function executeBrowserRuntimeCommand(
	hostScope: string | undefined,
	command: BrowserRuntimeCommand,
	signal: AbortSignal,
): Promise<unknown> {
	const host = browserViewHost;
	if (!host) {
		throw Object.assign(new Error("Browser target owner is unavailable"), {
			code: "BROWSER_TARGET_UNAVAILABLE",
		});
	}
	return host.execute(
		command.sessionId,
		command.action,
		command.args,
		signal,
		scopedProfileKey(hostScope, command.profileKey),
	);
}
```

(`BrowserRuntimeCommand` is exported from `browser-runtime-link.ts`.) The local link becomes:

```ts
	browserRuntimeLink = connectBrowserRuntime(address, {
		token,
		execute: (command, signal) => executeBrowserRuntimeCommand(undefined, command, signal),
		log: (message) => console.log(`AO: ${message}`),
	});
```

3. Registry construction (~line 1947):

```ts
const registry = new RemoteRegistry(startRemoteProxy, (entry, proxy) =>
	connectBrowserRuntime(null, {
		dial: upgradeDial(proxy.base),
		// A remote conn is rejected while the host's own desktop app is
		// connected (local wins); back off slowly so two apps do not saw at
		// each other every two seconds.
		backoffMaxMs: 15_000,
		execute: (command, signal) => executeBrowserRuntimeCommand(entry.url, command, signal),
		log: (message) => console.log(`AO: [${entry.label}] ${message}`),
	}),
);
```

4. **Verify the invariant:** grep the renderer for where `registerHostBase` is called (in `frontend/src/renderer`, likely where `remotes:connected`/`remotes:connect` results are consumed) and confirm the `HostId` it registers is the remote's `url` (the same `entry.url` string used above). If it registers something else (label or proxy base), use *that exact value* as `hostScope` here instead, and record which one won in the commit message. The partition invariant in Global Constraints depends on it.

- [ ] **Step 6: Typecheck and test**

Run: `cd frontend && npm run typecheck && npx vitest run --config vite.renderer.config.ts src/main/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/main/remote-registry.ts frontend/src/main/remote-registry.test.ts frontend/src/main.ts
git commit -m "feat(desktop): attach as browser runtime for every connected remote host"
```

---

### Task 6: Renderer passes the session's host into `browser:ensure`

**Files:**
- Modify: `frontend/src/main/browser-view-host.ts` (`browser:ensure` handler, ~line 1136)
- Modify: `frontend/src/preload.ts` (`browser.ensure`, ~line 276)
- Modify: `frontend/src/renderer/hooks/useBrowserView.ts` (options + the `ensure` call, ~lines 197-206 and 397-401)
- Modify: `frontend/src/renderer/components/SessionView.tsx` (the `useBrowserView(...)` call site)
- Test: `frontend/src/main/browser-view-host.test.ts`, `frontend/src/renderer/hooks/useBrowserView.test.tsx`

**Interfaces:**
- Consumes: `scopedProfileKey` from Task 4; renderer `HostId` (`sessionRef.host`, `"local"` for the local daemon).
- Produces: IPC `browser:ensure(sessionId: string, profileKey?: string, host?: string)`; preload `window.ao.browser.ensure(sessionId, profileKey?, host?)`; `useBrowserView` options gain `host?: string`. A human opening the Browser panel for a remote session now lands on the same scoped partition the agent's commands use.

- [ ] **Step 1: Write the failing tests**

1. `browser-view-host.test.ts` — find the existing `browser:ensure` handler test and add:

```ts
it("scopes the persistent partition by host on ensure", async () => {
	// invoke the registered browser:ensure handler the way existing tests do
	const local = await invokeEnsure(host, "s1", "proj", undefined);
	const remote = await invokeEnsure(host, "s2", "proj", "http://10.0.0.5:3001");
	expect(partitionOf(local)).toBe("persist:ao-browser-proj");
	expect(partitionOf(remote)).toMatch(/^persist:ao-browser-r[0-9a-f]{40}$/);
});
```

(`invokeEnsure`/`partitionOf` = whatever access pattern the existing ensure tests use — reuse it; the assertion is on the partition string derived from the third argument.)

2. `useBrowserView.test.tsx` — extend the ensure-call assertions: when the hook receives `host: "http://10.0.0.5:3001"`, `window.ao.browser.ensure` is called with `(sessionId, profileKey, "http://10.0.0.5:3001")`; with `host: "local"` (or omitted) it is called with `(sessionId, profileKey, "local")` / `(sessionId, undefined, "local")` matching the hook's argument shape below.

- [ ] **Step 2: Run to verify failure**

Run: `cd frontend && npx vitest run --config vite.renderer.config.ts src/main/browser-view-host.test.ts src/renderer/hooks/useBrowserView.test.tsx`
Expected: FAIL — extra argument not forwarded.

- [ ] **Step 3: Implement**

1. `browser-view-host.ts` handler (~line 1136):

```ts
	handle("browser:ensure", (event, sessionId: string, profileKey?: string, host?: string) => {
		const session = ensureSession(sessionId, event.sender.id, scopedProfileKey(host, profileKey));
```

(The daemon-link `execute` path at ~line 1251 stays raw — main.ts already scoped its key in Task 5. Do **not** scope twice: `ensureSession` receives an already-scoped key from both entry points.)

2. `preload.ts`:

```ts
			ensure: (sessionId: string, profileKey?: string, host?: string) =>
				ipcRenderer.invoke("browser:ensure", sessionId, profileKey, host) as Promise<BrowserNavState>,
```

(Match the existing return-type cast used at ~line 276.)

3. `useBrowserView.ts` — add `host?: string` to the hook's options type (next to `persistentProfile`, ~line 39/197) and change the ensure call (~line 397):

```ts
    const ensured = !profileKeyResolved
      ? undefined
      : profileKey
        ? window.ao?.browser.ensure(sessionId, profileKey, host)
        : window.ao?.browser.ensure(sessionId, undefined, host);
```

Update the comment above it: the third argument is the session's host so a remote project's persistent profile lands on a host-scoped partition; `"local"`/`undefined` is a no-op in main.

4. `SessionView.tsx` — locate the `useBrowserView({ ... persistentProfile ... })` call and add `host: sessionRef.host`. Then grep for **every other** `useBrowserView(` call site in the renderer; any that can only ever show local sessions may omit `host` (undefined = local); any that renders per-session views must pass its session's `host`.

5. Update the `window.ao` bridge type declaration if it is written out separately (follow the typecheck errors — the preload types may flow automatically).

- [ ] **Step 4: Run the tests + typecheck**

Run: `cd frontend && npm run typecheck && npx vitest run --config vite.renderer.config.ts src/main/browser-view-host.test.ts src/renderer/hooks/useBrowserView.test.tsx`
Expected: PASS.

- [ ] **Step 5: Run the full frontend suite**

Run: `cd frontend && npm test`
Expected: PASS, modulo the known pre-existing failures on clean `develop` (landing suites; the `@T0` chat-message-overflow smoke flake). Compare against a clean-`develop` baseline run if anything unexpected fails — do not chase failures this change cannot cause, and do not claim them fixed.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/main/browser-view-host.ts frontend/src/preload.ts frontend/src/renderer/hooks/useBrowserView.ts frontend/src/renderer/components/SessionView.tsx frontend/src/main/browser-view-host.test.ts frontend/src/renderer/hooks/useBrowserView.test.tsx
git commit -m "feat(desktop): bind remote sessions' browser panels to host-scoped partitions"
```

---

### Task 7: Docs — skill guide and CLI help text

**Files:**
- Modify: `backend/internal/skillassets/using-ao/commands/browser.md`
- Modify: `backend/internal/cli/browser.go` (Long text, ~line 53)
- Test: `backend/internal/cli/browser_test.go` (only if an existing test asserts the Long text verbatim)

**Interfaces:**
- Consumes: nothing from other tasks (text only).
- Produces: user/agent-facing docs describing remote support.

- [ ] **Step 1: Update the skill guide**

In `skillassets/using-ao/commands/browser.md`, in the opening paragraph after "The desktop app must be open.", add:

```markdown
Sessions on a remote host are supported: the desktop app attaches as the
browser runtime for every host it is connected to, so `ao browser` inside a
remote worker drives the Browser panel in the user's app. If the host's own
machine also runs the desktop app, that local app keeps the runtime and remote
commands wait until it disconnects. `BROWSER_RUNTIME_UNAVAILABLE` on a remote
session means no desktop app is currently attached to this host.
```

- [ ] **Step 2: Update the CLI Long text**

`backend/internal/cli/browser.go` (~line 53):

```go
		Long: "Inspect and control the target-isolated browser owned by the current AO session.\n\n" +
			"The desktop app must be open — for sessions on a remote host, open and attached\n" +
			"to that host. Commands operate the same live page the user sees, including while\n" +
			"the Browser panel is hidden.",
```

- [ ] **Step 3: Run backend tests**

Run: `cd backend && go test ./internal/cli/ ./internal/skillassets/...`
Expected: PASS (fix any snapshot test that asserts the old Long text). Note the memory caveat: `internal/cli` tests need a clean env — clear `AO_*` variables if failures look ambient.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/skillassets/using-ao/commands/browser.md backend/internal/cli/browser.go
git commit -m "docs(browser): document remote-session support in skill guide and CLI help"
```

---

### Task 8: Full verification + manual end-to-end check

**Files:** none created; verification only.

- [ ] **Step 1: Full automated pass**

```bash
cd backend && go test ./... && cd ..
npm run lint
cd frontend && npm run typecheck && npm test
```

Expected: green, modulo the documented pre-existing failures on clean `develop` (`internal/cli` ambient-env failures if `AO_*` is set; frontend landing suites; chat-overflow smoke flake). Anything else: fix before proceeding.

- [ ] **Step 2: Manual end-to-end (single machine, two daemons)**

This simulates a remote host with a second daemon in an isolated data dir. Adapt paths/ports to what `ao remote --help` and `scripts/daemon-build.sh` actually produce — do not guess flags; read the help output first.

1. Build: `scripts/daemon-build.sh` (per the repo's install-from-source flow) and launch the desktop app from this checkout (see the `ao-desktop-dev` skill).
2. Start a second "remote" daemon: `AO_DATA_DIR=/tmp/ao-remote-e2e <built-ao> start` in headless/persistent mode (`AO_KEEP_DAEMON=1` per the app-owned-daemon rules), enable its LAN listener + password the same way a real remote is set up (`ao doctor` / the mobile/LAN enable flow — read `using-ao` commands docs).
3. In the app, add + connect that daemon as a remote host (Hosts UI or `ao remote add`).
4. Create a project + spawn a worker **on the remote host**, then inside that worker session run:
   - `ao browser status` → `connected`
   - `ao browser open https://example.com` → the Browser panel for that session in the desktop app shows example.com
   - `ao browser snapshot --interactive` → returns refs
   - `ao browser screenshot /tmp/remote-e2e.png` → PNG written inside the worker's host filesystem
5. Persistent-profile check: opt the remote project in (`ao project set-config --browser-persistent-profile` on the remote), re-run a command, and verify in the app's `userData` (`~/.ao/electron/Partitions/`) that the partition directory is `ao-browser-r<40 hex>`, not the raw project id.
6. Local-wins check (optional if a second machine is unavailable): with the app attached to its **own** daemon's LAN listener as a "remote", confirm the remote link logs the rejection/backoff and the local panel keeps working.
7. Disconnect the remote host in the app → inside the remote worker `ao browser status` reports disconnected and commands return `BROWSER_RUNTIME_UNAVAILABLE`.

- [ ] **Step 3: Record results**

Note pass/fail per check in the PR description (with the screenshot from 4 as evidence). Then follow `superpowers:finishing-a-development-branch`.

---

## Self-review (performed while writing)

- **Spec coverage:** the request — "`ao browser` works with remote sessions" — is covered end-to-end: daemon accepts a remote runtime (Tasks 1-2), the app provides one per connected host (Tasks 3, 5), profile isolation across hosts holds on both the agent path (Task 5) and the human-panel path (Tasks 4, 6), docs updated (Task 7), verified (Task 8). CLI needs no functional change — established in research: the worker CLI already reaches its own daemon's loopback API on any host.
- **Placeholder scan:** every code step contains the actual code; the two "mirror existing helpers" steps (Tasks 1-2 test harnesses, Task 6 ensure-test access pattern) name the exact file whose pattern to copy and pin the assertions, which is deliberate — inventing helper names the file may already have would collide.
- **Type consistency:** `ServeRemoteConn(ctx, net.Conn)` (Task 1) = handler call (Task 2) = `BrowserRuntimeBridge` interface (Task 2). `upgradeDial(base) → BrowserRuntimeDial` and `connectBrowserRuntime(null, {dial, backoffMaxMs, execute, log})` (Task 3) = registry factory usage (Task 5). `scopedProfileKey(host, key)` (Task 4) = usage in Tasks 5 and 6. `browser:ensure(sessionId, profileKey?, host?)` consistent across handler/preload/hook (Task 6).
