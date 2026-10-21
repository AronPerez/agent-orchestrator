package controllers

// Fork compatibility: frozen develop UI contract over upstream provider switching.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// AgentSwitchIDParam names a retained legacy switch receipt.
type AgentSwitchIDParam struct {
	SwitchID string `path:"switchId"`
}

// SwitchAgentRequest preserves the frozen UI request shape.
type SwitchAgentRequest struct {
	TargetHarness  domain.AgentHarness `json:"targetHarness" enum:"claude-code,codex,fx" description:"Agent harness to continue the logical AO session with."`
	Model          string              `json:"model,omitempty" maxLength:"256" description:"Optional model override for the target agent launch or resume."`
	IdempotencyKey string              `json:"idempotencyKey,omitempty" maxLength:"128" description:"Optional retry key. Reusing it with a different request is rejected."`
}

// AgentSwitchView is the deliberately small public projection of a durable
// switch receipt. Provider context, local artifact paths, retry keys, generation
// fences, and raw failure details remain daemon-private.
type AgentSwitchView struct {
	ID                      string              `json:"id"`
	SessionID               domain.SessionID    `json:"sessionId"`
	FromHarness             domain.AgentHarness `json:"fromHarness"`
	TargetHarness           domain.AgentHarness `json:"targetHarness"`
	TargetStartMode         string              `json:"targetStartMode,omitempty" enum:"fresh,resumed"`
	State                   string              `json:"state" enum:"preparing_handoff,stopping_source,source_stopped,starting_target,target_ready,delivering_context,completed,failed"`
	AgentHandoffStatus      string              `json:"agentHandoffStatus" enum:"not_attempted,requested,received,unavailable,timed_out,failed,rejected"`
	SemanticHandoffIncluded bool                `json:"semanticHandoffIncluded"`
	SourceTranscriptStatus  string              `json:"sourceTranscriptStatus,omitempty" enum:"not_attempted,available,unavailable"`
	ErrorCode               string              `json:"errorCode,omitempty" enum:"daemon_restart_pre_stop,daemon_restart_post_stop,daemon_restart_unrecoverable_target,daemon_restart_before_delivery,delivery_unconfirmed,source_session_terminated,source_stop_unconfirmed,target_binary_missing,target_agent_unauthorized,target_start_unconfirmed,source_restore_unconfirmed,request_cancelled,source_blocked,failed_pre_stop,failed_post_stop,target_ready_failed,delivery_failed,switch_failed"`
	RequestedAt             time.Time           `json:"requestedAt"`
	UpdatedAt               time.Time           `json:"updatedAt"`
}

// AgentSwitchResponse is the body returned by switch creation, reads, and
// generation-fenced handoff submission.
type AgentSwitchResponse struct {
	Switch AgentSwitchView `json:"switch"`
}

// ListAgentSwitchesResponse is the body of
// GET /api/v1/sessions/{sessionId}/agent-switches.
type ListAgentSwitchesResponse struct {
	Switches []AgentSwitchView `json:"switches"`
}

// SubmitAgentHandoffRequest is the body of
// POST /api/v1/sessions/{sessionId}/agent-switches/{switchId}/handoff.
// Handoff remains provider-neutral JSON and is accepted only from the source
// generation recorded by the durable switch.
type SubmitAgentHandoffRequest struct {
	SourceGenerationID string `json:"sourceGenerationId" description:"Source invocation generation that authored this handoff."`
	// RawMessage deliberately preserves the source object's original token
	// stream. Decoding into a map here would silently collapse duplicate keys
	// before the semantic validator can reject them.
	Handoff json.RawMessage `json:"handoff" description:"Structured, source-agent-authored handoff enrichment."`
}

type forkAgentSwitcher interface {
	ForkSwitchAgent(context.Context, domain.SessionID, domain.ProviderSwitchRequest) (domain.AgentSwitch, error)
	ForkListAgentSwitches(context.Context, domain.SessionID) ([]domain.AgentSwitch, error)
	ForkRecoverAgentSwitch(context.Context, domain.SessionID, string) (domain.AgentSwitch, error)
}

func (c *ConversationsController) forkAgentSwitchRoutes(r chi.Router) {
	r.Post("/sessions/{sessionId}/switch-agent", c.forkSwitchAgent)
	r.Get("/sessions/{sessionId}/agent-switches", c.forkListAgentSwitches)
	r.Post("/sessions/{sessionId}/agent-switches/{switchId}/recover", c.forkRecoverAgentSwitch)
	r.Post("/sessions/{sessionId}/agent-switches/{switchId}/handoff", c.forkSubmitAgentHandoff)
}

func (c *ConversationsController) forkSwitcher(w http.ResponseWriter, r *http.Request) forkAgentSwitcher {
	svc, ok := c.Svc.(forkAgentSwitcher)
	if !ok {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return nil
	}
	return svc
}

func (c *ConversationsController) forkSwitchAgent(w http.ResponseWriter, r *http.Request) {
	svc := c.forkSwitcher(w, r)
	if svc == nil {
		return
	}
	var in SwitchAgentRequest
	if err := decodeForkSwitchBody(w, r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	in.TargetHarness = domain.AgentHarness(strings.TrimSpace(string(in.TargetHarness)))
	in.Model = strings.TrimSpace(domain.SanitizeControlChars(in.Model))
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	code, message := "", ""
	switch {
	case in.TargetHarness == "":
		code, message = "TARGET_HARNESS_REQUIRED", "targetHarness is required"
	case utf8.RuneCountInString(in.Model) > 256:
		code, message = "MODEL_TOO_LONG", "Model must be 256 characters or fewer"
	case len(in.IdempotencyKey) > 128:
		code, message = "IDEMPOTENCY_KEY_TOO_LONG", "idempotencyKey is too long"
	}
	if code != "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", code, message, nil)
		return
	}
	ctx, cancel := withoutClientCancel(r.Context())
	defer cancel()
	sw, err := svc.ForkSwitchAgent(ctx, sessionID(r), domain.ProviderSwitchRequest{TargetHarness: in.TargetHarness, Model: in.Model, IdempotencyKey: in.IdempotencyKey})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, AgentSwitchResponse{Switch: agentSwitchView(sw)})
}

func (c *ConversationsController) forkListAgentSwitches(w http.ResponseWriter, r *http.Request) {
	svc := c.forkSwitcher(w, r)
	if svc == nil {
		return
	}
	rows, err := svc.ForkListAgentSwitches(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]AgentSwitchView, 0, len(rows))
	for _, row := range rows {
		out = append(out, agentSwitchView(row))
	}
	envelope.WriteJSON(w, http.StatusOK, ListAgentSwitchesResponse{Switches: out})
}

func (c *ConversationsController) forkRecoverAgentSwitch(w http.ResponseWriter, r *http.Request) {
	svc := c.forkSwitcher(w, r)
	if svc == nil {
		return
	}
	ctx, cancel := withoutClientCancel(r.Context())
	defer cancel()
	sw, err := svc.ForkRecoverAgentSwitch(ctx, sessionID(r), chi.URLParam(r, "switchId"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, AgentSwitchResponse{Switch: agentSwitchView(sw)})
}

func (c *ConversationsController) forkSubmitAgentHandoff(w http.ResponseWriter, r *http.Request) {
	svc := c.forkSwitcher(w, r)
	if svc == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 65*1024)
	var in SubmitAgentHandoffRequest
	if err := decodeForkSwitchBody(w, r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	rows, err := svc.ForkListAgentSwitches(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	for _, sw := range rows {
		if sw.ID != chi.URLParam(r, "switchId") {
			continue
		}
		// New switching builds its own canonical note. Never accept text from an
		// old process as proof of delivery or as a trusted native-history checkpoint.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "AGENT_HANDOFF_NOT_REQUESTED", "Provider switching does not request source-authored handoffs", nil)
		return
	}
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "AGENT_SWITCH_NOT_FOUND", "Unknown agent switch", nil)
}

func agentSwitchView(s domain.AgentSwitch) AgentSwitchView {
	return AgentSwitchView{
		ID:                      s.ID,
		SessionID:               s.SessionID,
		FromHarness:             s.FromHarness,
		TargetHarness:           s.TargetHarness,
		TargetStartMode:         s.TargetStartMode,
		State:                   s.State,
		AgentHandoffStatus:      s.AgentHandoffStatus,
		SemanticHandoffIncluded: s.SemanticHandoffIncluded,
		SourceTranscriptStatus:  s.SourceTranscriptStatus,
		ErrorCode:               s.ErrorCode,
		RequestedAt:             s.RequestedAt,
		UpdatedAt:               s.UpdatedAt,
	}
}

func decodeForkSwitchBody(w http.ResponseWriter, r *http.Request, into any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65*1024))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}
