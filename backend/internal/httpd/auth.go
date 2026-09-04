package httpd

import (
	"encoding/json"
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

// authCookieName carries the connection token on requests a browser cannot put
// a header on. It is minted two ways: by handleLogin at Path=/ (a session
// credential for a daemon-served page), and by maybeSetPreviewAuthCookie scoped
// to one session's preview files. See credential for how both are honored.
const authCookieName = "ao_conn"

// previewFilesMarker is the path segment that identifies a preview-file request
// (GET /api/v1/sessions/{id}/preview/files/*).
const previewFilesMarker = "/preview/files/"

// previewFilesCookiePath returns the cookie Path to scope the auth cookie to the
// requesting session's preview files (".../preview/files/"), or "" if the request
// is not a preview-file request. Preview output is workspace-authored content, so
// the cookie IT gets stays pinned to its own path — it never widens into the
// Path=/ session cookie handleLogin issues to a page the daemon served itself.
func previewFilesCookiePath(urlPath string) string {
	i := strings.Index(urlPath, previewFilesMarker)
	if i < 0 {
		return ""
	}
	return urlPath[:i+len(previewFilesMarker)]
}

// authLoginPath exchanges the connection password for a session cookie. It
// exists on the authenticated (LAN) listener only, where there is a password to
// exchange; on the no-auth loopback listener it is simply not a route.
const authLoginPath = "/api/v1/auth/login"

// credential returns the token authenticating r against hash, whether it
// arrived in a cookie, and whether it is valid.
//
// It comes from the Authorization: Bearer header (the mobile API client and a
// preview page's top-level navigation), the ao.bearer.* Sec-WebSocket-Protocol
// entry (a browser's /mux handshake, which cannot set Authorization), or the
// ao_conn cookie — the only channel available to a browser API that lets it set
// neither, notably EventSource/SSE, <img>, and top-level navigation.
//
// Every ao_conn cookie is tried, not just the first: the login route mints one
// at Path=/ while a preview page may separately hold one scoped to its own
// preview-files path (see maybeSetPreviewAuthCookie), and the browser sends
// both. Picking the first would fail auth roughly half the time.
//
// A cookie authenticating on its own is deliberately narrower than it looks —
// authMiddleware additionally requires a strict origin whenever the cookie is
// what authenticated, because cookies ride cross-site and a Bearer never does.
func credential(hash string, r *http.Request) (tok string, viaCookie, ok bool) {
	if t := bearerToken(r); t != "" {
		return t, false, mobilebridge.PasswordMatches(hash, t)
	}
	if t := wsProtocolToken(r); t != "" {
		return t, false, mobilebridge.PasswordMatches(hash, t)
	}
	var seen string
	for _, c := range r.Cookies() {
		if c.Name != authCookieName {
			continue
		}
		if mobilebridge.PasswordMatches(hash, c.Value) {
			return c.Value, true, true
		}
		seen = c.Value
	}
	return seen, seen != "", false
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

// handleLogin serves POST /api/v1/auth/login: it exchanges the connection
// password for the ao_conn session cookie.
//
//	POST /api/v1/auth/login {"password":"<connection password>"}
//	  204 No Content + Set-Cookie: ao_conn=<token>; HttpOnly; SameSite=Strict; Path=/
//	  401 on a wrong password, counted by the same per-source lockout
//
// It exists because a browser cannot put an Authorization header on everything:
// EventSource (SSE) in particular sends cookies and nothing else. The cookie
// value is the password itself, so it validates through exactly the same hash
// comparison as a Bearer — there is no second credential system to keep in sync
// or to revoke separately: rotating the password invalidates every cookie.
//
// Minting the cookie is gated on a strict origin. A cookie is the one
// credential a browser will attach to requests a hostile page initiates, so
// handing one out to a cross-origin caller would be handing out CSRF. That is
// checked here, and again on every request the cookie authenticates.
func handleLogin(w http.ResponseWriter, r *http.Request, state *authState, lock *lockout, log *slog.Logger, connected *mobileConnectReporter) {
	if r.Method != http.MethodPost {
		methodNotAllowedJSON(w, r)
		return
	}
	if !strictOriginOK(nil, r) {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "ORIGIN_FORBIDDEN",
			"login is accepted only from this daemon's own pages", nil)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	// 4 KiB is orders of magnitude more than an 8-char password needs; the cap
	// is only there so an unauthenticated route cannot be used to buffer a
	// large body.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON",
			"request body must be JSON of the form {\"password\":\"...\"}", nil)
		return
	}
	src := sourceKey(r)
	if body.Password == "" || !mobilebridge.PasswordMatches(state.currentHash(), body.Password) {
		if body.Password != "" && lock.fail(src) {
			log.Warn("LAN auth lockout tripped", "src", src)
		}
		envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "BAD_PASSWORD",
			"missing or invalid connection password", nil)
		return
	}
	lock.reset(src)
	connected.report(src)
	//nolint:gosec // Secure is intentionally omitted, exactly as for the preview
	// cookie: the LAN bridge is plaintext http by design (ADR 0001), and a Secure
	// cookie would never be sent over it. The password already travels the same
	// plain link as a Bearer.
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    body.Password,
		Path:     "/",
		HttpOnly: true,
		// Strict, not Lax: nothing about this daemon is meant to be reached by
		// following a link from another site, so there is no navigation case Lax
		// would buy — and Strict keeps the cookie off cross-site top-level
		// requests entirely.
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
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

// identityProbePath is the one route the LAN listener serves without the
// connection password. The phone races several endpoints, and a private
// address is not an identity: 192.168.1.42 exists on most networks. Verifying
// which machine answered has to happen BEFORE a credential is presented, or
// the phone leaks its token to whatever device holds that address on a foreign
// network. The response carries an opaque host id and nothing else.
//
// Exact path, GET only, and checked ahead of the lockout so a phone racing
// endpoints cannot lock itself out probing. See
// docs/adr/0003-unauthenticated-identity-probe.md.
const identityProbePath = "/api/v1/identity"

// isIdentityProbe reports whether r is the exempt probe. Deliberately an exact
// match rather than a prefix, so nothing nested below the path inherits the
// exemption.
func isIdentityProbe(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == identityProbePath
}

// authMiddleware authenticates LAN requests against the current connection
// password. connected, which may be nil, is notified of the source address of
// every request that authenticates; it exists so telemetry can observe that a
// phone actually reached this desktop, and it must not block the request, since
// it runs inline on every authenticated call.
func authMiddleware(state *authState, lock *lockout, log *slog.Logger, connected *mobileConnectReporter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isIdentityProbe(r) {
				next.ServeHTTP(w, r)
				return
			}
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
			if r.URL.Path == authLoginPath {
				handleLogin(w, r, state, lock, log, connected)
				return
			}
			tok, viaCookie, ok := credential(state.currentHash(), r)
			if ok {
				// A cookie rides along on cross-site requests; a Bearer or the
				// ao.bearer.* subprotocol can only be attached by code that was
				// handed the password. So when the cookie is what authenticated,
				// origin checking is not optional — without it any page on the
				// network could drive this daemon through the user's browser
				// (classic CSRF, and CSWSH on the /mux upgrade).
				if viaCookie && !strictOriginOK(nil, r) {
					envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "ORIGIN_FORBIDDEN",
						"cookie credentials are accepted only from this daemon's own pages", nil)
					return
				}
				lock.reset(src)
				connected.report(src)
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
