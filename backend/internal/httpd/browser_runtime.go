package httpd

import (
	"context"
	"net"
	"net/http"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
)

type browserRuntimeBroker interface {
	ServeRemoteConn(context.Context, net.Conn)
}

// The desktop dials through its existing authenticated remote proxy. Keep this
// separate from browser/live, which streams a host's browser to a phone.
func mountBrowserRuntime(r chi.Router, broker browserRuntimeBroker) {
	if broker == nil {
		return
	}
	r.Get("/browser-runtime", func(w http.ResponseWriter, r *http.Request) {
		if !requestscope.IsLAN(r.Context()) || !authenticatedLANBearer(r) ||
			r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			notFoundJSON(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		broker.ServeRemoteConn(r.Context(), websocket.NetConn(r.Context(), conn, websocket.MessageText))
	})
}
