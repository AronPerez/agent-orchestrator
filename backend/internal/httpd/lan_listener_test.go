package httpd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

func TestLANManagerAuthGatesSharedHandler(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(inner, st, 0, slog.Default(), nil) // port 0 → ephemeral
	port, err := m.Start(0, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	if !m.Running() || m.BoundPort() != port {
		t.Fatalf("running=%v boundPort=%d port=%d", m.Running(), m.BoundPort(), port)
	}

	// An API path, not "/anything": paths the daemon does not own are the web UI
	// bundle, which is served without a password on purpose (see webUIBypass).
	base := fmt.Sprintf("http://127.0.0.1:%d/api/v1/anything", port)
	// no auth → 401
	resp, _ := http.Get(base)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-auth: got %d want 401", resp.StatusCode)
	}
	// with auth → 200
	req, _ := http.NewRequest(http.MethodGet, base, nil)
	req.Header.Set("Authorization", "Bearer secret12")
	resp2, _ := http.DefaultClient.Do(req)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("auth: got %d want 200", resp2.StatusCode)
	}
}

// TestLANManagerBlocksLoopbackOnlyControlRoutes proves the LAN listener never
// serves /shutdown, /internal/*, /api/v1/mobile*, /api/v1/dev*, or
// /api/v1/browser* — even when the request carries a spoofed Host: 127.0.0.1
// and valid LAN auth, since gating on Host alone (localControlRequest) is what
// let a LAN client reach these routes.
func TestLANManagerBlocksLoopbackOnlyControlRoutes(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(inner, st, 0, slog.Default(), nil)
	port, err := m.Start(0, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())

	blocked := []string{
		"/shutdown",
		"/internal/telemetry/cli-invoked",
		"/api/v1/mobile/status",
		"/api/v1/dev/import-projects",
		"/api/v1/browser/status",
		"/api/v1/sessions/ao-1/preview/server",
	}
	for _, path := range blocked {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		req.Host = "127.0.0.1" // spoofed loopback Host
		req.Header.Set("Authorization", "Bearer secret12")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: request failed: %v", path, err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: got %d want 404 (Host-spoof + valid auth must not reach control routes)", path, resp.StatusCode)
		}
	}

	// A normal app route must still be reachable through the LAN listener
	// (not swallowed by the control-route filter). Auth-gating, not the
	// control filter, decides its fate.
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/v1/sessions", port), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sessions: request failed: %v", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("/api/v1/sessions: got 404, should not be blocked by the control-route filter")
	}

}

func TestLANManagerStartStopIdempotent(t *testing.T) {
	m := NewLANManager(http.NotFoundHandler(), &authState{}, 0, slog.Default(), nil)
	p1, _ := m.Start(0, "")
	p2, _ := m.Start(0, "") // idempotent — same port, no error
	if p1 != p2 {
		t.Fatalf("second start changed port: %d != %d", p1, p2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if m.Running() {
		t.Fatal("still running after stop")
	}
	_ = m.Stop(ctx) // second stop is a no-op
}

// The bind mode narrows the listening socket. Binding 127.0.0.1 is the
// observable stand-in for the tailscale case (a real tailnet address is not
// available in CI): the listener must answer on the address it was given and
// be absent from every other one.
func TestLANManagerBindsRequestedAddress(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(inner, st, 0, slog.Default(), nil)
	port, err := m.Start(0, "127.0.0.1")
	if err != nil {
		t.Fatalf("start bound to 127.0.0.1: %v", err)
	}
	defer m.Stop(context.Background())

	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/anything", port), nil)
	req.Header.Set("Authorization", "Bearer secret12")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request to the bound address: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bound address: got %d want 200", resp.StatusCode)
	}

	// Nothing else on this host reaches it. A LAN interface may not exist in CI,
	// so probe the other loopback address instead — the point is that the socket
	// is not wildcard-bound.
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("[::1]:%d", port), time.Second); err == nil {
		c.Close()
		t.Error("listener bound to 127.0.0.1 also answered on ::1 — it is wildcard-bound")
	}
}

// An unresolvable bind mode must fail loudly. Falling back to 0.0.0.0 would
// widen exposure at the exact moment the user asked to narrow it.
func TestLANManagerRejectsUnresolvableBind(t *testing.T) {
	m := NewLANManager(http.NotFoundHandler(), &authState{}, 0, slog.Default(), nil)
	if _, err := m.Start(0, "not-an-ip"); err == nil {
		defer m.Stop(context.Background())
		t.Fatal("expected an error, got a listener")
	}
	if m.Running() {
		t.Fatal("a failed bind must leave the manager stopped")
	}
}

// The LAN listener is credential-gated, so origin is not what authorizes a
// request there — which is why the native mobile client can pin
// Origin: http://localhost on a state-changing call to the LAN address and
// still be served. The strict origin rule applies to the no-auth loopback
// listener (and to cookie-authenticated requests), not to a valid Bearer.
func TestLANListenerAllowsPinnedOriginWithBearer(t *testing.T) {
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	router := newTestRouter(config.Config{AllowedOrigins: []string{"app://renderer"}}, discardLogger(), nil)
	m := NewLANManager(router, st, 0, slog.Default(), nil)
	port, err := m.Start(0, "127.0.0.1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())

	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/v1/sessions", port), nil)
	req.Header.Set("Authorization", "Bearer secret12")
	req.Header.Set("Origin", "http://localhost") // what packages/mobile/lib/mux.ts pins
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("the native mobile client's pinned Origin was rejected on the LAN listener")
	}
}

// The same zero-configuration guarantee on the LAN listener, end to end through
// the real router and authMiddleware: a UI the daemon serves on :3011 logs in,
// gets the session cookie, and drives the API — with an empty allowlist and no
// AO_ALLOWED_ORIGINS anywhere. Host-equality carries it; if this ever needs an
// allowlist entry, the daemon-served web UI is broken.
func TestDaemonServedUIOnLANNeedsNoAllowlistEntry(t *testing.T) {
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	router := newTestRouter(config.Config{AllowedOrigins: nil}, discardLogger(), nil)
	m := NewLANManager(router, st, 0, slog.Default(), nil)
	port, err := m.Start(0, "127.0.0.1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	// The origin a real phone sees is the LAN address, NOT loopback. Using the
	// socket's own loopback URL here would send every request down
	// isLoopbackOrigin and the test would pass while host-equality — the thing
	// this test exists to prove — was broken for every actual client. Host is
	// pinned to match; hostGuard exempts the LAN listener, so the socket it
	// physically listens on is irrelevant to the logic under test.
	const lanHost = "192.168.1.227:65142"
	const origin = "http://" + lanHost

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	login, _ := http.NewRequest(http.MethodPost, base+"/api/v1/auth/login",
		strings.NewReader(`{"password":"secret12"}`))
	login.Host = lanHost
	login.Header.Set("Origin", origin)
	login.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(login)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("login: got %d want 204", resp.StatusCode)
	}

	// The jar now holds ao_conn. Every shape the page then uses must authenticate
	// AND pass the origin rule. The Origin-BEARING cases are the ones that matter:
	// a browser sends Origin on same-origin fetch(), so an Origin-less request
	// alone would prove nothing about what the UI actually does.
	cookies := jar.Cookies(login.URL)
	for _, tc := range []struct {
		name, method, origin, secFetchSite string
	}{
		{name: "GET (fetch sends Origin)", method: http.MethodGet, origin: origin},
		{name: "GET with fetch metadata", method: http.MethodGet, origin: origin, secFetchSite: "same-origin"},
		{name: "state-changing POST", method: http.MethodPost, origin: origin},
		{name: "PATCH", method: http.MethodPatch, origin: origin},
		// EventSource is same-origin, so the browser sends no Origin at all.
		{name: "SSE-shaped GET (no Origin)", method: http.MethodGet, secFetchSite: "same-origin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, base+"/api/v1/sessions", nil)
			req.Host = lanHost
			for _, c := range cookies {
				req.AddCookie(c)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.secFetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.secFetchSite)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s: %v", tc.method, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				t.Fatalf("daemon-served LAN UI got %d with the cookie it just logged in for", resp.StatusCode)
			}
		})
	}
}

// unauthenticatedLANRoutes names the routes that are allowed to answer an
// unauthenticated request on the LAN listener. It is EMPTY on purpose.
//
// The LAN listener's exemption from hostGuard (see cors.go) rests entirely on
// every route there being credential-gated: a DNS-rebinding page reaching the
// LAN socket carries no Bearer and no ao_conn cookie — the browser scopes the
// cookie to the host the daemon actually served — so it gets nothing. Add one
// unauthenticated route that returns data or has a side effect and that argument
// silently stops holding, on a listener bound to the network.
//
// If you are here because TestEveryLANRouteIsCredentialGated failed: adding your
// route to this list is the mechanism working, not a formality. A public,
// data-free app shell (a static bundle rendering a login prompt) is fine. A
// route that reads state, or changes any, is not — it re-opens rebinding against
// the LAN listener and the exemption has to be revisited with it.
// The embedded web UI (internal/httpd/webui, routed by webUIBypass) is the one
// unauthenticated surface on the LAN listener, and it is deliberately absent
// from this map: it is not a registered chi route but the NotFound fallback, so
// chi.Walk below never yields it and no entry could exempt it. Declaring it here
// anyway would be worse than useless — the key would silently exempt a real
// route of that name later. It is a data-free app shell whose only job is to
// render the password prompt, and it is covered by
// TestLANManagerServesWebUIWithoutPassword, which asserts the other half: that
// every path the daemon answers itself still requires the password.
//
// The walk below is therefore still exhaustive over registered routes, which is
// the property that matters: webui.IsUIRequest excludes every daemon prefix, so
// nothing chi serves can slip through the bypass.
var unauthenticatedLANRoutes = map[string]struct{}{}

// TestEveryLANRouteIsCredentialGated walks the real router and proves each route
// either authenticates or is not served at all on the LAN listener. Prose in
// AGENTS.md cannot fail a build; this can.
func TestEveryLANRouteIsCredentialGated(t *testing.T) {
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	router := newTestRouter(config.Config{}, discardLogger(), nil)
	m := NewLANManager(router, st, 0, slog.Default(), nil)
	port, err := m.Start(0, "127.0.0.1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	walked := 0
	err = chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if _, exempt := unauthenticatedLANRoutes[route]; exempt {
			return nil
		}
		// Substitute any URL params so the request reaches the route rather than
		// dying in the router.
		path := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(route, "probe")
		path = strings.TrimSuffix(path, "/*") // wildcard tails
		walked++
		req, err := http.NewRequest(method, base+path, nil)
		if err != nil {
			return nil // a pattern we cannot build a request for proves nothing
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("%s %s: %v", method, route, err)
			return nil
		}
		defer resp.Body.Close()
		// 401 = authenticated surface refusing us. 404/405 = lanControlBlock or
		// the router declining to serve it at all. Anything else means an
		// unauthenticated caller got a real answer.
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusNotFound, http.StatusMethodNotAllowed:
		default:
			t.Errorf("%s %s answered an UNAUTHENTICATED LAN request with %d — "+
				"the hostGuard exemption for the LAN listener assumes this cannot happen; "+
				"see unauthenticatedLANRoutes", method, route, resp.StatusCode)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	if walked == 0 {
		t.Fatal("walked no routes — the test proved nothing")
	}
	t.Logf("checked %d routes", walked)
}

// TestLANManagerServesWebUIWithoutPassword pins the one deliberate hole in LAN
// auth: the static web UI, which is the password prompt itself. It must load
// unauthenticated, and nothing the daemon answers may follow it through.
func TestLANManagerServesWebUIWithoutPassword(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(inner, st, 0, slog.Default(), nil)
	port, err := m.Start(0, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())

	// The UI shell and its assets: no password, served by the shared handler.
	for _, path := range []string{"/", "/assets/index.js", "/projects/ao-1"} {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
		if err != nil {
			t.Fatalf("%s: request failed: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			t.Fatalf("%s: got 401; the UI must load so the user can enter a password", path)
		}
	}

	// Everything that can carry daemon data still needs the password, including
	// the GET routes that look most like a page.
	for _, path := range []string{"/api/v1/sessions", "/mux", "/healthz", "/readyz"} {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, path))
		if err != nil {
			t.Fatalf("%s: request failed: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: got %d want 401 (the web UI bypass must not cover daemon routes)", path, resp.StatusCode)
		}
	}

	// A POST is never a UI request, so an unknown path cannot be used to reach a
	// side effect without the password.
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/anything", port), "application/json", nil)
	if err != nil {
		t.Fatalf("post: request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /anything: got %d want 401", resp.StatusCode)
	}
}
