package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

type unpairTestRegistry struct{ unpaired []string }

func (*unpairTestRegistry) Upsert(mobilebridge.PushDevice) error { return nil }
func (*unpairTestRegistry) UnregisterToken(string) error         { return nil }
func (r *unpairTestRegistry) Unpair(id string) error {
	r.unpaired = append(r.unpaired, id)
	return nil
}

type failingRequestDeviceRevoker struct{ err error }

func (f failingRequestDeviceRevoker) RevokeDeviceFromRequest(context.Context, string) (string, bool, error) {
	return "install-phone", false, f.err
}

func TestUnpairKeepsPushRegistrationWhenCredentialRevocationFails(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "listener shutdown canceled the request", err: context.Canceled},
		{name: "credential registry could not persist", err: errors.New("persist access devices")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := &unpairTestRegistry{}
			controller := &PushController{Registry: registry, Access: failingRequestDeviceRevoker{err: tc.err}}
			router := chi.NewRouter()
			controller.Register(router)
			req := httptest.NewRequest(http.MethodDelete, "/push/pairings/install-phone", nil)
			req = req.WithContext(requestscope.WithCredential(req.Context(), requestscope.Credential{
				Kind: requestscope.CredentialDevice, DeviceID: "device-phone",
			}))
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, req)

			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
			}
			if len(registry.unpaired) != 0 {
				t.Fatalf("push registration was removed before credential revocation: %v", registry.unpaired)
			}
			var body envelope.APIError
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != "DEVICE_UNPAIR_RETRY" {
				t.Fatalf("code = %q, want DEVICE_UNPAIR_RETRY", body.Code)
			}
		})
	}
}
