package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	decisionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/decision"
)

const raiseDecisionBody = `{"title":"Which auth scheme should the proxy use?","context":"The connector calls the proxy for each tenant.","options":[{"label":"OAuth client credentials","consequence":"One app registration per tenant.","recommended":true},{"label":"Per-tenant API keys","consequence":"Customers rotate keys by hand."}]}`

type fakeDecisionService struct {
	actor    decisionsvc.Actor
	raise    decisionsvc.RaiseInput
	scope    decisionsvc.Scope
	query    string
	limit    int
	searches int
	boards   int
	board    decisionsvc.Board
	id       string
	action   string
	answer   decisionsvc.AnswerInput
	note     string
	outcome  decisionsvc.OutcomeInput
	retired  bool
	project  domain.ProjectID
	call     *domain.DecisionCall
	decide   decisionsvc.DecideInput
	err      error
}

func (f *fakeDecisionService) Decide(_ context.Context, actor decisionsvc.Actor, input decisionsvc.DecideInput) (domain.DecisionCall, error) {
	f.actor, f.decide = actor, input
	if f.err != nil {
		return domain.DecisionCall{}, f.err
	}
	return decidedSampleCall(), nil
}

func (f *fakeDecisionService) CarriedOut(_ context.Context, actor decisionsvc.Actor, id string) (domain.DecisionCall, error) {
	f.actor, f.id, f.action = actor, id, "carried-out"
	call := decidedSampleCall()
	call.Decided.CarriedOut = true
	return call, f.err
}

func (f *fakeDecisionService) Overrule(_ context.Context, actor decisionsvc.Actor, id, note string) (domain.DecisionCall, error) {
	f.actor, f.id, f.action, f.note = actor, id, "overrule", note
	return f.actionResult()
}

func decidedSampleCall() domain.DecisionCall {
	return domain.DecisionCall{
		ID: "dec_2", Origin: "h_a", State: domain.DecisionStateDecided,
		Raised: domain.DecisionRaisedPayload{
			Title: "Closed the duplicate pull request #12", Context: "It repeats #11.", Kind: "pr_hygiene",
			AutonomyMode: domain.AutonomyOn, MachineLabel: "mac-mini", OwnerSessionID: "w-1", OwnerSessionName: "Worker one",
		},
		Decided: &domain.DecisionDecidedDetail{Key: "close-pr-12", Undo: "Reopen #12.", Verify: "One open PR remains."},
	}
}

func (f *fakeDecisionService) Observe(_ context.Context, actor decisionsvc.Actor, project domain.ProjectID) (decisionsvc.ObserveReport, error) {
	f.actor, f.project = actor, project
	if f.err != nil {
		return decisionsvc.ObserveReport{}, f.err
	}
	return decisionsvc.ObserveReport{ProjectID: "shop", Mode: domain.AutonomyObserve, Rows: []decisionsvc.ObserveRow{
		{Kind: "pr_hygiene", Label: "Pull request hygiene", Calls: 4, Answered: 3, Agreed: 1, Differed: 2},
	}}, nil
}

func (f *fakeDecisionService) Kinds(_ context.Context, actor decisionsvc.Actor, project domain.ProjectID) (decisionsvc.Policy, error) {
	f.actor, f.project = actor, project
	if f.err != nil {
		return decisionsvc.Policy{}, f.err
	}
	return decisionsvc.Policy{ProjectID: "shop", Mode: domain.AutonomyObserve, Kinds: []decisionsvc.KindPolicy{
		{ID: "production", Label: "Production changes and deploys", Reserved: true, ReservedByDefault: true},
		{ID: "pr_hygiene", Label: "Pull request hygiene"},
	}}, nil
}

func (f *fakeDecisionService) Raise(_ context.Context, actor decisionsvc.Actor, input decisionsvc.RaiseInput) (domain.DecisionCall, error) {
	f.actor, f.raise = actor, input
	if f.err != nil {
		return domain.DecisionCall{}, f.err
	}
	if f.call != nil {
		return *f.call, nil
	}
	return sampleDecisionCall(), nil
}

func (f *fakeDecisionService) Get(_ context.Context, id string) (decisionsvc.Detail, error) {
	f.id = id
	if f.err != nil {
		return decisionsvc.Detail{}, f.err
	}
	return decisionsvc.Detail{Call: sampleDecisionCall(), Entries: []domain.DecisionEntry{{
		ID: "dent_1", Origin: "h_a", CallID: "dec_1", Kind: domain.DecisionKindRaised,
		Payload: json.RawMessage(raiseDecisionBody), WrittenAt: time.Date(2026, 11, 16, 15, 0, 0, 0, time.UTC),
		AuthorKind: domain.DecisionAuthorSession, AuthorSession: "w-1",
	}}}, nil
}

func (f *fakeDecisionService) List(_ context.Context, actor decisionsvc.Actor, scope decisionsvc.Scope) ([]domain.DecisionCall, error) {
	f.actor, f.scope = actor, scope
	if f.err != nil {
		return nil, f.err
	}
	return []domain.DecisionCall{sampleDecisionCall()}, nil
}

func (f *fakeDecisionService) Board(_ context.Context) (decisionsvc.Board, error) {
	f.boards++
	if f.err != nil {
		return decisionsvc.Board{}, f.err
	}
	return f.board, nil
}

func (f *fakeDecisionService) Search(_ context.Context, actor decisionsvc.Actor, query string, limit int) ([]domain.DecisionCall, error) {
	f.actor, f.query, f.limit = actor, query, limit
	f.searches++
	if f.err != nil {
		return nil, f.err
	}
	return []domain.DecisionCall{sampleDecisionCall()}, nil
}

func (f *fakeDecisionService) Answer(_ context.Context, actor decisionsvc.Actor, id string, input decisionsvc.AnswerInput) (domain.DecisionCall, error) {
	f.actor, f.id, f.action, f.answer = actor, id, "answer", input
	return f.actionResult()
}

func (f *fakeDecisionService) Dismiss(_ context.Context, actor decisionsvc.Actor, id, note string) (domain.DecisionCall, error) {
	f.actor, f.id, f.action, f.note = actor, id, "dismiss", note
	return f.actionResult()
}

func (f *fakeDecisionService) RecordOutcome(_ context.Context, actor decisionsvc.Actor, id string, input decisionsvc.OutcomeInput) (domain.DecisionCall, error) {
	f.actor, f.id, f.action, f.outcome = actor, id, "outcome", input
	return f.actionResult()
}

func (f *fakeDecisionService) Withdraw(_ context.Context, actor decisionsvc.Actor, id string) (domain.DecisionCall, error) {
	f.actor, f.id, f.action = actor, id, "withdraw"
	return f.actionResult()
}

func (f *fakeDecisionService) Retire(_ context.Context, actor decisionsvc.Actor, id string, retired bool) (domain.DecisionCall, error) {
	f.actor, f.id, f.action, f.retired = actor, id, "retire", retired
	return f.actionResult()
}

func (f *fakeDecisionService) actionResult() (domain.DecisionCall, error) {
	if f.err != nil {
		return domain.DecisionCall{}, f.err
	}
	call := sampleDecisionCall()
	call.ID = f.id
	return call, nil
}

func sampleDecisionCall() domain.DecisionCall {
	return domain.DecisionCall{
		ID: "dec_1", Origin: "h_a", State: domain.DecisionStateNeedsYou,
		RaisedAt: time.Date(2026, 11, 16, 15, 0, 0, 0, time.UTC),
		Raised: domain.DecisionRaisedPayload{
			Title:   "Which auth scheme should the proxy use?",
			Context: "The connector calls the proxy for each tenant.",
			Options: []domain.DecisionOption{
				{Label: "OAuth client credentials", Consequence: "One app registration per tenant.", Recommended: true},
				{Label: "Per-tenant API keys", Consequence: "Customers rotate keys by hand."},
			},
			MachineLabel: "mac-mini", OwnerSessionID: "w-1", OwnerSessionName: "Worker one",
		},
	}
}

func newDecisionServer(t *testing.T, svc *fakeDecisionService) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newDecisionHandler(svc))
	t.Cleanup(srv.Close)
	return srv
}

func newDecisionHandler(svc *fakeDecisionService) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Decisions: svc}, httpd.ControlDeps{})
}

func decisionRequest(t *testing.T, srv *httptest.Server, method, path, body, callerSession string) ([]byte, int) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if callerSession != "" {
		req.Header.Set(caller.Header, callerSession)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return responseBody, resp.StatusCode
}

func TestDecisionsAPI_RaiseTakesTheSessionFromTheCallerHeader(t *testing.T) {
	svc := &fakeDecisionService{}
	body, status := decisionRequest(t, newDecisionServer(t, svc), http.MethodPost, "/api/v1/decisions", raiseDecisionBody, "w-1")
	if status != http.StatusCreated {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if svc.actor != (decisionsvc.Actor{Session: "w-1"}) || svc.raise.Title != "Which auth scheme should the proxy use?" || len(svc.raise.Options) != 2 || !svc.raise.Options[0].Recommended {
		t.Fatalf("actor=%+v raise=%+v", svc.actor, svc.raise)
	}
	var got struct {
		ID, Link, State, StateLabel, HostID string
		Links                               []json.RawMessage `json:"links"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode response: %v body=%s", err, body)
	}
	if got.ID != "dec_1" || got.Link != "ao://decisions/dec_1" || got.State != "needs_you" || got.StateLabel != "Needs you" || got.HostID != "h_a" || got.Links == nil || len(got.Links) != 0 {
		t.Fatalf("response=%+v body=%s", got, body)
	}
}

func TestDecisionsAPI_WithoutACallerThePersonIsActing(t *testing.T) {
	svc := &fakeDecisionService{}
	body, status := decisionRequest(t, newDecisionServer(t, svc), http.MethodGet, "/api/v1/decisions", "", "")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if svc.actor != (decisionsvc.Actor{Surface: domain.RequestSurfaceCLI}) || svc.scope != decisionsvc.ScopeNeedsYou {
		t.Fatalf("actor=%+v scope=%q", svc.actor, svc.scope)
	}
}

func TestDecisionsAPI_ListSearchAndGet(t *testing.T) {
	svc := &fakeDecisionService{}
	srv := newDecisionServer(t, svc)
	if body, status := decisionRequest(t, srv, http.MethodGet, "/api/v1/decisions?scope=mine", "", "w-1"); status != http.StatusOK || svc.scope != decisionsvc.ScopeMine {
		t.Fatalf("list status=%d body=%s scope=%q", status, body, svc.scope)
	}
	if body, status := decisionRequest(t, srv, http.MethodGet, "/api/v1/decisions?q=auth+scheme&scope=mine", "", "w-1"); status != http.StatusOK || svc.query != "auth scheme" {
		t.Fatalf("search status=%d body=%s query=%q", status, body, svc.query)
	}
	body, status := decisionRequest(t, srv, http.MethodGet, "/api/v1/decisions/dec_1", "", "")
	if status != http.StatusOK || svc.id != "dec_1" || !reportContainsAll(string(body), `"id":"dec_1"`, `"kind":"raised"`, `"authorKind":"session"`, `"authorSession":"w-1"`) {
		t.Fatalf("get status=%d body=%s id=%q", status, body, svc.id)
	}
}

func TestDecisionsAPI_ExplicitBlankQueryUsesSearchValidation(t *testing.T) {
	for _, path := range []string{"/api/v1/decisions?q=", "/api/v1/decisions?q=%20%20"} {
		t.Run(path, func(t *testing.T) {
			svc := &fakeDecisionService{err: apierr.Invalid("DECISION_QUERY_REQUIRED", "Say what to search for.", nil)}
			recorder := httptest.NewRecorder()
			newDecisionHandler(svc).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			body, status := recorder.Body.Bytes(), recorder.Code
			if status != http.StatusBadRequest || !reportContainsAll(string(body), "DECISION_QUERY_REQUIRED", "Say what to search for") {
				t.Fatalf("status=%d body=%s", status, body)
			}
			if svc.searches != 1 {
				t.Fatalf("searches=%d scope=%q, want one search", svc.searches, svc.scope)
			}
		})
	}
}

func TestDecisionsAPI_Errors(t *testing.T) {
	svc := &fakeDecisionService{err: apierr.Invalid(
		"DECISION_INVALID",
		"Mark exactly one option as recommended; this call marks 0.",
		map[string]any{"rule": "recommended_count"},
	)}
	srv := newDecisionServer(t, svc)
	body, status := decisionRequest(t, srv, http.MethodPost, "/api/v1/decisions", raiseDecisionBody, "w-1")
	if status != http.StatusBadRequest || !reportContainsAll(string(body), "DECISION_INVALID", "Mark exactly one option as recommended") {
		t.Fatalf("service error status=%d body=%s", status, body)
	}
	body, status = decisionRequest(t, srv, http.MethodPost, "/api/v1/decisions", `{"title":"x","surprise":1}`, "w-1")
	if status != http.StatusBadRequest || !reportContainsAll(string(body), "INVALID_JSON") {
		t.Fatalf("json error status=%d body=%s", status, body)
	}
}

func TestDecisionsAPI_RejectsOversizedWhitespaceBeforeDecoding(t *testing.T) {
	svc := &fakeDecisionService{}
	body := `{"title":"` + strings.Repeat(" ", 70<<10) + `x","context":"C","options":[{"label":"A","consequence":"a","recommended":true},{"label":"B","consequence":"b"}]}`
	response, status := decisionRequest(t, newDecisionServer(t, svc), http.MethodPost, "/api/v1/decisions", body, "w-1")
	if status != http.StatusBadRequest || !reportContainsAll(string(response), "INVALID_JSON") {
		t.Fatalf("status=%d body=%s", status, response)
	}
	if svc.raise.Title != "" {
		t.Fatalf("service received oversized title of %d bytes", len(svc.raise.Title))
	}
}

func TestDecisionsAPI_RejectsOversizedActionBodiesBeforeDecoding(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{name: "answer", path: "/api/v1/decisions/dec_1/answer", body: `{"choice":"option","option":1,"note":"` + strings.Repeat(" ", 70<<10) + `x"}`},
		{name: "dismiss", path: "/api/v1/decisions/dec_1/dismiss", body: `{"note":"` + strings.Repeat(" ", 70<<10) + `x"}`},
		{name: "outcome", path: "/api/v1/decisions/dec_1/outcome", body: `{"text":"done","link":"https://example.com/` + strings.Repeat("x", 70<<10) + `"}`},
		{name: "retire", path: "/api/v1/decisions/dec_1/retire", body: `{` + strings.Repeat(" ", 70<<10) + `"retired":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeDecisionService{}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			newDecisionHandler(svc).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || !reportContainsAll(recorder.Body.String(), "INVALID_JSON") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if svc.action != "" {
				t.Fatalf("service action = %q", svc.action)
			}
		})
	}
}

func TestDecisionsAPI_Actions(t *testing.T) {
	svc := &fakeDecisionService{}
	srv := newDecisionServer(t, svc)

	body, status := decisionRequest(t, srv, http.MethodPost, "/api/v1/decisions/dec_1/answer", `{"choice":"option","option":2,"note":"Rotate monthly"}`, "")
	if status != http.StatusOK || !strings.Contains(string(body), `"id":"dec_1"`) || svc.id != "dec_1" || svc.action != "answer" ||
		svc.answer != (decisionsvc.AnswerInput{Choice: "option", Option: 2, Note: "Rotate monthly"}) || svc.actor.Session != "" {
		t.Fatalf("answer status=%d body=%s actor=%+v input=%+v", status, body, svc.actor, svc.answer)
	}

	body, status = decisionRequest(t, srv, http.MethodPost, "/api/v1/decisions/dec_1/dismiss", `{"note":"Not enough to go on"}`, "")
	if status != http.StatusOK || svc.action != "dismiss" || svc.note != "Not enough to go on" {
		t.Fatalf("dismiss status=%d body=%s action=%q note=%q", status, body, svc.action, svc.note)
	}
	body, status = decisionRequest(t, srv, http.MethodPost, "/api/v1/decisions/dec_1/dismiss", "", "")
	if status != http.StatusOK || svc.action != "dismiss" || svc.note != "" {
		t.Fatalf("empty dismiss status=%d body=%s action=%q note=%q", status, body, svc.action, svc.note)
	}

	body, status = decisionRequest(t, srv, http.MethodPost, "/api/v1/decisions/dec_1/outcome", `{"forEntry":"dent_answer","text":"Shipped in PR 412","link":"https://example.com/pr/412"}`, "w-1")
	if status != http.StatusOK || svc.action != "outcome" || svc.actor.Session != "w-1" ||
		svc.outcome != (decisionsvc.OutcomeInput{ForEntry: "dent_answer", Text: "Shipped in PR 412", Link: "https://example.com/pr/412"}) {
		t.Fatalf("outcome status=%d body=%s actor=%+v input=%+v", status, body, svc.actor, svc.outcome)
	}

	body, status = decisionRequest(t, srv, http.MethodPost, "/api/v1/decisions/dec_1/withdraw", "", "w-1")
	if status != http.StatusOK || svc.action != "withdraw" || svc.actor.Session != "w-1" {
		t.Fatalf("withdraw status=%d body=%s actor=%+v action=%q", status, body, svc.actor, svc.action)
	}

	body, status = decisionRequest(t, srv, http.MethodPost, "/api/v1/decisions/dec_1/retire", `{"retired":true}`, "")
	if status != http.StatusOK || svc.action != "retire" || !svc.retired {
		t.Fatalf("retire status=%d body=%s action=%q retired=%v", status, body, svc.action, svc.retired)
	}
}

func TestDecisionsAPI_ActionRefusals(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			name: "person only", status: http.StatusForbidden, code: "DECISION_PERSON_ONLY",
			err: apierr.Forbidden("DECISION_PERSON_ONLY", "Only the person can answer, dismiss or retire a call. A session cannot."),
		},
		{
			name: "wrong state", status: http.StatusConflict, code: "DECISION_WRONG_STATE",
			err: apierr.Conflict("DECISION_WRONG_STATE", `This call is closed. Its state is "Done".`, map[string]any{"state": "done"}),
		},
		{
			name: "not found", status: http.StatusNotFound, code: "DECISION_NOT_FOUND",
			err: apierr.NotFound("DECISION_NOT_FOUND", "Unknown call"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeDecisionService{err: tt.err}
			body, status := decisionRequest(t, newDecisionServer(t, svc), http.MethodPost, "/api/v1/decisions/dec_1/answer", `{"choice":"option","option":1}`, "")
			if status != tt.status || !strings.Contains(string(body), tt.code) {
				t.Fatalf("status=%d body=%s", status, body)
			}
		})
	}
}

func TestDecisionsAPI_RaiseCarriesUrgencyKindAndPrediction(t *testing.T) {
	svc := &fakeDecisionService{}
	body := `{"title":"Close the duplicate PR?","context":"It repeats an open one.",` +
		`"options":[{"label":"Close it","consequence":"Branch kept.","recommended":true},{"label":"Keep both","consequence":"Two reviews."}],` +
		`"urgency":"blocking","kind":"pr_hygiene","prediction":{"option":1,"why":"It is a duplicate.","undo":"Reopen the PR."}}`
	response, status := decisionRequest(t, newDecisionServer(t, svc), http.MethodPost, "/api/v1/decisions", body, "w-1")
	if status != http.StatusCreated {
		t.Fatalf("status=%d body=%s", status, response)
	}
	want := domain.DecisionPrediction{Option: 1, Why: "It is a duplicate.", Undo: "Reopen the PR."}
	if svc.raise.Urgency != "blocking" || svc.raise.Kind != "pr_hygiene" || svc.raise.Prediction == nil || *svc.raise.Prediction != want {
		t.Fatalf("raise=%+v", svc.raise)
	}
}

func raisedResponse(t *testing.T, call domain.DecisionCall) controllers.DecisionCallResponse {
	t.Helper()
	svc := &fakeDecisionService{call: &call}
	body, status := decisionRequest(t, newDecisionServer(t, svc), http.MethodPost, "/api/v1/decisions", raiseDecisionBody, "w-1")
	if status != http.StatusCreated {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var got controllers.DecisionCallResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	return got
}

func TestDecisionCallResponseCarriesAutonomyFields(t *testing.T) {
	call := sampleDecisionCall()
	legacy := raisedResponse(t, call)
	if legacy.Urgency != "can_wait" || legacy.Kind != "" || legacy.Prediction != nil || legacy.PredictionAgreed != nil {
		t.Fatalf("legacy response = %+v", legacy)
	}

	call.Raised.Urgency = domain.DecisionUrgencyBlocking
	call.Raised.Kind = "pr_hygiene"
	call.Raised.AutonomyMode = domain.AutonomyObserve
	call.Raised.Prediction = &domain.DecisionPrediction{Option: 2, Why: "w", Undo: "u"}
	call.State = domain.DecisionStateInProgress
	call.StandingKind = domain.DecisionKindAnswered
	call.Choice, call.Option = domain.DecisionChoiceOption, 2
	got := raisedResponse(t, call)
	if got.Urgency != "blocking" || got.Kind != "pr_hygiene" || got.KindLabel != "Pull request hygiene" || got.Reserved || got.AutonomyMode != "observe" {
		t.Fatalf("response = %+v", got)
	}
	if got.Prediction == nil || got.Prediction.Option != 2 || got.Prediction.Label != "Per-tenant API keys" || got.Prediction.Why != "w" || got.Prediction.Undo != "u" {
		t.Fatalf("prediction = %+v", got.Prediction)
	}
	if got.PredictionAgreed == nil || !*got.PredictionAgreed {
		t.Fatalf("predictionAgreed = %v", got.PredictionAgreed)
	}
}

func TestDecisionsAPI_Kinds(t *testing.T) {
	svc := &fakeDecisionService{}
	srv := newDecisionServer(t, svc)
	body, status := decisionRequest(t, srv, http.MethodGet, "/api/v1/decisions/kinds?project=shop", "", "")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if svc.project != "shop" || svc.actor.Session != "" {
		t.Fatalf("actor=%+v project=%q", svc.actor, svc.project)
	}
	var got controllers.DecisionKindsResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if got.ProjectID != "shop" || got.Mode != "observe" || len(got.Kinds) != 2 || !got.Kinds[0].Reserved || got.Kinds[1].Reserved || got.Kinds[1].Label != "Pull request hygiene" || !got.Kinds[0].ReservedByDefault || got.Kinds[1].ReservedByDefault {
		t.Fatalf("kinds = %+v", got)
	}
	if _, status := decisionRequest(t, srv, http.MethodGet, "/api/v1/decisions/kinds", "", "w-1"); status != http.StatusOK || svc.actor.Session != "w-1" {
		t.Fatalf("session kinds status=%d actor=%+v", status, svc.actor)
	}
}

func TestDecisionsAPI_Observe(t *testing.T) {
	svc := &fakeDecisionService{}
	body, status := decisionRequest(t, newDecisionServer(t, svc), http.MethodGet, "/api/v1/decisions/observe?project=shop", "", "")
	if status != http.StatusOK || svc.project != "shop" {
		t.Fatalf("status=%d project=%q body=%s", status, svc.project, body)
	}
	var got controllers.DecisionObserveResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	want := controllers.DecisionObserveRowResponse{Kind: "pr_hygiene", Label: "Pull request hygiene", Calls: 4, Answered: 3, Agreed: 1, Differed: 2}
	if got.ProjectID != "shop" || got.Mode != "observe" || len(got.Rows) != 1 || got.Rows[0] != want {
		t.Fatalf("observe = %+v", got)
	}
}

func TestDecisionsAPI_DecideAndOverrule(t *testing.T) {
	svc := &fakeDecisionService{}
	srv := newDecisionServer(t, svc)
	body := `{"key":"close-pr-12","kind":"pr_hygiene","what":"Closed the duplicate pull request #12","why":"It repeats #11.","undo":"Reopen #12.","verify":"One open PR remains.","links":[{"label":"PR","url":"https://example.com/12"}],"reference":"SKY-1"}`
	response, status := decisionRequest(t, srv, http.MethodPost, "/api/v1/decisions/decided", body, "w-1")
	if status != http.StatusCreated {
		t.Fatalf("status=%d body=%s", status, response)
	}
	want := decisionsvc.DecideInput{Key: "close-pr-12", Kind: "pr_hygiene", What: "Closed the duplicate pull request #12", Why: "It repeats #11.", Undo: "Reopen #12.", Verify: "One open PR remains.",
		Links: []domain.DecisionLink{{Label: "PR", URL: "https://example.com/12"}}, Reference: "SKY-1"}
	if svc.actor.Session != "w-1" || !reflect.DeepEqual(svc.decide, want) {
		t.Fatalf("decide=%+v actor=%+v", svc.decide, svc.actor)
	}
	var got controllers.DecisionCallResponse
	if err := json.Unmarshal(response, &got); err != nil {
		t.Fatal(err)
	}
	if got.State != "decided" || got.StateLabel != "Decided for you" || got.Decided == nil || got.Decided.Undo != "Reopen #12." || got.Decided.Verify != "One open PR remains." || got.Decided.Key != "close-pr-12" || got.AutonomyMode != "on" {
		t.Fatalf("response = %+v", got)
	}

	if body, status := decisionRequest(t, srv, http.MethodPost, "/api/v1/decisions/dec_2/overrule", `{"note":"Keep both open."}`, ""); status != http.StatusOK ||
		svc.action != "overrule" || svc.id != "dec_2" || svc.note != "Keep both open." || svc.actor.Session != "" {
		t.Fatalf("overrule status=%d body=%s svc=%+v", status, body, svc)
	}
	if body, status := decisionRequest(t, srv, http.MethodGet, "/api/v1/decisions?scope=decided", "", ""); status != http.StatusOK || svc.scope != decisionsvc.ScopeDecided {
		t.Fatalf("decided scope status=%d body=%s scope=%q", status, body, svc.scope)
	}
}

func TestDecisionsAPI_CarriedOut(t *testing.T) {
	svc := &fakeDecisionService{}
	body, status := decisionRequest(t, newDecisionServer(t, svc), http.MethodPost, "/api/v1/decisions/dec_2/carried-out", "", "w-1")
	if status != http.StatusOK || svc.action != "carried-out" || svc.id != "dec_2" || svc.actor.Session != "w-1" {
		t.Fatalf("status=%d body=%s svc=%+v", status, body, svc)
	}
	var got controllers.DecisionCallResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Decided == nil || !got.Decided.CarriedOut {
		t.Fatalf("decided = %+v", got.Decided)
	}
}

type lookupDecisionService struct {
	*fakeDecisionService
	asked []string
}

func (f *lookupDecisionService) Lookup(_ context.Context, ids []string) ([]domain.DecisionCall, error) {
	f.asked = ids
	if len(ids) > 100 {
		return nil, apierr.Invalid("DECISION_IDS_TOO_MANY", "Look up at most 100 calls at a time.", nil)
	}
	return []domain.DecisionCall{{ID: ids[0], State: domain.DecisionStateDone}}, nil
}

func TestDecisionsAPI_LookupByIDs(t *testing.T) {
	svc := &lookupDecisionService{fakeDecisionService: &fakeDecisionService{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Decisions: svc}, httpd.ControlDeps{}))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/v1/decisions?ids=dec_a,dec_b&ids=dec_c")
	if err != nil {
		t.Fatal(err)
	}
	var body controllers.ListDecisionsResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(body.Calls) != 1 || body.Calls[0].ID != "dec_a" || strings.Join(svc.asked, ",") != "dec_a,dec_b,dec_c" {
		t.Fatalf("lookup: status %d calls %+v asked %v", resp.StatusCode, body.Calls, svc.asked)
	}
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = fmt.Sprintf("dec_%d", i)
	}
	resp, err = http.Get(srv.URL + "/api/v1/decisions?ids=" + strings.Join(ids, ","))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("101 ids: status %d, want 400", resp.StatusCode)
	}
}

func TestDecisionsAPI_AnswerPassesTheIdempotencyKeyHeader(t *testing.T) {
	svc := &fakeDecisionService{}
	srv := newDecisionServer(t, svc)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/decisions/dec_1/answer", strings.NewReader(`{"choice":"option","option":2}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "ans_7f3c")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || svc.answer.IdempotencyKey != "ans_7f3c" {
		t.Fatalf("status=%d input=%+v", resp.StatusCode, svc.answer)
	}
}
