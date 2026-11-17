package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/caller"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	decisionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/decision"
)

const raiseDecisionBody = `{"title":"Which auth scheme should the proxy use?","context":"The connector calls the proxy for each tenant.","options":[{"label":"OAuth client credentials","consequence":"One app registration per tenant.","recommended":true},{"label":"Per-tenant API keys","consequence":"Customers rotate keys by hand."}]}`

type fakeDecisionService struct {
	actor decisionsvc.Actor
	raise decisionsvc.RaiseInput
	scope decisionsvc.Scope
	query string
	id    string
	err   error
}

func (f *fakeDecisionService) Raise(_ context.Context, actor decisionsvc.Actor, input decisionsvc.RaiseInput) (domain.DecisionCall, error) {
	f.actor, f.raise = actor, input
	if f.err != nil {
		return domain.DecisionCall{}, f.err
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

func (f *fakeDecisionService) Search(_ context.Context, actor decisionsvc.Actor, query string) ([]domain.DecisionCall, error) {
	f.actor, f.query = actor, query
	if f.err != nil {
		return nil, f.err
	}
	return []domain.DecisionCall{sampleDecisionCall()}, nil
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
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Decisions: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
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
