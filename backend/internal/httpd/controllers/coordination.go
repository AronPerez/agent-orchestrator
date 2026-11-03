package controllers

import (
	"context"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

type sessionCoordinationReader interface {
	Coordination(context.Context, domain.SessionID) ([]domain.CoordinationEvent, error)
}

func (c *SessionsController) coordination(w http.ResponseWriter, r *http.Request) {
	reader, ok := c.Svc.(sessionCoordinationReader)
	if !ok {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/coordination")
		return
	}
	events, err := reader.Coordination(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	more := len(events) > 50
	if more {
		events = events[:50]
	}
	if events == nil {
		events = []domain.CoordinationEvent{}
	}
	envelope.WriteJSON(w, http.StatusOK, SessionCoordinationResponse{Events: events, HasMore: more})
}
