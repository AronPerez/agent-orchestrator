package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func fakeBundle() fstest.MapFS {
	return fstest.MapFS{
		"index.html":             {Data: []byte("<!doctype html><title>AO</title>")},
		"assets/index-abc123.js": {Data: []byte("console.log(1)")},
	}
}

func jsonNotFound() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found"}`))
	})
}

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// The daemon owns its own routes; the SPA fallback must never answer one, or an
// unknown /api/v1 path would return HTML instead of the locked JSON envelope —
// and, on the LAN listener, would skip the connection password.
func TestIsUIRequestExcludesDaemonRoutes(t *testing.T) {
	daemon := []string{
		"/api/v1/sessions", "/api/v1", "/api/v1/sessions/ao-1/preview/files/x.png",
		"/mux", "/internal/telemetry/cli-invoked", "/shutdown", "/healthz", "/readyz",
	}
	for _, p := range daemon {
		if IsUIRequest(httptest.NewRequest(http.MethodGet, p, nil)) {
			t.Errorf("%s: treated as a UI request", p)
		}
	}
	for _, p := range []string{"/", "/projects/ao-1", "/assets/index-abc123.js", "/apidocs"} {
		if !IsUIRequest(httptest.NewRequest(http.MethodGet, p, nil)) {
			t.Errorf("%s: not treated as a UI request", p)
		}
	}
	// Only safe methods can be a page; a POST to an unknown path is a client error.
	for _, m := range []string{http.MethodPost, http.MethodDelete, http.MethodPatch} {
		if IsUIRequest(httptest.NewRequest(m, "/anything", nil)) {
			t.Errorf("%s /anything: treated as a UI request", m)
		}
	}
	// Traversal must not sneak an API path past the exclusion list.
	if IsUIRequest(httptest.NewRequest(http.MethodGet, "/assets/../api/v1/sessions", nil)) {
		t.Error("traversal into /api treated as a UI request")
	}
}

func TestHandlerServesShellAssetsAndSPAFallback(t *testing.T) {
	h := handlerFor(fakeBundle(), jsonNotFound())

	shell := get(t, h, http.MethodGet, "/")
	if shell.Code != http.StatusOK || !strings.Contains(shell.Body.String(), "<title>AO</title>") {
		t.Fatalf("GET /: %d %q", shell.Code, shell.Body.String())
	}
	if got := shell.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("shell Cache-Control = %q, want no-store", got)
	}

	asset := get(t, h, http.MethodGet, "/assets/index-abc123.js")
	if asset.Code != http.StatusOK || asset.Body.String() != "console.log(1)" {
		t.Fatalf("GET asset: %d %q", asset.Code, asset.Body.String())
	}
	if !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset Cache-Control = %q, want immutable", asset.Header().Get("Cache-Control"))
	}

	// A client-side route has no file behind it and must still boot the app.
	deep := get(t, h, http.MethodGet, "/projects/ao-1/sessions/ao-2")
	if deep.Code != http.StatusOK || !strings.Contains(deep.Body.String(), "<title>AO</title>") {
		t.Fatalf("deep link: %d %q", deep.Code, deep.Body.String())
	}
}

func TestHandlerDelegatesDaemonRoutes(t *testing.T) {
	h := handlerFor(fakeBundle(), jsonNotFound())
	rec := get(t, h, http.MethodGet, "/api/v1/nope")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "not_found") {
		t.Fatalf("GET /api/v1/nope: %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Security-Policy") != "" {
		t.Error("CSP set on a delegated API response")
	}
}

// A daemon built without running the frontend bundle step must still start and
// say so, rather than serving a blank page or panicking.
func TestHandlerWithoutBundleReports503(t *testing.T) {
	h := handlerFor(fstest.MapFS{}, jsonNotFound())
	rec := get(t, h, http.MethodGet, "/")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET / without a bundle: got %d want 503", rec.Code)
	}
	if rec := get(t, h, http.MethodGet, "/api/v1/nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("API delegation broke without a bundle: got %d", rec.Code)
	}
}

// The CSP is a response header, not a meta tag, precisely so the mux WebSocket
// origin can be the host this request actually arrived on.
func TestContentSecurityPolicyNamesTheRequestHost(t *testing.T) {
	h := handlerFor(fakeBundle(), jsonNotFound())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "192.168.1.5:3011"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "ws://192.168.1.5:3011", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q missing %q", csp, want)
		}
	}
}

// Host is client-supplied; a value carrying directive separators must be
// dropped, not reflected into the policy.
func TestContentSecurityPolicyIgnoresAHostileHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "evil; script-src *"
	if csp := contentSecurityPolicy(req); strings.Contains(csp, "script-src *") {
		t.Fatalf("hostile Host reflected into CSP: %q", csp)
	}
}
