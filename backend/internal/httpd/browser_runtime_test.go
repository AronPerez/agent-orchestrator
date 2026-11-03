package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

func TestBrowserRuntimeRequiresLANBearer(t *testing.T) {
	broker := browserruntime.New(nil, "private-launch-token")
	router := chi.NewRouter()
	mountBrowserRuntime(router, broker)
	auth := &authState{}
	auth.setHash(mobilebridge.HashPassword("paired-password"))
	lan := lanControlBlock(authMiddleware(auth, newLockout(time.Now), nil)(router))
	for _, tc := range []struct {
		name    string
		handler http.Handler
		header  http.Header
		status  int
	}{
		{"loopback", router, nil, http.StatusNotFound},
		{"missing bearer", lan, nil, http.StatusUnauthorized},
		{"preview cookie", lan, http.Header{"Cookie": {"ao_conn=paired-password"}}, http.StatusUnauthorized},
		{"browser origin", lan, http.Header{"Authorization": {"Bearer paired-password"}, "Origin": {"https://page.example"}}, http.StatusNotFound},
		{"fetch metadata", lan, http.Header{"Authorization": {"Bearer paired-password"}, "Sec-Fetch-Site": {"cross-site"}}, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/browser-runtime", nil)
			req.Header = tc.header
			res := httptest.NewRecorder()
			tc.handler.ServeHTTP(res, req)
			if res.Code != tc.status {
				t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
			}
			if broker.Status().Connected {
				t.Fatal("unauthorized runtime connected")
			}
		})
	}
}

func TestBrowserRuntimeRemoteRoundTrip(t *testing.T) {
	broker := browserruntime.New(nil, "private-launch-token")
	router := chi.NewRouter()
	mountBrowserRuntime(router, broker)
	auth := &authState{}
	auth.setHash(mobilebridge.HashPassword("paired-password"))
	server := httptest.NewServer(lanControlBlock(authMiddleware(auth, newLockout(time.Now), nil)(router)))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/browser-runtime", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer paired-password"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	conn := websocket.NetConn(ctx, ws, websocket.MessageText)
	enc, dec := json.NewEncoder(conn), json.NewDecoder(conn)
	if err := enc.Encode(map[string]any{"type": "hello", "version": 2}); err != nil {
		t.Fatal(err)
	}
	var hello map[string]any
	if err := dec.Decode(&hello); err != nil {
		t.Fatal(err)
	}
	if hello["type"] != "hello-ok" {
		t.Fatalf("hello: %v", hello)
	}
	result := make(chan error, 1)
	go func() {
		got, err := broker.Execute(ctx, "remote-session", "snapshot", nil)
		if err == nil && got.Value.(map[string]any)["text"] != "remote page" {
			err = context.Canceled
		}
		result <- err
	}()
	var command map[string]any
	if err := dec.Decode(&command); err != nil {
		t.Fatal(err)
	}
	if command["sessionId"] != "remote-session" || command["action"] != "snapshot" {
		t.Fatalf("command: %v", command)
	}
	if err := enc.Encode(map[string]any{"type": "result", "requestId": command["requestId"], "ok": true, "result": map[string]string{"text": "remote page"}}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
