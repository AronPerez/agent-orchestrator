package httpd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry/policyauthority"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// Fork compatibility: the frozen desktop waits for these opt-out acknowledgments.
// The old switch uploader stays retired (the desktop's production gate is off).
func mountForkAgentSwitchPolicy(r chi.Router, dataDir string, purge func(context.Context) error) {
	if purge == nil {
		return
	}
	authority := policyauthority.New(filepath.Join(dataDir, policyauthority.FileName))
	for _, operation := range []string{"prepare-disable", "apply-policy"} {
		r.Post("/internal/agent-switch-observability/"+operation, func(w http.ResponseWriter, req *http.Request) {
			if !localControlRequest(req) {
				envelope.WriteJSON(w, http.StatusForbidden, map[string]string{"status": "forbidden"})
				return
			}
			generation, purged := "", false
			if operation == "apply-policy" {
				var body struct {
					ConsentGeneration string `json:"consentGeneration"`
					EventsEnabled     bool   `json:"eventsEnabled"`
				}
				dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096))
				dec.DisallowUnknownFields()
				if err := dec.Decode(&body); err != nil || body.ConsentGeneration == "" {
					envelope.WriteJSON(w, http.StatusBadRequest, map[string]string{"status": "invalid_request"})
					return
				}
				if err := dec.Decode(&struct{}{}); err != io.EOF {
					envelope.WriteJSON(w, http.StatusBadRequest, map[string]string{"status": "invalid_request"})
					return
				}
				policy, err := authority.Read(req.Context())
				if err != nil || !policy.Present || policy.ConsentGeneration != body.ConsentGeneration || policy.EventsEnabled != body.EventsEnabled {
					envelope.WriteJSON(w, http.StatusConflict, map[string]string{"status": "authority_mismatch"})
					return
				}
				if err := purge(req.Context()); err != nil {
					envelope.WriteJSON(w, http.StatusInternalServerError, map[string]string{"status": "failed"})
					return
				}
				generation, purged = body.ConsentGeneration, true
			}
			envelope.WriteJSON(w, http.StatusOK, map[string]any{
				"status": "applied", "consentGeneration": generation, "eventsEnabled": false, "gateDrained": true, "purgeConfirmed": purged,
			})
		})
	}
}
