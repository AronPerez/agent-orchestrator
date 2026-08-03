package httpd

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

// authState holds the current password hash for the LAN listener. Swapped
// atomically on regenerate so an in-flight request never sees a torn value.
type authState struct{ hash atomic.Pointer[string] }

func (a *authState) setHash(h string) { a.hash.Store(&h) }
func (a *authState) currentHash() string {
	if p := a.hash.Load(); p != nil {
		return *p
	}
	return ""
}

// lockout throttles password guessing per source address.
type lockout struct {
	mu       sync.Mutex
	limit    int
	cooldown time.Duration
	now      func() time.Time
	fails    map[string]int
	until    map[string]time.Time
}

func newLockout(limit int, cooldown time.Duration, now func() time.Time) *lockout {
	return &lockout{limit: limit, cooldown: cooldown, now: now, fails: map[string]int{}, until: map[string]time.Time{}}
}

func (l *lockout) blocked(src string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.until[src]
	if !ok {
		return false
	}
	if l.now().Before(t) {
		return true
	}
	// Cooldown elapsed: clear the lockout AND the fail counter so the source
	// starts a fresh window. Without this the counter stays at the limit and the
	// very next failure would immediately re-lock for another full cooldown —
	// and a client that keeps polling would stay locked out forever. This also
	// bounds map growth, since expired entries are pruned on the next request.
	delete(l.until, src)
	delete(l.fails, src)
	return false
}

// fail records a failed password guess and reports whether THIS call tripped
// the lockout (exactly at the limit), so the caller can log the trip once
// rather than on every subsequent failure.
func (l *lockout) fail(src string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[src]++
	if l.fails[src] >= l.limit {
		l.until[src] = l.now().Add(l.cooldown)
		return l.fails[src] == l.limit
	}
	return false
}

func (l *lockout) reset(src string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, src)
	delete(l.until, src)
}

func sourceKey(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// wsProtocolPrefix marks the Sec-WebSocket-Protocol entry carrying the
// connection token. Browsers cannot set headers on new WebSocket(), so the web
// client requests ["ao.auth", "ao.bearer.<pw>"] and the daemon echoes the
// "ao.auth" marker (see muxAuthSubprotocol in terminal_mux.go). Safe as an
// auth channel: the Fetch spec forbids Sec-* headers on fetch/XHR, so only a
// real WebSocket handshake can carry it, and the value is still verified
// against the password hash exactly like a Bearer token.
const wsProtocolPrefix = "ao.bearer."

func wsProtocolToken(r *http.Request) string {
	for _, v := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); strings.HasPrefix(p, wsProtocolPrefix) {
				return strings.TrimPrefix(p, wsProtocolPrefix)
			}
		}
	}
	return ""
}

// authCookieName carries the connection token for a preview page's in-page
// subresource requests. See connectionToken / maybeSetPreviewAuthCookie.
const authCookieName = "ao_conn"

// previewFilesMarker is the path segment that identifies a preview-file request
// (GET /api/v1/sessions/{id}/preview/files/*). The auth cookie is both scoped to
// and honored only on this path, so it can never authenticate any other endpoint.
const previewFilesMarker = "/preview/files/"

// previewFilesCookiePath returns the cookie Path to scope the auth cookie to the
// requesting session's preview files (".../preview/files/"), or "" if the request
// is not a preview-file request. Scoping this tightly is what keeps the cookie
// from ever reaching /kill, /send, another session, or any non-preview route.
func previewFilesCookiePath(urlPath string) string {
	i := strings.Index(urlPath, previewFilesMarker)
	if i < 0 {
		return ""
	}
	return urlPath[:i+len(previewFilesMarker)]
}

// connectionToken returns the caller's connection token. It comes from the
// Authorization: Bearer header (the mobile API client and a preview page's
// top-level navigation), the ao.bearer.* Sec-WebSocket-Protocol entry (a
// browser's /mux handshake, which cannot set Authorization) or, ONLY on the
// preview-files route, the auth cookie (a preview page's subresource requests —
// images/CSS/JS — which the WebView issues without our header). Restricting the
// cookie to the preview-files path means it can never authenticate any other
// mobile endpoint even if a client sends it.
func connectionToken(r *http.Request) string {
	if t := bearerToken(r); t != "" {
		return t
	}
	if t := wsProtocolToken(r); t != "" {
		return t
	}
	if previewFilesCookiePath(r.URL.Path) != "" {
		if c, err := r.Cookie(authCookieName); err == nil {
			return c.Value
		}
	}
	return ""
}

// maybeSetPreviewAuthCookie drops the auth cookie when a preview FILE is fetched
// with a valid token, so the WebView's follow-up subresource requests on the same
// password-protected preview route authenticate too (they never carry our
// Authorization header). The cookie is Path-scoped to this session's preview
// files only, HttpOnly, and re-sent only when it doesn't already match the token
// that just authenticated — so a normal subresource costs no Set-Cookie, but a
// cookie left over from a regenerated password is overwritten instead of being
// kept until it 401s every image/CSS/JS on the page. This runs on the LAN
// listener only; the loopback/desktop preview path never reaches authMiddleware,
// so desktop preview behavior is unchanged.
func maybeSetPreviewAuthCookie(w http.ResponseWriter, r *http.Request, tok string) {
	path := previewFilesCookiePath(r.URL.Path)
	if path == "" {
		return
	}
	if c, err := r.Cookie(authCookieName); err == nil && c.Value == tok {
		return // already current; don't re-send Set-Cookie on every subresource
	}
	//nolint:gosec // Secure is intentionally omitted: the LAN bridge is plaintext
	// http by design (ADR 0001, home-network-only), and a Secure cookie would never
	// be sent over it. The token already travels the same plain link via Bearer.
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    tok,
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// No Secure: the LAN link is plain http (a TLS tunnel still sends it),
		// matching how the Bearer token already travels.
	})
}

// isCORSPreflight reports whether r is a CORS preflight: an OPTIONS request
// bearing both an Origin and the Access-Control-Request-Method header. This is
// the same shape corsMiddleware answers itself (see cors.go), so anything
// matching here terminates there and never reaches a route handler.
func isCORSPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions &&
		r.Header.Get("Origin") != "" &&
		r.Header.Get("Access-Control-Request-Method") != ""
}

func authMiddleware(state *authState, lock *lockout, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// A CORS preflight can never be authenticated: browsers strip
			// credentials from it, so it arrives with no Authorization header by
			// design. Rejecting it 401s the preflight, the browser reports an
			// opaque "CORS error", and the real request is never sent — so every
			// cross-origin browser client (the Expo web build reaching the LAN
			// listener from another machine) is locked out regardless of password.
			// Worse, counting it as a failed attempt means `limit` preflights trip
			// the per-source lockout for a client holding the CORRECT password.
			//
			// Pass it through to corsMiddleware, which answers it with 204 and no
			// body, or 403 when the Origin is not allowlisted. Requiring BOTH
			// preflight headers is what keeps this from becoming an auth bypass:
			// corsMiddleware handles every request matching this shape itself and
			// never calls a route handler, so no side effect can run unauthenticated.
			if isCORSPreflight(r) {
				next.ServeHTTP(w, r)
				return
			}
			src := sourceKey(r)
			if lock.blocked(src) {
				envelope.WriteAPIError(w, r, http.StatusTooManyRequests, "too_many_requests", "LOCKED_OUT",
					"too many failed attempts; try again shortly", nil)
				return
			}
			tok := connectionToken(r)
			if mobilebridge.PasswordMatches(state.currentHash(), tok) {
				lock.reset(src)
				maybeSetPreviewAuthCookie(w, r, tok)
				next.ServeHTTP(w, r)
				return
			}
			// A tokenless request guesses nothing: 401 it, but don't let it consume
			// lockout budget — the lockout throttles password guessing, and counting
			// headerless traffic (an old web build's /mux retry loop, a stray probe)
			// would 429 every request from that IP, including authenticated REST.
			// The trip Warn is the only trace a lockout leaves: auth runs outside
			// requestLogger, so these 401/429s never reach the access log.
			if tok != "" && lock.fail(src) {
				log.Warn("LAN auth lockout tripped", "src", src)
			}
			envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "BAD_PASSWORD",
				"missing or invalid connection password", nil)
		})
	}
}
