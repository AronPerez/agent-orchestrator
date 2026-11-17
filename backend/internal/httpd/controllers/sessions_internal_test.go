package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

func TestCoordinationSpawnErrorPreservesOriginalDiagnostic(t *testing.T) {
	const privateDiagnostic = "/private/project/worktrees/other-session"
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			name:   "setup conflict",
			err:    apierr.Conflict("WORKSPACE_PROVISION_FAILED", "setup failed at "+privateDiagnostic, nil),
			status: http.StatusConflict,
			code:   "WORKSPACE_PROVISION_FAILED",
		},
		{
			name:   "spawn internal failure",
			err:    apierr.InternalCause("SPAWN_CREATE_FAILED", "create failed at "+privateDiagnostic, errors.New("private cause")),
			status: http.StatusInternalServerError,
			code:   "SPAWN_CREATE_FAILED",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", nil)
			req = req.WithContext(requestscope.WithCredential(req.Context(), requestscope.Credential{
				Kind: requestscope.CredentialDevice, Scopes: []string{mobilebridge.DeviceScopeAgentCoordination},
			}))
			req, captured := envelope.WithErrorCapture(req)
			recorder := httptest.NewRecorder()

			writeSpawnError(recorder, req, tc.err)

			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.status)
			}
			var body envelope.APIError
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tc.code || body.Message != coordinationSpawnFailureMessage || strings.Contains(recorder.Body.String(), privateDiagnostic) {
				t.Fatalf("response = %+v, body=%s", body, recorder.Body.String())
			}
			if !errors.Is(captured().Err, tc.err) {
				t.Fatalf("captured error = %v, want original %v", captured().Err, tc.err)
			}
		})
	}
}
