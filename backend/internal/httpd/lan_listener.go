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

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/webui"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/preview"
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
		handler:     lanControlBlock(webUIBypass(handler, webui.Handler(http.HandlerFunc(notFoundJSON)), authed, log)),
		defaultPort: defaultPort,
		log:         log,
		state:       state,
	}
}

// routeMatcher is chi.Router's route-matching half. Declared as an interface so
// the bypass can ask "does the daemon actually serve this?" without the LAN
// manager taking a hard dependency on the concrete router type.
type routeMatcher interface {
	Match(rctx *chi.Context, method, path string) bool
}

// webUIBypass serves the embedded web UI without a connection password and
// sends everything else through authMiddleware.
//
// The UI *is* the password prompt: a browser on another machine has no way to
// send a credential it has not been asked for yet, so 401-ing index.html and
// its assets would leave the user staring at a JSON error with nowhere to type.
// What it exposes is a static bundle — the same bytes every AO release ships —
// and never a byte of daemon data, which is what makes the SPA see a 401 and
// show the prompt.
//
// It fails CLOSED, and that is the load-bearing part. webui.IsUIRequest is a
// deny-list of the paths the daemon owns today, and a deny-list fails open: the
// day someone registers a top-level route (say GET /metrics) without adding it
// there, that route stops being excluded and the bypass hands the request
// straight to the router — which runs the handler, unauthenticated, on a socket
// bound to the network. Not a shadowed route: a live one, answering strangers.
// The failure would be silent and remote-only, so local dev would never show it.
//
// So a request is only treated as UI when the router has NO handler for it. A
// registered route always goes through authMiddleware, whether or not anyone
// remembered to list its prefix, and the deny-list becomes defense-in-depth
// rather than the sole guard.
//
// A handler that cannot report its routes — anything but the chi router, e.g.
// if someone later wraps it in middleware before it reaches here — disables the
// bypass entirely rather than skipping the check. "Cannot prove this is not a
// daemon route" is exactly when to charge for the password; the visible cost is
// that the LAN UI stops loading, which is a bug someone reports, not a silent
// unauthenticated route on a socket bound to the network.
//
// It serves ui directly and never calls the shared router. Routing an
// unauthenticated request into the router and expecting it to land in NotFound
// was a real hole: the router runs its own middleware stack first, and
// previewOriginMiddleware terminates any GET whose Host is a preview subdomain
// by serving files out of that session's workspace — session data, keyed on a
// base32 session id in the Host, which is not a secret. Measured before this
// change: GET / with Host ao-preview.<b32>.localhost reached PreviewOrigin on
// the LAN listener with no password. Serving the bundle directly means no
// router middleware can ever run on the unauthenticated path, which closes the
// whole class rather than that one instance.
func webUIBypass(router, ui, authed http.Handler, log *slog.Logger) http.Handler {
	matcher, isRouter := router.(routeMatcher)
	if !isRouter {
		log.Warn("web UI bypass disabled: the LAN handler cannot report its routes, so every LAN request will require the connection password, including the UI shell",
			"handler", fmt.Sprintf("%T", router))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isRouter || !webui.IsUIRequest(r) {
			authed.ServeHTTP(w, r)
			return
		}
		// A preview-origin Host belongs to the workspace preview flow, not the
		// UI. Sending it to authMiddleware keeps that flow working over the LAN
		// exactly as it did before the bypass existed — with the password —
		// rather than silently answering it with the app shell.
		if _, isPreview := preview.SessionIDFromHost(r.Host); isPreview {
			authed.ServeHTTP(w, r)
			return
		}
		// A path the daemon serves is the daemon's, however it is spelled. chi
		// matches per method, so a HEAD to a GET-only route falls through to the
		// UI shell — harmless, since no daemon handler runs either way.
		if matcher.Match(chi.NewRouteContext(), r.Method, r.URL.Path) {
			authed.ServeHTTP(w, r)
			return
		}
		ui.ServeHTTP(w, r)
	})
}

// lanControlBlockedPrefixes are the loopback-only daemon-control route
// prefixes that must never be reachable through the LAN listener: /shutdown,
// the telemetry routes under /internal/, and the Connect Mobile control
// surface under /api/v1/mobile, developer maintenance routes under /api/v1/dev,
// and host-mutating installer routes under /api/v1/system/install. Some routes
// are gated in the shared router by localControlRequest,
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
	"/api/v1/desktop",
	"/api/v1/system/install",
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

// loopbackOnlyJSON answers a request for a route this daemon serves, but not on
// this listener. It is deliberately NOT ROUTE_NOT_FOUND: that code reads as
// "this daemon is too old / that endpoint does not exist", so an operator who
// hits a policy block goes and audits daemon builds and finds nothing wrong,
// because nothing is wrong. This says the block is a decision.
//
// What it does not do is resolve the route. Every path under a blocked prefix
// gets this same answer whether or not a handler exists behind it, so the
// response is a compile-time constant of the AO build and never an oracle for
// which loopback-only routes this particular daemon has. That is also why it is
// safe to serve before authMiddleware: see the note on lanControlBlock.
//
// The status stays 404. The route genuinely is not mounted on this listener —
// the LAN handler chain is a different one — and holding the status still keeps
// every existing client's error handling working; the code and message are
// what carry the diagnosis.
func loopbackOnlyJSON(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ROUTE_LOOPBACK_ONLY",
		r.Method+" "+r.URL.Path+" is served on this daemon's loopback listener only and is deliberately "+
			"not exposed on the network listener — run the command on that machine, or over loopback", nil)
}

// lanControlBlock returns 404 for any request whose path is, or is nested
// under, a loopback-only control-route prefix, before it ever reaches auth or
// the shared router. It also stamps the LAN-listener marker every layer below
// reads.
//
// It answers before authMiddleware, and stays there. Being outermost is the
// load-bearing property: no code below — not auth, not the router's middleware
// stack — can run for a blocked path, whatever headers the caller forged. A
// message that only an authenticated caller could see would mean moving this
// inside auth, or evaluating the credential twice; the first trades a
// structural guarantee for wording, and neither buys anything, because what an
// unauthenticated caller learns here is a constant of the AO build (the prefix
// list, which is in the source) and not a fact about this machine. It still
// never confirms that a specific route exists — see loopbackOnlyJSON.
func lanControlBlock(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLANControlBlockedPath(r.URL.Path) {
			loopbackOnlyJSON(w, r)
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

// IsLANControlBlockedPathForTest exposes the LAN block check to package-external
// tests so route-level invariants can be asserted without a live listener.
func IsLANControlBlockedPathForTest(path string) bool { return isLANControlBlockedPath(path) }

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
