package httpd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/webui"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LANManager owns the daemon's second, network-facing HTTP listener. It binds
// only while Connect Mobile is enabled (0.0.0.0 by default; narrower with a
// bind mode — see Start) and wraps the shared router in authMiddleware. The
// loopback listener is unaffected.
type LANManager struct {
	handler     http.Handler // shared router, already auth-wrapped
	defaultPort int
	log         *slog.Logger
	state       *authState // shared with authMiddleware; SetPasswordHash writes through here

	mu    sync.Mutex
	srv   *http.Server
	ln    net.Listener
	bound int
}

// NewLANManager wraps handler in the LAN control-block and authMiddleware
// (backed by the shared state) and returns a manager that can start/stop the
// network-facing listener. Most callers want NewMobileLAN, which owns the state.
func NewLANManager(handler http.Handler, state *authState, defaultPort int, log *slog.Logger, sink ports.EventSink) *LANManager {
	log = loggerOrDefault(log)
	lock := newLockout(5, time.Minute, time.Now)
	authed := authMiddleware(state, lock, log, newMobileConnectReporter(sink, time.Now))(handler)
	return &LANManager{
		handler:     lanControlBlock(webUIBypass(handler, authed)),
		defaultPort: defaultPort,
		log:         log,
		state:       state,
	}
}

// webUIBypass serves the embedded web UI without a connection password and
// sends everything else through authMiddleware.
//
// The UI *is* the password prompt: a browser on another machine has no way to
// send a credential it has not been asked for yet, so 401-ing index.html and
// its assets would leave the user staring at a JSON error with nowhere to type.
// What is exposed by this is a static bundle — the same bytes every AO release
// ships — and never a byte of daemon data: webui.IsUIRequest excludes every
// path the daemon answers itself (API, /mux, probes, control), so the first
// request that could return anything about this machine is still authenticated,
// which is what makes the SPA see a 401 and show the prompt.
func webUIBypass(ui http.Handler, authed http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if webui.IsUIRequest(r) {
			ui.ServeHTTP(w, r)
			return
		}
		authed.ServeHTTP(w, r)
	})
}

// lanControlBlockedPrefixes are the loopback-only daemon-control route
// prefixes that must never be reachable through the LAN listener: /shutdown,
// the telemetry routes under /internal/, and the Connect Mobile control
// surface under /api/v1/mobile, plus developer maintenance routes under
// /api/v1/dev. Some routes are gated in the shared router by localControlRequest,
// which trusts the client-supplied Host header (and RealIP, which trusts
// X-Forwarded-For/X-Real-IP) — both spoofable by any LAN client. The LAN
// listener is the one thing a caller cannot spoof: it is the physical socket the
// request arrived on. So the block below is applied only to the LAN-served
// handler, outermost (wrapping authMiddleware), independent of any header.
var lanControlBlockedPrefixes = []string{
	"/shutdown",
	"/internal/",
	"/api/v1/mobile",
	"/api/v1/dev",
	"/api/v1/browser",
}

// lanListenerCtxKey marks a request as having arrived on the physical LAN
// socket. Unlike Host or X-Forwarded-For it cannot be spoofed by the client:
// only lanControlBlock sets it, and lanControlBlock wraps the LAN-served
// handler only.
type lanListenerCtxKey struct{}

// servedOverLAN reports whether r arrived on the LAN listener (and is therefore
// credential-gated by authMiddleware) rather than the no-auth loopback
// listener. Origin policy differs between the two — see requiresStrictOrigin.
func servedOverLAN(r *http.Request) bool {
	v, _ := r.Context().Value(lanListenerCtxKey{}).(bool)
	return v
}

// lanControlBlock returns 404 for any request whose path is, or is nested
// under, a loopback-only control-route prefix, before it ever reaches auth or
// the shared router. It answers as if the route were never mounted at all —
// no 403/401 that would confirm the path exists. It also stamps the
// LAN-listener marker every layer below reads.
func lanControlBlock(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLANControlBlockedPath(r.URL.Path) {
			notFoundJSON(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), lanListenerCtxKey{}, true)))
	})
}

// isLANControlBlockedPath reports whether path matches a blocked prefix on an
// exact segment boundary: "/api/v1/mobile" blocks itself and everything
// beneath it ("/api/v1/mobile/status") but must not catch unrelated siblings
// such as "/api/v1/mobileapp".
func isLANControlBlockedPath(path string) bool {
	if strings.HasPrefix(path, "/api/v1/sessions/") && strings.HasSuffix(strings.TrimSuffix(path, "/"), "/preview/server") {
		return true
	}
	for _, prefix := range lanControlBlockedPrefixes {
		trimmed := prefix
		if len(trimmed) > 1 && trimmed[len(trimmed)-1] == '/' {
			trimmed = trimmed[:len(trimmed)-1]
		}
		if path == trimmed || strings.HasPrefix(path, trimmed+"/") {
			return true
		}
	}
	return false
}

// NewMobileLAN constructs a LANManager with its own private authState. Callers
// outside this package (the daemon) cannot construct an authState directly
// since it is unexported; this gives them a LANManager that owns one, and the
// daemon rotates the connection password exclusively via SetPasswordHash.
func NewMobileLAN(handler http.Handler, defaultPort int, log *slog.Logger, sink ports.EventSink) *LANManager {
	return NewLANManager(handler, &authState{}, defaultPort, log, sink)
}

// SetPasswordHash stores the current connection password hash on the shared
// authState so the auth middleware (already wrapping handler) validates
// against it. Satisfies controllers.LANController.
func (m *LANManager) SetPasswordHash(hash string) {
	m.state.setHash(hash)
}

// PasswordHash returns the current connection password hash. Used to snapshot the
// prior hash before an enable/regenerate so a failed persist can be rolled back.
// Satisfies controllers.LANController.
func (m *LANManager) PasswordHash() string {
	return m.state.currentHash()
}

// Start binds the network-facing listener on <bind>:port (falling back to an
// ephemeral port if that port is in use) and serves the wrapped handler. bind
// is a mobilebridge bind mode — "" / "all" for every interface, "tailscale" for
// the Tailscale interface only (WireGuard-encrypted transport, no TLS work), or
// a literal IP. It is idempotent: a second call while running returns the
// already-bound port, bind included.
func (m *LANManager) Start(port int, bind string) (int, error) {
	host, err := mobilebridge.BindAddress(bind)
	if err != nil {
		return 0, fmt.Errorf("bind LAN: %w", err)
	}
	m.mu.Lock()
	if m.srv != nil {
		defer m.mu.Unlock()
		return m.bound, nil // idempotent
	}
	if port == 0 {
		port = m.defaultPort
	}
	//nolint:gosec // G102: binding all interfaces is the deliberate default of the Connect Mobile LAN listener; it runs only while the bridge is enabled and behind authMiddleware, and `bind` narrows it when the user asks.
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		if !isAddrInUse(err) {
			m.mu.Unlock()
			return 0, fmt.Errorf("bind LAN %s:%d: %w", host, port, err)
		}
		//nolint:gosec // G102: see above — same bind host, ephemeral port.
		if ln, err = net.Listen("tcp", net.JoinHostPort(host, "0")); err != nil {
			m.mu.Unlock()
			return 0, fmt.Errorf("bind LAN ephemeral: %w", err)
		}
		m.log.Warn("LAN port in use; bound ephemeral", "wanted", port, "bound", ln.Addr())
	}
	m.ln = ln
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		m.mu.Unlock()
		_ = ln.Close()
		return 0, fmt.Errorf("bind LAN: unexpected listener address type %T", ln.Addr())
	}
	m.bound = tcpAddr.Port
	m.srv = &http.Server{Handler: m.handler, ReadHeaderTimeout: 10 * time.Second}
	srv := m.srv
	boundPort := m.bound
	m.mu.Unlock()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.log.Error("LAN listener serve", "err", err)
		}
	}()
	m.log.Info("LAN listener started", "addr", ln.Addr())
	return boundPort, nil
}

// Stop gracefully shuts down the listener (honoring ctx) and clears the bound
// state. It is a no-op if the listener is not running.
func (m *LANManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	srv := m.srv
	m.srv, m.ln, m.bound = nil, nil, 0
	m.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// Running reports whether the LAN listener is currently serving.
func (m *LANManager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.srv != nil
}

// BoundPort returns the port the listener is bound to, or 0 when not running.
func (m *LANManager) BoundPort() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bound
}
