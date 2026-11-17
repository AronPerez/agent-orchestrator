package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	decisionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/decision"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// DecisionService is what the decisions routes need. Later slices add actions.
type DecisionService interface {
	Raise(context.Context, decisionsvc.Actor, decisionsvc.RaiseInput) (domain.DecisionCall, error)
	Get(context.Context, string) (decisionsvc.Detail, error)
	List(context.Context, decisionsvc.Actor, decisionsvc.Scope) ([]domain.DecisionCall, error)
	Search(context.Context, decisionsvc.Actor, string) ([]domain.DecisionCall, error)
}

// DecisionsController owns the /decisions routes.
type DecisionsController struct{ Svc DecisionService }

// Register mounts decision call routes on r.
func (c *DecisionsController) Register(r chi.Router) {
	r.Post("/decisions", c.raise)
	r.Get("/decisions", c.list)
	r.Get("/decisions/{id}", c.get)
}

func (c *DecisionsController) raise(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/decisions")
		return
	}
	var req RaiseDecisionRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	options := make([]domain.DecisionOption, len(req.Options))
	for i, option := range req.Options {
		options[i] = domain.DecisionOption{Label: option.Label, Consequence: option.Consequence, Recommended: option.Recommended}
	}
	links := make([]domain.DecisionLink, len(req.Links))
	for i, link := range req.Links {
		links[i] = domain.DecisionLink{Label: link.Label, URL: link.URL}
	}
	call, err := c.Svc.Raise(r.Context(), decisionActor(r), decisionsvc.RaiseInput{
		Title: req.Title, Context: req.Context, Options: options, Links: links, Reference: req.Reference,
	})
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "DECISION_RAISE_FAILED", "Unable to raise decision")
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, decisionCallResponse(call))
}

func (c *DecisionsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/decisions")
		return
	}
	actor := decisionActor(r)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	var (
		calls []domain.DecisionCall
		err   error
	)
	if query != "" {
		calls, err = c.Svc.Search(r.Context(), actor, query)
	} else {
		scope := decisionsvc.Scope(r.URL.Query().Get("scope"))
		if scope == "" {
			scope = decisionsvc.ScopeNeedsYou
		}
		calls, err = c.Svc.List(r.Context(), actor, scope)
	}
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "DECISION_LIST_FAILED", "Unable to list decisions")
		return
	}
	response := ListDecisionsResponse{Calls: make([]DecisionCallResponse, len(calls))}
	for i := range calls {
		response.Calls[i] = decisionCallResponse(calls[i])
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

func (c *DecisionsController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/decisions/{id}")
		return
	}
	detail, err := c.Svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "DECISION_GET_FAILED", "Unable to get decision")
		return
	}
	response := DecisionDetailResponse{
		Call:    decisionCallResponse(detail.Call),
		History: make([]DecisionEntryResponse, len(detail.Entries)),
	}
	for i := range detail.Entries {
		response.History[i] = decisionEntryResponse(detail.Entries[i])
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

func decisionActor(r *http.Request) decisionsvc.Actor {
	if session := caller.From(r.Context()); session != "" {
		return decisionsvc.Actor{Session: domain.SessionID(session)}
	}
	surface, _ := telemetrymeta.SurfaceFromContext(r.Context())
	return decisionsvc.Actor{Surface: surface}
}

func decisionCallResponse(call domain.DecisionCall) DecisionCallResponse {
	options := make([]DecisionOptionPayload, len(call.Raised.Options))
	for i, option := range call.Raised.Options {
		options[i] = DecisionOptionPayload{Label: option.Label, Consequence: option.Consequence, Recommended: option.Recommended}
	}
	links := make([]DecisionLinkPayload, len(call.Raised.Links))
	for i, link := range call.Raised.Links {
		links[i] = DecisionLinkPayload{Label: link.Label, URL: link.URL}
	}
	response := DecisionCallResponse{
		ID: call.ID, Link: domain.DecisionCallURL(call.ID), State: string(call.State), StateLabel: call.State.Label(),
		Title: call.Raised.Title, Context: call.Raised.Context, Options: options, Links: links, Reference: call.Raised.Reference,
		ProjectID: string(call.Raised.ProjectID), ProjectName: call.Raised.ProjectName,
		StreamSessionID: string(call.Raised.StreamSessionID), StreamName: call.Raised.StreamName,
		HostID: call.Origin, MachineLabel: call.Raised.MachineLabel,
		OwnerSessionID: string(call.Raised.OwnerSessionID), OwnerSessionName: call.Raised.OwnerSessionName,
		RaisedAt: call.RaisedAt, Choice: call.Choice, Option: call.Option, ChosenLabel: call.ChosenLabel(), Note: call.Note,
		AnswerSurface: string(call.AnswerSurface), Outcome: call.Outcome, OutcomeLink: call.OutcomeLink,
		Retired: call.Retired, Delivery: string(call.Delivery), DeliveredTo: string(call.DeliveredTo),
		DeliveredToName: call.DeliveredToName, DeliveredFallback: call.DeliveredFallback,
		ChangedAfterClose: call.ChangedAfterClose,
	}
	if !call.AnsweredAt.IsZero() {
		answeredAt := call.AnsweredAt
		response.AnsweredAt = &answeredAt
	}
	if !call.OutcomeAt.IsZero() {
		outcomeAt := call.OutcomeAt
		response.OutcomeAt = &outcomeAt
	}
	return response
}

func decisionEntryResponse(entry domain.DecisionEntry) DecisionEntryResponse {
	response := DecisionEntryResponse{
		ID: entry.ID, Kind: string(entry.Kind), WrittenAt: entry.WrittenAt, HostID: entry.Origin,
		AuthorKind: string(entry.AuthorKind), AuthorSurface: string(entry.AuthorSurface), AuthorSession: string(entry.AuthorSession),
	}
	switch entry.Kind {
	case domain.DecisionKindAnswered:
		var payload domain.DecisionAnsweredPayload
		if json.Unmarshal(entry.Payload, &payload) == nil {
			response.Choice, response.Option, response.Note = payload.Choice, payload.Option, payload.Note
		}
	case domain.DecisionKindDismissed:
		var payload domain.DecisionDismissedPayload
		if json.Unmarshal(entry.Payload, &payload) == nil {
			response.Note = payload.Note
		}
	case domain.DecisionKindDelivered:
		var payload domain.DecisionDeliveredPayload
		if json.Unmarshal(entry.Payload, &payload) == nil {
			response.Status, response.SessionID = payload.Status, string(payload.SessionID)
			response.SessionName, response.Fallback = payload.SessionName, payload.Fallback
		}
	case domain.DecisionKindOutcome:
		var payload domain.DecisionOutcomePayload
		if json.Unmarshal(entry.Payload, &payload) == nil {
			response.Text, response.Link = payload.Text, payload.Link
		}
	case domain.DecisionKindRetired:
		var payload domain.DecisionRetiredPayload
		if json.Unmarshal(entry.Payload, &payload) == nil {
			response.Retired = &payload.Retired
		}
	}
	return response
}
