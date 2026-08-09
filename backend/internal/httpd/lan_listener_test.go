package httpd

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
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

// --- a blocked route must not look like a missing one -----------------------
//
// The block list is a policy decision, so it must not be reported with the code
// that means "this daemon does not have that endpoint" — an operator chasing
// ROUTE_NOT_FOUND audits daemon builds and finds nothing wrong. These tests pin
// the distinction on the wire, and pin exactly how much of it an unauthenticated
// LAN caller can see.

// lanBlockFixture builds a LAN listener over a real chi router, so route
// matching (and therefore webUIBypass) behaves as it does in the daemon:
// /api/v1/dev/import-projects is a registered route, /api/v1/no-such-route is
// not, and unmatched paths get the router's locked JSON 404.
func lanBlockFixture(t *testing.T) (router http.Handler, port int) {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/api/v1/dev/import-projects", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})
	r.Get("/api/v1/sessions", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	})
	r.NotFound(notFoundJSON)

	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(r, st, 0, slog.Default(), nil)
	port, err := m.Start(0, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { m.Stop(context.Background()) })
	return r, port
}

// lanGet issues a GET to the LAN listener and returns the status and the decoded
// error envelope (zero-valued when the body is not one).
func lanGet(t *testing.T, port int, path, bearer string) (int, envelopeBody) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		t.Fatalf("%s: new request: %v", path, err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: request failed: %v", path, err)
	}
	defer resp.Body.Close()
	var body envelopeBody
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

type envelopeBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func TestLANBlockedRouteIsNotReportedAsMissing(t *testing.T) {
	_, port := lanBlockFixture(t)

	// A loopback-only route over the LAN listener: still 404 (it is genuinely
	// not mounted on this listener), but the code says policy, not absence.
	status, body := lanGet(t, port, "/api/v1/dev/import-projects", "secret12")
	if status != http.StatusNotFound || body.Code != "ROUTE_LOOPBACK_ONLY" {
		t.Fatalf("blocked route: got %d %s, want 404 ROUTE_LOOPBACK_ONLY", status, body.Code)
	}
	if !strings.Contains(body.Message, "loopback listener only") {
		t.Fatalf("blocked route: message does not say why: %q", body.Message)
	}

	// A genuinely absent route keeps meaning exactly what it did before.
	status, body = lanGet(t, port, "/api/v1/no-such-route", "secret12")
	if status != http.StatusNotFound || body.Code != "ROUTE_NOT_FOUND" {
		t.Fatalf("absent route: got %d %s, want 404 ROUTE_NOT_FOUND", status, body.Code)
	}

	// An unrelated app route is untouched by either.
	if status, _ := lanGet(t, port, "/api/v1/sessions", "secret12"); status != http.StatusOK {
		t.Fatalf("/api/v1/sessions: got %d, want 200", status)
	}
}

// The loopback listener serves the shared router directly — lanControlBlock
// wraps the LAN-served handler only — so the same route is unchanged there.
func TestLoopbackStillServesBlockedRoute(t *testing.T) {
	router, _ := lanBlockFixture(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/dev/import-projects")
	if err != nil {
		t.Fatalf("loopback request: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(got) != "ok" {
		t.Fatalf("loopback: got %d %q, want 200 \"ok\"", resp.StatusCode, got)
	}
}

// What an unauthenticated LAN caller may learn. The answer is a constant of the
// AO build — "paths under this prefix are loopback-only" — and never which
// routes exist behind it: a registered blocked route and a path under the same
// prefix that no handler serves are byte-identical. Without this, a nicer error
// message would have turned the block into a route-table oracle for anyone who
// can reach the socket.
func TestUnauthenticatedLANCallerLearnsNoRouteTable(t *testing.T) {
	_, port := lanBlockFixture(t)

	const realPath, fakePath = "/api/v1/dev/import-projects", "/api/v1/dev/no-such-dev-route"
	realStatus, hit := lanGet(t, port, realPath, "")
	fakeStatus, fake := lanGet(t, port, fakePath, "")
	if realStatus != fakeStatus || hit.Code != fake.Code {
		t.Fatalf("blocked prefix leaks route existence: real %d %q vs absent %d %q",
			realStatus, hit.Code, fakeStatus, fake.Code)
	}
	if hit.Code != "ROUTE_LOOPBACK_ONLY" {
		t.Fatalf("unauthenticated blocked path: got code %q, want ROUTE_LOOPBACK_ONLY", hit.Code)
	}
	// The messages differ only where they echo the caller's own path back, so
	// the answer carries nothing the caller did not already supply.
	if got := strings.Replace(fake.Message, fakePath, realPath, 1); got != hit.Message {
		t.Fatalf("blocked-path message is not a pure function of the requested path:\n real: %q\nabsent: %q",
			hit.Message, fake.Message)
	}

	// Outside the blocked prefixes nothing changed: no credential, no answer.
	if status, _ := lanGet(t, port, "/api/v1/no-such-route", ""); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated ordinary path: got %d, want 401", status)
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
	// The real router, not a stand-in handler: the bypass now decides by asking
	// the router whether a route matches, so a fake handler would exercise a
	// different code path than production takes.
	router := newTestRouter(config.Config{}, discardLogger(), nil)
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(router, st, 0, slog.Default(), nil)
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

// TestWebUIBypassFailsClosedForRegisteredRoutes is the regression test for the
// deny-list's failure mode. webui.IsUIRequest enumerates the paths the daemon
// owns TODAY, so a top-level route added later without updating it stops being
// excluded — and before the bypass consulted the router, that handed the request
// to the router and ran the handler with no password, on a socket bound to the
// network. Verified against the pre-fix code: GET /metrics returned 200 and the
// handler's body to an unauthenticated caller.
//
// The daemon has no such route today, which is exactly why this test invents
// one: the bug is only reachable through a route nobody has written yet, and it
// would be silent and remote-only when it arrives.
func TestWebUIBypassFailsClosedForRegisteredRoutes(t *testing.T) {
	router := chi.NewRouter()
	router.Get("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "daemon-data")
	})
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(router, st, 0, slog.Default(), nil)
	port, err := m.Start(0, "127.0.0.1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	// A registered route is the daemon's, whether or not anyone remembered to
	// add its prefix to webui.daemonPrefixes.
	resp, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /metrics: got %d %q, want 401 — an unlisted route must not answer without the password",
			resp.StatusCode, body)
	}
	if strings.Contains(string(body), "daemon-data") {
		t.Fatal("GET /metrics: the handler ran for an unauthenticated caller")
	}

	// ...and the same route with the password still works, so failing closed
	// costs nothing but the credential.
	req, _ := http.NewRequest(http.MethodGet, base+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret12")
	authed, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("authenticated GET /metrics: %v", err)
	}
	defer authed.Body.Close()
	if authed.StatusCode != http.StatusOK {
		t.Fatalf("authenticated GET /metrics: got %d, want 200", authed.StatusCode)
	}

	// A path the router does not serve is still the UI, so the prompt can load.
	shell, err := http.Get(base + "/projects/ao-1")
	if err != nil {
		t.Fatalf("GET /projects/ao-1: %v", err)
	}
	defer shell.Body.Close()
	if shell.StatusCode == http.StatusUnauthorized {
		t.Fatal("GET /projects/ao-1: got 401; the UI shell must still load unauthenticated")
	}
}

// TestWebUIBypassDisabledForUnknownHandler pins that "cannot tell" means
// restrictive. The bypass identifies daemon routes by asking the router; a
// handler that cannot answer — anything but the chi router, which is what a
// future middleware wrapper around it would produce — must disable the bypass
// entirely, not skip the question.
//
// The permissive reading of the same nil is the bug this whole guard exists to
// prevent, and it would have been reintroduced silently: no panic, no failing
// test, just an unauthenticated route on a network-bound socket. The visible
// cost of failing closed is that the LAN UI stops loading, which someone reports.
func TestWebUIBypassDisabledForUnknownHandler(t *testing.T) {
	opaque := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "daemon-data")
	})
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(opaque, st, 0, slog.Default(), nil)
	port, err := m.Start(0, "127.0.0.1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	base := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Every path a UI request could take, including the shell itself.
	for _, path := range []string{"/", "/assets/index.js", "/projects/ao-1", "/api/v1/sessions"} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: got %d %q, want 401 — an unrecognized handler must disable the bypass, not skip the check",
				path, resp.StatusCode, body)
		}
		if strings.Contains(string(body), "daemon-data") {
			t.Errorf("%s: the wrapped handler ran for an unauthenticated caller", path)
		}
	}

	// The credential still works, so failing closed costs only the password.
	req, _ := http.NewRequest(http.MethodGet, base+"/", nil)
	req.Header.Set("Authorization", "Bearer secret12")
	authed, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("authenticated GET /: %v", err)
	}
	defer authed.Body.Close()
	if authed.StatusCode != http.StatusOK {
		t.Fatalf("authenticated GET /: got %d, want 200", authed.StatusCode)
	}
}

// TestWebUIBypassDoesNotExposeWorkspacePreviews is the regression test for the
// bypass routing unauthenticated requests through the shared router.
//
// previewOriginMiddleware sits in the router's middleware stack and terminates
// any GET whose Host is a preview subdomain by serving files out of that
// session's workspace. It is keyed on a base32 session id in the Host, which is
// not a secret. So "run the router and let it land in NotFound" was never the
// same thing as "serve the static bundle": the router runs its middleware
// first. Measured before the fix, on the LAN listener with no password, these
// requests reached PreviewOrigin and got its envelope back — session data on a
// socket bound to the network.
//
// Two things hold it shut now: the bypass serves the UI handler directly rather
// than the router, so no router middleware can run unauthenticated at all, and a
// preview Host is routed to authMiddleware so the preview flow keeps working
// over the LAN with the password, as it did before the bypass existed.
func TestWebUIBypassDoesNotExposeWorkspacePreviews(t *testing.T) {
	router := newTestRouter(config.Config{}, discardLogger(), nil)
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(router, st, 0, slog.Default(), nil)
	port, err := m.Start(0, "127.0.0.1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())

	label := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("ao-1")))
	previewHost := "ao-preview." + label + ".localhost"

	for _, path := range []string{"/", "/index.html", "/app.js", "/assets/main.css"} {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		req.Host = previewHost
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s with a preview Host: got %d %q, want 401 — workspace previews must not be reachable without the password",
				path, resp.StatusCode, body)
		}
		// PreviewOrigin's own envelopes are the tell that it ran at all.
		if strings.Contains(string(body), "PREVIEW_NOT_FOUND") || strings.Contains(string(body), "NO_PREVIEW_ENTRY") {
			t.Errorf("%s with a preview Host: previewOriginMiddleware ran for an unauthenticated caller (%q)", path, body)
		}
	}

	// The ordinary UI shell, on a normal Host, still loads without a password.
	shell, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer shell.Body.Close()
	if shell.StatusCode == http.StatusUnauthorized {
		t.Fatal("GET / on a normal Host: got 401; the UI shell must still load unauthenticated")
	}
}

// The version signal is only useful if a REMOTE client can actually read it, so
// this reads it back over the wire through the real LAN listener rather than
// asserting the field was set. It also pins where it must NOT be readable:
// /healthz is credential-gated on that socket, because an unauthenticated
// data-returning route there would break the hostGuard LAN exemption.
func TestBuildSignalReadableOnlyWithCredential(t *testing.T) {
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(newTestRouter(config.Config{}, discardLogger(), nil), st, 0, slog.Default(), nil)
	port, err := m.Start(0, "127.0.0.1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)

	// No credential: the signal must not be readable at all.
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /healthz: got %d want 401 — a version signal must not be an unauthenticated data route on the LAN socket", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer secret12")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("authenticated /healthz: got %d want 200", resp2.StatusCode)
	}
	var got struct {
		Build *struct {
			Identity string `json:"identity"`
			Source   string `json:"source"`
		} `json:"build"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&got); err != nil {
		t.Fatalf("decode probe payload: %v", err)
	}
	if got.Build == nil {
		t.Fatal("no build object on the wire — a remote client has no version signal at all")
	}
	switch got.Build.Source {
	case "stamp", "vcs":
		if got.Build.Identity == "" {
			t.Errorf("source %q carried an empty identity over the wire", got.Build.Source)
		}
	case "unknown":
		// The case this whole change exists for: the test binary is built from a
		// worktree with no -X stamp, so this is the DEFAULT outcome here, and it
		// must arrive as an explicit "unknown" with no identity to mis-compare.
		if got.Build.Identity != "" {
			t.Errorf("unknown build sent identity %q over the wire", got.Build.Identity)
		}
	default:
		t.Fatalf("unrecognised build source %q on the wire", got.Build.Source)
	}
	t.Logf("build signal over the wire: source=%q identity=%q", got.Build.Source, got.Build.Identity)
}
