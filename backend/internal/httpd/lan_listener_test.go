package httpd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

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

	base := fmt.Sprintf("http://127.0.0.1:%d/anything", port)
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
