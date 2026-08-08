package httpd

import (
	"net/http"
	"net/http/httptest"
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
	if c.Path != "/api/v1/sessions/abc/preview/files/" {
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
	if c.Value != "newpass12" {
		t.Errorf("cookie Value = %q, want the current token newpass12", c.Value)
	}
}

// The cookie must NOT authenticate any non-preview endpoint: a preview page that
// tries POST /kill with only the cookie is rejected. This is the server-side
// half of the guarantee (the cookie's Path already stops the browser sending it
// here at all).
func TestPreviewCookieRejectedOnOtherEndpoints(t *testing.T) {
	h, _ := newAuthUnderTest("secret12", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqPathCookie(http.MethodPost, "/api/v1/sessions/abc/kill", "", "secret12"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("cookie on /kill: got %d want 401", w.Code)
	}
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
