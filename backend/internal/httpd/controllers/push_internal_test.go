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

type unpairTestRegistry struct {
	unpaired []string
	err      error
}

func (*unpairTestRegistry) Upsert(mobilebridge.PushDevice) error { return nil }
func (*unpairTestRegistry) UnregisterToken(string) error         { return nil }
func (r *unpairTestRegistry) Unpair(id string) error {
	if r.err != nil {
		return r.err
	}
	r.unpaired = append(r.unpaired, id)
	return nil
}

type failingRequestDeviceRevoker struct{ err error }

func (f failingRequestDeviceRevoker) RevokeDeviceFromRequest(context.Context, string) (string, bool, error) {
	return "install-phone", false, f.err
}

type recordingRequestDeviceRevoker struct{ calls int }

func (r *recordingRequestDeviceRevoker) RevokeDeviceFromRequest(context.Context, string) (string, bool, error) {
	r.calls++
	return "install-phone", true, nil
}

func TestUnpairDoesNotRevokeCredentialBeforePushRemovalPersists(t *testing.T) {
	registry := &unpairTestRegistry{err: errors.New("persist push registry")}
	revoker := &recordingRequestDeviceRevoker{}
	controller := &PushController{Registry: registry, Access: revoker}
	router := chi.NewRouter()
	controller.Register(router)
	req := httptest.NewRequest(http.MethodDelete, "/push/pairings/install-phone", nil)
	req = req.WithContext(requestscope.WithCredential(req.Context(), requestscope.Credential{
		Kind: requestscope.CredentialDevice, DeviceID: "device-phone",
	}))
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
	if revoker.calls != 0 {
		t.Fatalf("credential revocations = %d, want 0 while push row remains", revoker.calls)
	}
}

func TestUnpairRemainsRetryableWhenCredentialRevocationFails(t *testing.T) {
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
			if len(registry.unpaired) != 1 || registry.unpaired[0] != "install-phone" {
				t.Fatalf("push removals = %v, want idempotent removal before revocation", registry.unpaired)
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
