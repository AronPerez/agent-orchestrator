package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

func TestRecoverTelemetryEmitsPanicEvent(t *testing.T) {
	sink := &captureSink{}
	r := NewRouterWithControl(config.Config{}, discardLogger(), nil, APIDeps{Telemetry: sink}, ControlDeps{})
	r.Get("/panic", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/panic", nil)
	req.Host = "127.0.0.1:3001"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var panicPayload, fiveXXPayload map[string]any
	for _, ev := range sink.events {
		switch ev.Name {
		case "ao.daemon.panic":
			panicPayload = ev.Payload
		case "ao.http.5xx":
			fiveXXPayload = ev.Payload
		}
	}
	if panicPayload == nil {
		t.Fatalf("events = %#v, want ao.daemon.panic", sink.events)
	}
	if fiveXXPayload == nil {
		t.Fatalf("events = %#v, want ao.http.5xx after recovery", sink.events)
	}
	if got := panicPayload["component"]; got != "httpd" {
		t.Fatalf("panic payload.component = %#v, want httpd", got)
	}
	if got := panicPayload["operation"]; got != "http_request_panic" {
		t.Fatalf("panic payload.operation = %#v, want http_request_panic", got)
	}
	if got := panicPayload["path"]; got != "/panic" {
		t.Fatalf("panic payload.path = %#v, want /panic", got)
	}
	if got := panicPayload["panic_kind"]; got != "string" {
		t.Fatalf("panic payload.panic_kind = %#v, want string", got)
	}
	if got := panicPayload["fingerprint"]; got == "" {
		t.Fatalf("panic payload.fingerprint = %#v, want non-empty", got)
	}
	if got := panicPayload["stack_fingerprint"]; got == "" {
		t.Fatalf("panic payload.stack_fingerprint = %#v, want non-empty", got)
	}
	if got := fiveXXPayload["path"]; got != "/panic" {
		t.Fatalf("5xx payload.path = %#v, want /panic", got)
	}
	if got := fiveXXPayload["status_family"]; got != "5xx" {
		t.Fatalf("5xx payload.status_family = %#v, want 5xx", got)
	}
}

func TestTelemetryControlRoutesRemoved(t *testing.T) {
	r := NewRouterWithControl(config.Config{}, discardLogger(), nil, APIDeps{Telemetry: &captureSink{}}, ControlDeps{})
	for _, path := range []string{"/internal/telemetry/cli-invoked", "/internal/telemetry/cli-usage-error", "/internal/agent-switch-observability/prepare-disable", "/internal/agent-switch-observability/apply-policy"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+path, nil))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
		})
	}
}
