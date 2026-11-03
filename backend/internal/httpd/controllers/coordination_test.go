package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type fakeCoordinationService struct {
	*fakeSessionService
	events []domain.CoordinationEvent
	id     domain.SessionID
	err    error
}

func (f *fakeCoordinationService) Coordination(_ context.Context, id domain.SessionID) ([]domain.CoordinationEvent, error) {
	f.id = id
	return f.events, f.err
}

func TestSessionCoordinationAPI(t *testing.T) {
	for _, size := range []int{0, 2, 51} {
		svc := &fakeCoordinationService{fakeSessionService: newFakeSessionService(), events: make([]domain.CoordinationEvent, size)}
		srv := coordinationServer(t, svc)
		body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/ao-1/coordination", "")
		var got controllers.SessionCoordinationResponse
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if status != http.StatusOK || svc.id != "ao-1" || len(got.Events) != min(size, 50) || got.HasMore != (size > 50) || got.Events == nil {
			t.Fatalf("status=%d got=%+v", status, got)
		}
	}
	svc := &fakeCoordinationService{fakeSessionService: newFakeSessionService(), err: apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")}
	body, status, _ := doRequest(t, coordinationServer(t, svc), http.MethodGet, "/api/v1/sessions/missing/coordination", "")
	if status != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func coordinationServer(t *testing.T, svc *fakeCoordinationService) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { (&controllers.SessionsController{Svc: svc}).Register(r) })
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}
