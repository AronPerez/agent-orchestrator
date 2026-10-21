package httpd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry/policyauthority"
)

func TestForkSwitchPolicyKeepsOptOutAndLoopbackBoundary(t *testing.T) {
	dir := t.TempDir()
	const generation = "12345678-1234-4234-8234-123456789abc"
	if err := os.WriteFile(filepath.Join(dir, policyauthority.FileName), []byte(`{"schema_version":1,"events_enabled":false,"consent_generation":"`+generation+`","updated_at":"2026-10-21T12:00:00.000Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	var purgeErr error
	r := chi.NewRouter()
	mountForkAgentSwitchPolicy(r, dir, func(context.Context) error { calls++; return purgeErr })
	call := func(host, origin, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://"+host+"/internal/agent-switch-observability/"+path, strings.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	body := `{"consentGeneration":"` + generation + `","eventsEnabled":false}`
	for _, tc := range []struct{ host, origin string }{{"attacker.example", ""}, {"127.0.0.1:3001", "https://attacker.example"}} {
		if w := call(tc.host, tc.origin, "apply-policy", body); w.Code != http.StatusForbidden {
			t.Fatalf("external policy: %d %s", w.Code, w.Body.String())
		}
	}
	if calls != 0 {
		t.Fatal("external request reached cleanup")
	}
	if w := call("127.0.0.1:3001", "", "prepare-disable", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"purgeConfirmed":false`) {
		t.Fatalf("prepare: %d %s", w.Code, w.Body.String())
	}
	if w := call("127.0.0.1:3001", "", "apply-policy", strings.Replace(body, "false", "true", 1)); w.Code != http.StatusConflict {
		t.Fatalf("authority mismatch: %d %s", w.Code, w.Body.String())
	}
	if calls != 0 {
		t.Fatal("mismatched policy reached cleanup")
	}
	if w := call("127.0.0.1:3001", "", "apply-policy", body); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"purgeConfirmed":true`) || !strings.Contains(w.Body.String(), `"eventsEnabled":false`) {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	purgeErr = errors.New("storage unavailable")
	if w := call("127.0.0.1:3001", "", "apply-policy", body); w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), `"purgeConfirmed":true`) {
		t.Fatalf("false cleanup acknowledgement: %d %s", w.Code, w.Body.String())
	}
}
