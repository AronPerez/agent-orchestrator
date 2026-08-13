package httpd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

func newAuthUnderTest(pw string, now func() time.Time) (http.Handler, *lockout) {
	st := &authState{}
	h := mobilebridge.HashPassword(pw)
	st.setHash(h)
	lock := newLockout(5, time.Minute, now)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	return authMiddleware(st, lock, discardLogger(), nil)(ok), lock
}

func req(auth string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	r.RemoteAddr = "192.168.1.50:5555"
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	return r
}

func reqFrom(remoteAddr, auth string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	r.RemoteAddr = remoteAddr
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	return r
}

func TestAuthLockoutResetsAfterCooldown(t *testing.T) {
	nowP := time.Now()
	h, _ := newAuthUnderTest("secret12", func() time.Time { return nowP })
	// Lock the source with 5 failures.
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req("Bearer wrong"))
	}
	// Still within cooldown → 429 even with the right password.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer secret12"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("during cooldown: got %d want 429", w.Code)
	}
	// Advance past the 1-minute cooldown.
	nowP = nowP.Add(time.Minute + time.Second)
	// A single WRONG attempt must NOT immediately re-lock — it starts a fresh
	// window and returns 401, not 429.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer wrong"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("first attempt after cooldown: got %d want 401 (fresh window, not re-locked)", w.Code)
	}
	// And the correct password now succeeds.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer secret12"))
	if w.Code != http.StatusOK {
		t.Fatalf("correct password after cooldown: got %d want 200", w.Code)
	}
}

func TestAuthRejectsMissingAndWrong(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	for _, tc := range []struct {
		name, auth string
		want       int
	}{
		{"missing", "", http.StatusUnauthorized},
		{"wrong", "Bearer nope", http.StatusUnauthorized},
		{"right", "Bearer secret12", http.StatusOK},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req(tc.auth))
		if w.Code != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, w.Code, tc.want)
		}
	}
}

func TestAuthLockoutAfterFive(t *testing.T) {
	now := time.Now()
	h, _ := newAuthUnderTest("secret12", func() time.Time { return now })
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req("Bearer wrong"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d want 401", i, w.Code)
		}
	}
	// 6th attempt — even with the RIGHT password — is locked out.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer secret12"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked attempt: got %d want 429", w.Code)
	}
}

// preflight builds the OPTIONS request a browser sends before a cross-origin
// request that carries an Authorization header. Browsers strip credentials from
// it, so it deliberately has no Authorization of its own.
func preflight() *http.Request {
	r := httptest.NewRequest(http.MethodOptions, "/api/v1/sessions", nil)
	r.RemoteAddr = "192.168.1.50:5555"
	r.Header.Set("Origin", "http://192.168.1.250:8081")
	r.Header.Set("Access-Control-Request-Method", "GET")
	r.Header.Set("Access-Control-Request-Headers", "authorization")
	return r
}

// A CORS preflight arrives without credentials by design. Rejecting it makes the
// browser report an opaque "CORS error" and never send the real request, so a
// cross-origin client can never connect no matter what password it holds.
func TestAuthPassesCORSPreflight(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, preflight())
	if w.Code != http.StatusOK {
		t.Fatalf("preflight: got %d want 200 (passed through to corsMiddleware)", w.Code)
	}
}

// Counting preflights as failed attempts locks out a client holding the CORRECT
// password: a browser issues one per cross-origin request, so it trips the
// 5-failure lockout on its own before any authenticated request goes through.
func TestAuthPreflightDoesNotCountTowardLockout(t *testing.T) {
	now := time.Now()
	h, _ := newAuthUnderTest("secret12", func() time.Time { return now })
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, preflight())
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer secret12"))
	if w.Code != http.StatusOK {
		t.Fatalf("after 10 preflights: got %d want 200 (preflights must not consume lockout budget)", w.Code)
	}
}

// The exemption keys on the full preflight shape. A bare OPTIONS — no Origin, no
// Access-Control-Request-Method — is a normal request corsMiddleware would pass
// to a route handler, so it must still be authenticated or this is an auth bypass.
func TestAuthStillGuardsNonPreflightOptions(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"bare OPTIONS", nil},
		{"OPTIONS with Origin only", map[string]string{"Origin": "http://192.168.1.250:8081"}},
		{"OPTIONS with ACRM only", map[string]string{"Access-Control-Request-Method": "GET"}},
	} {
		r := httptest.NewRequest(http.MethodOptions, "/api/v1/sessions", nil)
		r.RemoteAddr = "192.168.1.50:5555"
		for k, v := range tc.headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d want 401 (must not bypass auth)", tc.name, w.Code)
		}
	}
}

// wsUpgradeReq builds the GET a browser's new WebSocket() issues: no
// Authorization header (browsers cannot set one there), the token riding in the
// Sec-WebSocket-Protocol list instead.
func wsUpgradeReq(protocols string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/mux", nil)
	r.RemoteAddr = "192.168.1.50:5555"
	r.Header.Set("Sec-WebSocket-Protocol", protocols)
	return r
}

// A browser /mux handshake carries the token as the ao.bearer.* subprotocol
// entry (comma-separated alongside the ao.auth marker). It must authenticate.
func TestAuthAcceptsSubprotocolToken(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, wsUpgradeReq("ao.auth, ao.bearer.secret12"))
	if w.Code != http.StatusOK {
		t.Fatalf("subprotocol token: got %d want 200", w.Code)
	}
}

// Guessing the password via the subprotocol channel is still guessing: it must
// consume lockout budget exactly like a wrong Bearer token.
func TestAuthWrongSubprotocolTokenCountsTowardLockout(t *testing.T) {
	now := time.Now()
	h, _ := newAuthUnderTest("secret12", func() time.Time { return now })
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, wsUpgradeReq("ao.auth, ao.bearer.wrongpw1"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d want 401", i, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer secret12"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("after 5 wrong subprotocol tokens: got %d want 429", w.Code)
	}
}

// A request with NO token guesses nothing, so it must not consume lockout
// budget: counting headerless traffic (an old web build's /mux retry loop, a
// stray probe) would 429 every request from that IP — including correctly
// authenticated REST. That is the bug that motivated this test.
func TestAuthTokenlessDoesNotCountTowardLockout(t *testing.T) {
	now := time.Now()
	h, _ := newAuthUnderTest("secret12", func() time.Time { return now })
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req(""))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("tokenless attempt %d: got %d want 401", i, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer secret12"))
	if w.Code != http.StatusOK {
		t.Fatalf("after 10 tokenless requests: got %d want 200 (tokenless must not consume lockout budget)", w.Code)
	}
}

// reqPathCookie builds a request to an arbitrary path, optionally carrying the
// Bearer header and/or the preview auth cookie, for the preview-cookie tests.
func reqPathCookie(method, path, auth, cookie string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = "192.168.1.50:5555"
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: authCookieName, Value: cookie})
	}
	return r
}

// A preview subresource (image/CSS/JS) is fetched by the WebView WITHOUT our
// Authorization header, carrying only the cookie the top-level load set. It must
// authenticate on the preview-files path.
func TestPreviewCookieAuthenticatesSubresource(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqPathCookie(http.MethodGet, "/api/v1/sessions/abc/preview/files/logo.png", "", "secret12"))
	if w.Code != http.StatusOK {
		t.Fatalf("preview subresource with cookie: got %d want 200", w.Code)
	}
}

// The top-level preview file load (Bearer header) must set the auth cookie,
// scoped tightly to that session's preview-files directory and HttpOnly.
func TestPreviewFileSetsScopedCookie(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqPathCookie(http.MethodGet, "/api/v1/sessions/abc/preview/files/index.html", "Bearer secret12", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("preview index with bearer: got %d want 200", w.Code)
	}
	var c *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == authCookieName {
			c = ck
		}
	}
	if c == nil {
		t.Fatal("expected auth cookie on preview file response")
		return
	}
	if c.Path != "/api/v1/sessions/abc/preview/files/" { //nolint:staticcheck // SA5011 false positive: t.Fatal above halts the test
		t.Errorf("cookie Path = %q, want /api/v1/sessions/abc/preview/files/", c.Path)
	}
	if !c.HttpOnly {
		t.Error("cookie must be HttpOnly")
	}
}

// After a password regenerate the WebView still holds the cookie minted under the
// OLD password. The top-level load re-authenticates via the Bearer header (the
// mobile app has the new password), so the server must overwrite the stale cookie
// — otherwise the page's subresources keep sending the old token and 401.
func TestPreviewCookieRefreshedAfterPasswordChange(t *testing.T) {
	h, _ := newAuthUnderTest("newpass12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqPathCookie(http.MethodGet,
		"/api/v1/sessions/abc/preview/files/index.html", "Bearer newpass12", "oldpass12"))
	if w.Code != http.StatusOK {
		t.Fatalf("preview index with new bearer + stale cookie: got %d want 200", w.Code)
	}
	var c *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == authCookieName {
			c = ck
		}
	}
	if c == nil {
		t.Fatal("expected stale auth cookie to be refreshed")
		return
	}
	if c.Value != "newpass12" { //nolint:staticcheck // SA5011 false positive: t.Fatal above halts the test
		t.Errorf("cookie Value = %q, want the current token newpass12", c.Value)
	}
}

// The cookie is a session credential on the authenticated listener: it
// authenticates any route, not just preview files (this is what lets an
// EventSource, which cannot set a header, subscribe). What keeps that from
// being CSRF is the origin rule, exercised below.
func TestCookieAuthenticatesAnyRouteOnAuthenticatedListener(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqPathCookie(http.MethodPost, "/api/v1/sessions/abc/kill", "", "secret12"))
	if w.Code != http.StatusOK {
		t.Fatalf("cookie on /kill: got %d want 200", w.Code)
	}
}

// A cookie is the one credential a browser attaches to requests a hostile page
// initiates, so a cookie-authenticated request from anywhere but this daemon's
// own pages is refused — whether the page announces itself with an Origin or
// (EventSource, <img>) only with Sec-Fetch-Site.
func TestCookieRejectedCrossOrigin(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"foreign origin", map[string]string{"Origin": "http://evil.example"}},
		{"foreign loopback origin", map[string]string{"Origin": "http://localhost:8080"}},
		{"no origin but cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newAuthUnderTest("secret12", time.Now)
			r := reqPathCookie(http.MethodPost, "/api/v1/sessions/abc/kill", "", "secret12")
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("got %d want 403", w.Code)
			}
		})
	}
}

// A Bearer cannot be attached by a page that was not given the password, so the
// same cross-origin request authenticates fine with one. This asymmetry is the
// point of the cookie rule — it must not leak into the header channel, which
// the native mobile client uses while pinning Origin: http://localhost.
func TestBearerUnaffectedByCookieOriginRule(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	r := reqPathCookie(http.MethodPost, "/api/v1/sessions/abc/kill", "Bearer secret12", "")
	r.Header.Set("Origin", "http://localhost")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("bearer from a pinned foreign origin: got %d want 200", w.Code)
	}
}

// POST /api/v1/auth/login exchanges the connection password for the session
// cookie. This is the contract the daemon-served web UI consumes: 204 + an
// HttpOnly, SameSite=Strict, Path=/ ao_conn cookie on success; 401 on a wrong
// password, counted by the same lockout as every other failed guess.
func TestLoginIssuesSessionCookie(t *testing.T) {
	h, lock := newAuthUnderTest("secret12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, loginReq(`{"password":"secret12"}`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("login: got %d want 204", w.Code)
	}
	var c *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == authCookieName {
			c = ck
		}
	}
	if c == nil {
		t.Fatal("login did not set the ao_conn cookie")
		return
	}
	if c.Value != "secret12" || c.Path != "/" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie = %+v, want value secret12, Path=/, HttpOnly, SameSite=Strict", c)
	}

	// The cookie it just minted authenticates a normal route.
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, reqPathCookie(http.MethodGet, "/api/v1/sessions", "", c.Value))
	if w2.Code != http.StatusOK {
		t.Fatalf("cookie from login: got %d want 200", w2.Code)
	}

	// Wrong password: 401, and it consumes lockout budget.
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, loginReq(`{"password":"nope"}`))
	if w3.Code != http.StatusUnauthorized {
		t.Fatalf("bad login: got %d want 401", w3.Code)
	}
	for i := 0; i < 4; i++ {
		h.ServeHTTP(httptest.NewRecorder(), loginReq(`{"password":"nope"}`))
	}
	if !lock.blocked("192.168.1.50") {
		t.Error("five failed logins must trip the per-source lockout")
	}
}

// Login mints a cookie, so it is refused cross-origin: handing one to a foreign
// page is handing out CSRF, password or no password.
func TestLoginRejectedCrossOrigin(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	r := loginReq(`{"password":"secret12"}`)
	r.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login: got %d want 403", w.Code)
	}
}

// loginReq builds a same-origin POST to the login route with the given JSON body.
func loginReq(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, authLoginPath, strings.NewReader(body))
	r.RemoteAddr = "192.168.1.50:5555"
	r.Header.Set("Origin", "http://"+r.Host)
	return r
}

// A normal (non-preview) authenticated request must not get an auth cookie set,
// so the cookie only ever exists for the preview flow.
func TestNoCookieSetOnNonPreviewRoutes(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req("Bearer secret12")) // path /api/v1/sessions
	if w.Code != http.StatusOK {
		t.Fatalf("got %d want 200", w.Code)
	}
	for _, ck := range w.Result().Cookies() {
		if ck.Name == authCookieName {
			t.Fatal("auth cookie must not be set on a non-preview route")
		}
	}
}

func TestAuthLockoutIsPerSource(t *testing.T) {
	now := time.Now()
	h, _ := newAuthUnderTest("secret12", func() time.Time { return now })

	// Source A: lock with 5 failed attempts from 192.168.1.50
	sourceA := "192.168.1.50:5555"
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, reqFrom(sourceA, "Bearer wrong"))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("source A attempt %d: got %d want 401", i, w.Code)
		}
	}
	// Verify source A is now locked
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqFrom(sourceA, "Bearer secret12"))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("source A locked check: got %d want 429", w.Code)
	}

	// Source B: should NOT be locked despite source A being locked
	sourceB := "192.168.1.99:6666"
	// B with correct password should be 200, not 429
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqFrom(sourceB, "Bearer secret12"))
	if w.Code != http.StatusOK {
		t.Fatalf("source B with correct password: got %d want 200", w.Code)
	}

	// B with wrong password should be 401, not 429
	w = httptest.NewRecorder()
	h.ServeHTTP(w, reqFrom(sourceB, "Bearer wrong"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("source B with wrong password: got %d want 401", w.Code)
	}
}
