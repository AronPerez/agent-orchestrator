package httpd

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// browserRuntimePath is where the desktop app's browser runtime attaches for
// REMOTE daemons. Mounted at root like /mux: it is a long-lived upgraded
// connection, deliberately outside the /api/v1 OpenAPI surface and the
// per-request timeout middleware. On the LAN listener it sits behind
// authMiddleware (the app's per-host proxy injects the connection password);
// on the loopback listener it is ambient authority like every other loopback
// route — a local process that could abuse it already owns the daemon.
const browserRuntimePath = "/browser-runtime"

// browserRuntimeUpgradeProtocol names the protocol in the Upgrade header. The
// wire protocol and its version live in the hello frame (browserruntime
// ProtocolVersion), not here.
const browserRuntimeUpgradeProtocol = "ao-browser-runtime"

// BrowserRuntimeBridge adopts an authenticated, upgraded connection as the
// daemon's Electron browser runtime. Implemented by *browserruntime.Broker.
type BrowserRuntimeBridge interface {
	ServeRemoteConn(ctx context.Context, conn net.Conn)
}

// mountBrowserRuntimeBridge registers the upgrade endpoint. A nil bridge means
// the daemon has no browser broker; the route is simply absent.
func mountBrowserRuntimeBridge(r chi.Router, bridge BrowserRuntimeBridge, log *slog.Logger) {
	if bridge == nil {
		return
	}
	r.Get(browserRuntimePath, browserRuntimeHandler(bridge, log))
}

// hijackedConn lets the broker's scanner see bytes the server's request reader
// buffered past the handshake before handing us the raw connection.
type hijackedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c hijackedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func browserRuntimeHandler(bridge BrowserRuntimeBridge, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), browserRuntimeUpgradeProtocol) ||
			!headerListContains(r.Header.Values("Connection"), "upgrade") {
			envelope.WriteAPIError(w, r, http.StatusUpgradeRequired, "upgrade_required",
				"BROWSER_RUNTIME_UPGRADE_REQUIRED",
				"This endpoint speaks the ao-browser-runtime upgrade protocol", nil)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal",
				"BROWSER_RUNTIME_HIJACK_UNSUPPORTED",
				"Connection cannot be upgraded on this listener", nil)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			log.Warn("browser runtime bridge: hijack failed", "err", err)
			return
		}
		if _, err := rw.WriteString(
			"HTTP/1.1 101 Switching Protocols\r\nUpgrade: " + browserRuntimeUpgradeProtocol +
				"\r\nConnection: Upgrade\r\n\r\n"); err != nil {
			_ = conn.Close()
			return
		}
		if err := rw.Flush(); err != nil {
			_ = conn.Close()
			return
		}
		// Blocks for the life of the runtime connection, exactly like the
		// terminal mux handler. The broker closes the conn on ctx cancellation
		// or replacement; process exit covers daemon shutdown.
		bridge.ServeRemoteConn(r.Context(), hijackedConn{Conn: conn, reader: rw.Reader})
	}
}

// headerListContains reports whether any comma-separated element of the given
// header values equals token, case-insensitively ("keep-alive, Upgrade").
func headerListContains(values []string, token string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}
