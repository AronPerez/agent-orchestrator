package httpd

import (
	"context"
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
// AO_ALLOWED_ORIGINS, on either listener.
//
// Read this one honestly: on the loopback listener a daemon-served page's origin
// IS loopback, so isLoopbackOrigin carries these cases and host-equality is
// never the load-bearing branch. That is not a blind spot here — hostGuard only
// admits a non-loopback Host when it belongs to an allowlisted origin, so the
// non-loopback same-origin shape is unreachable on this listener by
// construction. It IS reachable on the LAN listener, where the daemon's own
// address is neither loopback nor allowlisted, and
// TestDaemonServedUIOnLANNeedsNoAllowlistEntry is what exercises it.
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

// DNS rebinding: the attacker serves a page from http://evil.com:3001 (they own
// the host AND the port), then repoints evil.com at 127.0.0.1. The page's next
// request reaches this daemon and is GENUINELY same-origin — the browser sends
// Host: evil.com:3001, Sec-Fetch-Site: same-origin, and for a same-origin GET
// no Origin header at all, so every origin-based trust path passes on purpose.
// Only the Host tells us the caller thought it was talking to evil.com.
//
// The read case is the dangerous one: with no Origin header there is nothing for
// the CORS gate to reject, and the response is readable to the page because a
// same-origin request runs no CORS check at all.
func TestRebindingHostRejected(t *testing.T) {
	cfg := config.Config{AllowedOrigins: []string{"app://renderer", "http://192.168.1.250:3000"}}
	srv := httptest.NewServer(newTestRouter(cfg, discardLogger(), nil))
	defer srv.Close()

	for _, tc := range []struct {
		name, method, host, origin, secFetchSite string
		wantForbidden                            bool
	}{
		{
			name:   "same-origin read after rebinding (no Origin at all)",
			method: http.MethodGet, host: "evil.com:3001", secFetchSite: "same-origin",
			wantForbidden: true,
		},
		{
			name:   "same-origin read after rebinding, pre-fetch-metadata browser",
			method: http.MethodGet, host: "evil.com:3001",
			wantForbidden: true,
		},
		{
			name: "write after rebinding", method: http.MethodPost, host: "evil.com:3001",
			origin: "http://evil.com:3001", secFetchSite: "same-origin", wantForbidden: true,
		},
		{
			name:   "write after rebinding with no Origin (form POST)",
			method: http.MethodPost, host: "evil.com:3001", secFetchSite: "same-origin",
			wantForbidden: true,
		},
		// Everything that legitimately reaches the loopback listener keeps working.
		{name: "loopback IP", method: http.MethodPost, host: "127.0.0.1:3001"},
		{name: "localhost", method: http.MethodPost, host: "localhost:3001"},
		{name: "IPv6 loopback", method: http.MethodPost, host: "[::1]:3001"},
		{name: "workspace preview subdomain", method: http.MethodGet, host: "ao-preview.mfxs2mi.localhost:3001"},
		// A dev proxy with changeOrigin:false forwards the browser's own Host, so
		// a UI the operator allowlisted must be addressable by that Host too.
		{name: "allowlisted dev-proxy host", method: http.MethodGet, host: "192.168.1.250:3000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, srv.URL+"/api/v1/sessions", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.secFetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.secFetchSite)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("%s: %v", tc.method, err)
			}
			defer resp.Body.Close()
			if got := resp.StatusCode == http.StatusForbidden; got != tc.wantForbidden {
				t.Errorf("Host %q: status = %d, forbidden = %v, want forbidden = %v",
					tc.host, resp.StatusCode, got, tc.wantForbidden)
			}
		})
	}
}

// The LAN listener is exempt from the Host rule: it is credential-gated, and a
// rebinding page obtains neither the password nor the ao_conn cookie (which the
// browser binds to the host the daemon actually served). Enforcing it there
// would instead break how a phone legitimately addresses the bridge — a
// Tailscale MagicDNS name, or ao-phone-proxy forwarding the client's own Host.
func TestHostGuardExemptsLANListener(t *testing.T) {
	guarded := hostGuard(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, tc := range []struct {
		name, host string
		lan        bool
		want       int
	}{
		{"loopback listener, foreign host", "evil.com:3001", false, http.StatusForbidden},
		{"loopback listener, no host", "", false, http.StatusForbidden},
		{"LAN listener, MagicDNS name", "my-mac.tail1234.ts.net:3011", true, http.StatusOK},
		{"LAN listener, LAN IP", "192.168.1.5:3011", true, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
			r.Host = tc.host
			if tc.lan {
				r = r.WithContext(context.WithValue(r.Context(), lanListenerCtxKey{}, true))
			}
			w := httptest.NewRecorder()
			guarded.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Errorf("got %d want %d", w.Code, tc.want)
			}
		})
	}
}
