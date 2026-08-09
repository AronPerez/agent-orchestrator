package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// TestCORS exercises the allowlist boundary on a real router: trusted origins
// get per-origin CORS headers (REST reads and preflights), everything else —
// including the opaque "null" origin and no-Origin CLI traffic — gets none.
func TestCORS(t *testing.T) {
	cfg := config.Config{AllowedOrigins: []string{"app://renderer"}}
	router := newTestRouter(cfg, discardLogger(), nil)
	srv := httptest.NewServer(router)
	defer srv.Close()

	tests := []struct {
		name       string
		method     string
		headers    map[string]string
		wantStatus int
		wantACAO   string
	}{
		{
			name:       "allowed origin gets ACAO",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "app://renderer"},
			wantStatus: http.StatusOK,
			wantACAO:   "app://renderer",
		},
		{
			// Not in the allowlist — trusted because loopback-served content
			// can already reach the daemon directly (dev/preview servers on
			// arbitrary ports).
			name:       "loopback origin allowed without an allowlist entry",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "http://localhost:5181"},
			wantStatus: http.StatusOK,
			wantACAO:   "http://localhost:5181",
		},
		{
			name:       "loopback IP origin allowed",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "http://127.0.0.1:8080"},
			wantStatus: http.StatusOK,
			wantACAO:   "http://127.0.0.1:8080",
		},
		{
			name:       "isolated localhost preview origin allowed",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "http://ao-preview.mfxs2mi.localhost:5181"},
			wantStatus: http.StatusOK,
			wantACAO:   "http://ao-preview.mfxs2mi.localhost:5181",
		},
		{
			// localhost in the host position of a non-loopback origin must not
			// fool the predicate.
			name:       "lookalike origin rejected",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "http://localhost.evil.example"},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
		{
			// Rejected outright, not just denied CORS headers: a missing ACAO
			// hides the response but a "simple" cross-origin POST would still
			// execute the handler on this no-auth daemon.
			name:       "unknown origin is rejected before handlers",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "http://evil.example"},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
		{
			name:       "localhost suffix lookalike rejected",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "http://ao-preview.localhost.evil.example"},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
		{
			name:       "null origin is rejected",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "null"},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
		{
			name:       "no origin passes through untouched",
			method:     http.MethodGet,
			headers:    nil,
			wantStatus: http.StatusOK,
			wantACAO:   "",
		},
		{
			name:   "preflight from allowed origin",
			method: http.MethodOptions,
			headers: map[string]string{
				"Origin":                         "app://renderer",
				"Access-Control-Request-Method":  "POST",
				"Access-Control-Request-Headers": "content-type",
			},
			wantStatus: http.StatusNoContent,
			wantACAO:   "app://renderer",
		},
		{
			name:   "preflight from unknown origin is rejected",
			method: http.MethodOptions,
			headers: map[string]string{
				"Origin":                        "http://evil.example",
				"Access-Control-Request-Method": "POST",
			},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
	}

	client := &http.Client{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, srv.URL+"/healthz", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s /healthz: %v", tt.method, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if got := resp.Header.Get("Access-Control-Allow-Origin"); got != tt.wantACAO {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantACAO)
			}
			if tt.headers["Origin"] != "" && resp.Header.Get("Vary") == "" {
				t.Error("Vary header missing for request with Origin")
			}
		})
	}
}

// TestCORSPreflightHeaders pins the preflight grant shape: methods, echoed
// request headers, max-age, and the private-network opt-in.
func TestCORSPreflightHeaders(t *testing.T) {
	cfg := config.Config{AllowedOrigins: []string{"app://renderer"}}
	router := newTestRouter(cfg, discardLogger(), nil)
	srv := httptest.NewServer(router)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodOptions, srv.URL+"/api/v1/sessions", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Origin", "app://renderer")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type")
	req.Header.Set("Access-Control-Request-Private-Network", "true")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	for header, want := range map[string]string{
		"Access-Control-Allow-Origin":          "app://renderer",
		"Access-Control-Allow-Methods":         "GET, POST, PATCH, PUT, DELETE, OPTIONS",
		"Access-Control-Allow-Headers":         "content-type",
		"Access-Control-Max-Age":               "600",
		"Access-Control-Allow-Private-Network": "true",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// A state-changing request runs on ambient authority on the no-auth loopback
// listener — reaching the socket is the whole authorization — so "some loopback
// origin" is not good enough for it, even though it stays good enough for reads
// (workspace previews, dev servers). Only an origin this daemon actually names
// gets through.
func TestCORSStrictOriginOnStateChangingRoutes(t *testing.T) {
	cfg := config.Config{AllowedOrigins: []string{"app://renderer"}}
	srv := httptest.NewServer(newTestRouter(cfg, discardLogger(), nil))
	defer srv.Close()

	tests := []struct {
		name          string
		origin        string
		secFetchSite  string
		wantForbidden bool
	}{
		{name: "foreign loopback dev server", origin: "http://localhost:8080", wantForbidden: true},
		{name: "foreign loopback IP", origin: "http://127.0.0.1:5173", wantForbidden: true},
		{name: "workspace preview subdomain", origin: "http://ao-preview.abc.localhost:5181", wantForbidden: true},
		{name: "cross-site with no origin at all", secFetchSite: "cross-site", wantForbidden: true},
		{name: "allowlisted renderer", origin: "app://renderer"},
		{name: "allowlisted origin the browser calls cross-site", origin: "app://renderer", secFetchSite: "cross-site"},
		{name: "the daemon's own origin", origin: srv.URL},
		{name: "browser-attested same-origin", secFetchSite: "same-origin"},
		{name: "no origin (CLI)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.secFetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tt.secFetchSite)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			defer resp.Body.Close()
			if got := resp.StatusCode == http.StatusForbidden; got != tt.wantForbidden {
				t.Errorf("status = %d, forbidden = %v, want forbidden = %v", resp.StatusCode, got, tt.wantForbidden)
			}
		})
	}
}

// Reads keep the looser loopback trust: workspace preview pages and local dev
// servers fetch from the daemon on ports that can never be allowlisted ahead of
// time, and a read carries no side effect to protect.
func TestCORSLoopbackOriginStillReads(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(config.Config{}, discardLogger(), nil))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/healthz", nil)
	req.Header.Set("Origin", "http://ao-preview.abc.localhost:5181")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("loopback origin read: got %d want 200", resp.StatusCode)
	}
}

// W2's keystone: a UI the daemon serves itself is same-origin with the API, so
// it must work with ZERO configuration — an empty allowlist, no
// AO_ALLOWED_ORIGINS, on either listener. Host-equality is what provides that;
// nothing here may come to depend on an allowlist entry.
func TestDaemonServedUINeedsNoAllowlistEntry(t *testing.T) {
	// Deliberately empty: not even the app://renderer default.
	srv := httptest.NewServer(newTestRouter(config.Config{AllowedOrigins: nil}, discardLogger(), nil))
	defer srv.Close()

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req, err := http.NewRequest(method, srv.URL+"/api/v1/sessions", nil)
			if err != nil {
				t.Fatal(err)
			}
			// What a browser sends for a page the daemon served: its own origin,
			// and the fetch-metadata label to match.
			req.Header.Set("Origin", srv.URL)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("%s: %v", method, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusForbidden {
				t.Fatalf("%s from the daemon's own origin was 403'd with an empty allowlist", method)
			}
			if got := resp.Header.Get("Access-Control-Allow-Origin"); got != srv.URL {
				t.Errorf("ACAO = %q, want the daemon's own origin %q", got, srv.URL)
			}
		})
	}

	// The same page's EventSource: same-origin, so the browser sends no Origin
	// at all. It must not be mistaken for a foreign caller.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("SSE-shaped GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("a same-origin request with no Origin header was 403'd")
	}
}
