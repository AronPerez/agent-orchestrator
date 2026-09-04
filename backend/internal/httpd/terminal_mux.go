package httpd

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

// terminalMuxReadLimit caps a single inbound frame. Client→server frames are small
// (keystrokes, resize, control), so a generous 1 MiB is ample headroom while
// still bounding memory per message.
const terminalMuxReadLimit = 1 << 20

// terminalMuxPath is where the mux WebSocket is mounted. corsMiddleware names it
// too: /mux hands out live terminals, so it is origin-checked like a
// state-changing route rather than like a read (see requiresStrictOrigin).
const terminalMuxPath = "/mux"

// mountTerminalMux registers the long-lived terminal-multiplexing WebSocket at /mux. It
// is intentionally outside the per-request Timeout middleware (the connection is
// long-lived). When mgr is nil the route is not mounted — the daemon simply has
// no terminal surface yet.
func mountTerminalMux(r chi.Router, mgr *terminal.Manager, log *slog.Logger) {
	if mgr == nil {
		return
	}
	r.Get(terminalMuxPath, terminalMuxHandler(mgr, log))
}

// muxAuthSubprotocol is the marker the server negotiates and echoes so a
// browser client can request subprotocols at all: the web client sends
// ["ao.auth", "ao.bearer.<pw>"] to carry the connection token (see
// wsProtocolPrefix in auth.go), and browsers fail the handshake unless the
// server echoes one requested entry. Only this marker is in the negotiable
// list, so the credential entry can never be reflected back. Clients that
// request no subprotocols (the phone, the desktop renderer) get no echo and
// are unaffected.
const muxAuthSubprotocol = "ao.auth"

// terminalMuxHandler upgrades the request to a WebSocket and hands the connection to the
// terminal manager. httpd owns only the upgrade and the transport adaptation;
// all stream logic lives in internal/terminal.
func terminalMuxHandler(mgr *terminal.Manager, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// InsecureSkipVerify disables coder/websocket's own same-origin check
		// because it is too blunt for this daemon: the packaged renderer's
		// app://renderer origin and the mobile client's pinned http://localhost
		// both fail it, and it has no notion of the connection password. Origin
		// policy is enforced upstream instead, uniformly for /mux and every
		// state-changing route, by corsMiddleware (requiresStrictOrigin) on the
		// loopback listener and by authMiddleware's cookie rule on the LAN
		// listener. /mux is mounted only on routers carrying that middleware.
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
			Subprotocols:       []string{muxAuthSubprotocol},
		})
		if err != nil {
			log.Warn("terminal mux: websocket upgrade failed", "err", err)
			return
		}
		c.SetReadLimit(terminalMuxReadLimit)
		mgr.Serve(r.Context(), &terminalMuxConn{c: c})
	}
}

// terminalMuxConn adapts a coder/websocket connection to terminal.wsConn. JSON framing
// uses wsjson (text messages); Ping is a control frame; Close sends a normal
// closure.
type terminalMuxConn struct{ c *websocket.Conn }

func (a *terminalMuxConn) ReadJSON(ctx context.Context, v any) error { return wsjson.Read(ctx, a.c, v) }
func (a *terminalMuxConn) WriteJSON(ctx context.Context, v any) error {
	return wsjson.Write(ctx, a.c, v)
}
func (a *terminalMuxConn) Ping(ctx context.Context) error { return a.c.Ping(ctx) }
func (a *terminalMuxConn) Close(reason string) error {
	return a.c.Close(websocket.StatusNormalClosure, reason)
}
