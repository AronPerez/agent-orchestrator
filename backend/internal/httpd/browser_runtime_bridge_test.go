package httpd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
)

func bridgeTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// dialUpgrade performs the raw client half of the handshake and returns the
// conn positioned after the 101 response headers.
func dialUpgrade(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(conn,
		"GET /browser-runtime HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: Upgrade\r\nUpgrade: ao-browser-runtime\r\n\r\n")
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("expected 101, got %q", status)
	}
	for { // drain headers
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	if reader.Buffered() > 0 {
		t.Fatal("server must not send frames before the client hello")
	}
	return conn
}

func TestBrowserRuntimeBridgeRoundTrip(t *testing.T) {
	broker := browserruntime.New(bridgeTestLogger(), "local-token")
	r := chi.NewRouter()
	mountBrowserRuntimeBridge(r, broker, bridgeTestLogger())
	srv := httptest.NewServer(r)
	defer srv.Close()

	conn := dialUpgrade(t, srv.Listener.Addr().String())
	defer func() { _ = conn.Close() }()
	fmt.Fprint(conn, "{\"type\":\"hello\",\"version\":2}\n")

	deadline := time.Now().Add(5 * time.Second)
	for !broker.Status().Connected {
		if time.Now().After(deadline) {
			t.Fatal("broker never saw the upgraded conn")
		}
		time.Sleep(10 * time.Millisecond)
	}

	go func() {
		scanner := bufio.NewScanner(conn)
		if !scanner.Scan() {
			return
		}
		var msg map[string]any
		_ = json.Unmarshal(scanner.Bytes(), &msg)
		fmt.Fprintf(conn,
			"{\"type\":\"result\",\"requestId\":%q,\"ok\":true,\"result\":{\"url\":\"http://x\"}}\n",
			msg["requestId"])
	}()

	res, err := broker.Execute(context.Background(), "s1", "open",
		map[string]any{"url": "http://x"}, "")
	if err != nil {
		t.Fatalf("execute over upgraded conn: %v", err)
	}
	value, ok := res.Value.(map[string]any)
	if !ok || value["url"] != "http://x" {
		t.Fatalf("unexpected result: %#v", res.Value)
	}
}

func TestBrowserRuntimeBridgeRequiresUpgradeHeaders(t *testing.T) {
	broker := browserruntime.New(bridgeTestLogger(), "")
	r := chi.NewRouter()
	mountBrowserRuntimeBridge(r, broker, bridgeTestLogger())
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/browser-runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Fatalf("plain GET: want 426, got %d", resp.StatusCode)
	}
}

func TestBrowserRuntimeBridgeNotMountedWithoutBridge(t *testing.T) {
	r := chi.NewRouter()
	mountBrowserRuntimeBridge(r, nil, bridgeTestLogger())
	srv := httptest.NewServer(r)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/browser-runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("nil bridge: want 404, got %d", resp.StatusCode)
	}
}
