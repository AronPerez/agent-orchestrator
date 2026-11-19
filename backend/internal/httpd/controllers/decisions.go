package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	decisionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/decision"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

const maxDecisionRequestBytes = 64 << 10

// DecisionService is what the decisions routes need.
type DecisionService interface {
	Raise(context.Context, decisionsvc.Actor, decisionsvc.RaiseInput) (domain.DecisionCall, error)
	Get(context.Context, string) (decisionsvc.Detail, error)
	List(context.Context, decisionsvc.Actor, decisionsvc.Scope) ([]domain.DecisionCall, error)
	Board(context.Context) (decisionsvc.Board, error)
	Search(context.Context, decisionsvc.Actor, string, int) ([]domain.DecisionCall, error)
	Answer(context.Context, decisionsvc.Actor, string, decisionsvc.AnswerInput) (domain.DecisionCall, error)
	Dismiss(context.Context, decisionsvc.Actor, string, string) (domain.DecisionCall, error)
	RecordOutcome(context.Context, decisionsvc.Actor, string, decisionsvc.OutcomeInput) (domain.DecisionCall, error)
	Withdraw(context.Context, decisionsvc.Actor, string) (domain.DecisionCall, error)
	Retire(context.Context, decisionsvc.Actor, string, bool) (domain.DecisionCall, error)
	Kinds(context.Context, decisionsvc.Actor, domain.ProjectID) (decisionsvc.Policy, error)
	Observe(context.Context, decisionsvc.Actor, domain.ProjectID) (decisionsvc.ObserveReport, error)
}

// DecisionsController owns the /decisions routes.
type DecisionsController struct{ Svc DecisionService }

// Register mounts decision call routes on r.
func (c *DecisionsController) Register(r chi.Router) {
	r.Post("/decisions", c.raise)
	r.Get("/decisions", c.list)
	r.Get("/decisions/board", c.board)
	r.Get("/decisions/kinds", c.kinds)
	r.Get("/decisions/observe", c.observe)
	r.Get("/decisions/{id}", c.get)
	r.Post("/decisions/{id}/answer", c.answer)
	r.Post("/decisions/{id}/dismiss", c.dismiss)
	r.Post("/decisions/{id}/outcome", c.outcome)
	r.Post("/decisions/{id}/withdraw", c.withdraw)
	r.Post("/decisions/{id}/retire", c.retire)
}

func (c *DecisionsController) raise(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/decisions")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDecisionRequestBytes)
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
	input := decisionsvc.RaiseInput{
		Title: req.Title, Context: req.Context, Options: options, Links: links, Reference: req.Reference,
		Urgency: req.Urgency, Kind: req.Kind,
	}
	if req.Prediction != nil {
		input.Prediction = &domain.DecisionPrediction{Option: req.Prediction.Option, Why: req.Prediction.Why, Undo: req.Prediction.Undo}
	}
	call, err := c.Svc.Raise(r.Context(), decisionActor(r), input)
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
	values := r.URL.Query()
	query := strings.TrimSpace(values.Get("q"))
	_, hasQuery := values["q"]
	var (
		calls []domain.DecisionCall
		err   error
	)
	if hasQuery {
		limit, ok := decisionSearchLimit(values)
		if !ok {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "DECISION_LIMIT_INVALID",
				"limit must be a whole number of at least 1", nil)
			return
		}
		calls, err = c.Svc.Search(r.Context(), actor, query, limit)
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

// decisionSearchLimit reads an optional page size for q. Absent means the
// service default; values above the maximum are capped rather than refused.
func decisionSearchLimit(values map[string][]string) (int, bool) {
	raw, present := values["limit"]
	if !present {
		return 0, true
	}
	limit, err := strconv.Atoi(strings.TrimSpace(raw[0]))
	if err != nil || limit < 1 {
		return 0, false
	}
	return min(limit, decisionsvc.MaxDecisionSearchLimit), true
}

func (c *DecisionsController) board(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/decisions/board")
		return
	}
	board, err := c.Svc.Board(r.Context())
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "DECISION_BOARD_FAILED", "Unable to load decisions")
		return
	}
	response := DecisionBoardResponse{
		Calls:         make([]DecisionCallResponse, len(board.Calls)),
		NeedsYouCount: board.NeedsYouCount,
		ServingHostID: board.ServingHostID,
		DoneSince:     board.DoneSince,
	}
	for i := range board.Calls {
		response.Calls[i] = decisionCallResponse(board.Calls[i])
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

func (c *DecisionsController) kinds(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/decisions/kinds")
		return
	}
	project := domain.ProjectID(strings.TrimSpace(r.URL.Query().Get("project")))
	policy, err := c.Svc.Kinds(r.Context(), decisionActor(r), project)
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "DECISION_KINDS_FAILED", "Unable to list decision kinds")
		return
	}
	response := DecisionKindsResponse{
		ProjectID: string(policy.ProjectID), Mode: string(policy.Mode),
		Kinds: make([]DecisionKindResponse, len(policy.Kinds)),
	}
	for i, kind := range policy.Kinds {
		response.Kinds[i] = DecisionKindResponse{ID: string(kind.ID), Label: kind.Label, Reserved: kind.Reserved, ReservedByDefault: kind.ReservedByDefault}
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

func (c *DecisionsController) observe(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/decisions/observe")
		return
	}
	project := domain.ProjectID(strings.TrimSpace(r.URL.Query().Get("project")))
	report, err := c.Svc.Observe(r.Context(), decisionActor(r), project)
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "DECISION_OBSERVE_FAILED", "Unable to report observe mode")
		return
	}
	response := DecisionObserveResponse{
		ProjectID: string(report.ProjectID), Mode: string(report.Mode),
		Rows: make([]DecisionObserveRowResponse, len(report.Rows)),
	}
	for i, row := range report.Rows {
		response.Rows[i] = DecisionObserveRowResponse{
			Kind: string(row.Kind), Label: row.Label, Reserved: row.Reserved,
			Calls: row.Calls, Answered: row.Answered, Agreed: row.Agreed, Differed: row.Differed,
		}
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

func (c *DecisionsController) answer(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/decisions/{id}/answer")
		return
	}
	var req AnswerDecisionRequest
	if !decodeDecisionRequest(w, r, &req, false) {
		return
	}
	call, err := c.Svc.Answer(r.Context(), decisionActor(r), chi.URLParam(r, "id"), decisionsvc.AnswerInput{
		Choice: req.Choice, Option: req.Option, Note: req.Note,
	})
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "DECISION_ANSWER_FAILED", "Unable to answer decision")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, decisionCallResponse(call))
}

func (c *DecisionsController) dismiss(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/decisions/{id}/dismiss")
		return
	}
	var req DismissDecisionRequest
	if !decodeDecisionRequest(w, r, &req, true) {
		return
	}
	call, err := c.Svc.Dismiss(r.Context(), decisionActor(r), chi.URLParam(r, "id"), req.Note)
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "DECISION_DISMISS_FAILED", "Unable to dismiss decision")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, decisionCallResponse(call))
}

func (c *DecisionsController) outcome(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/decisions/{id}/outcome")
		return
	}
	var req DecisionOutcomeRequest
	if !decodeDecisionRequest(w, r, &req, false) {
		return
	}
	call, err := c.Svc.RecordOutcome(r.Context(), decisionActor(r), chi.URLParam(r, "id"), decisionsvc.OutcomeInput{
		ForEntry: req.ForEntry, Text: req.Text, Link: req.Link,
	})
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "DECISION_OUTCOME_FAILED", "Unable to record decision outcome")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, decisionCallResponse(call))
}

func (c *DecisionsController) withdraw(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/decisions/{id}/withdraw")
		return
	}
	call, err := c.Svc.Withdraw(r.Context(), decisionActor(r), chi.URLParam(r, "id"))
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "DECISION_WITHDRAW_FAILED", "Unable to withdraw decision")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, decisionCallResponse(call))
}

func (c *DecisionsController) retire(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/decisions/{id}/retire")
		return
	}
	var req RetireDecisionRequest
	if !decodeDecisionRequest(w, r, &req, false) {
		return
	}
	call, err := c.Svc.Retire(r.Context(), decisionActor(r), chi.URLParam(r, "id"), req.Retired)
	if err != nil {
		envelope.WriteErrorWithCode(w, r, err, "DECISION_RETIRE_FAILED", "Unable to retire decision")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, decisionCallResponse(call))
}

func decodeDecisionRequest(w http.ResponseWriter, r *http.Request, dst any, optional bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxDecisionRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if optional && errors.Is(err, io.EOF) {
			return true
		}
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return false
	}
	return true
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
		AnswerSurface: string(call.AnswerSurface), StandingEntryID: call.StandingEntryID,
		Outcome: call.Outcome, OutcomeLink: call.OutcomeLink,
		Retired: call.Retired, Delivery: string(call.Delivery), DeliveredTo: string(call.DeliveredTo),
		DeliveredToName: call.DeliveredToName, DeliveredFallback: call.DeliveredFallback,
		ChangedAfterClose: call.ChangedAfterClose,
		Urgency:           call.Raised.EffectiveUrgency(), Kind: string(call.Raised.Kind),
		Reserved: call.Raised.Reserved, AutonomyMode: string(call.Raised.AutonomyMode),
	}
	if kind, ok := domain.LookupDecisionKind(string(call.Raised.Kind)); ok {
		response.KindLabel = kind.Label
	}
	if prediction := call.Raised.Prediction; prediction != nil {
		response.Prediction = &DecisionPredictionPayload{Option: prediction.Option, Why: prediction.Why, Undo: prediction.Undo}
		if prediction.Option >= 1 && prediction.Option <= len(call.Raised.Options) {
			response.Prediction.Label = call.Raised.Options[prediction.Option-1].Label
		}
	}
	if agreed, known := call.PredictionAgreed(); known {
		response.PredictionAgreed = &agreed
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
			response.ForEntry = payload.ForEntry
			response.Status, response.SessionID = payload.Status, string(payload.SessionID)
			response.SessionName, response.Fallback = payload.SessionName, payload.Fallback
		}
	case domain.DecisionKindOutcome:
		var payload domain.DecisionOutcomePayload
		if json.Unmarshal(entry.Payload, &payload) == nil {
			response.ForEntry = payload.ForEntry
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
