package controllers_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	decisionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/decision"
)

func TestDecisionBoardAPI(t *testing.T) {
	done := sampleDecisionCall()
	done.ID, done.State = "dec_2", domain.DecisionStateDone
	svc := &fakeDecisionService{board: decisionsvc.Board{
		Calls:         []domain.DecisionCall{sampleDecisionCall(), done},
		NeedsYouCount: 1,
		ServingHostID: "h_a",
		DoneSince:     time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC),
	}}
	srv := newDecisionServer(t, svc)
	body, status := decisionRequest(t, srv, http.MethodGet, "/api/v1/decisions/board", "", "")
	if status != http.StatusOK || svc.boards != 1 || svc.id != "" {
		t.Fatalf("board status=%d boards=%d get id=%q body=%s", status, svc.boards, svc.id, body)
	}
	if !reportContainsAll(string(body), `"needsYouCount":1`, `"servingHostId":"h_a"`,
		`"doneSince":"2026-11-11T12:00:00Z"`, `"id":"dec_1"`, `"id":"dec_2"`, `"state":"done"`) {
		t.Fatalf("board body = %s", body)
	}
	// A call whose id is literally "board" stays reachable through the
	// encoded detail path; the board route only owns the bare segment.
	if _, status := decisionRequest(t, srv, http.MethodGet, "/api/v1/decisions/dec_board", "", ""); status != http.StatusOK || svc.id != "dec_board" {
		t.Fatalf("detail after board status=%d id=%q", status, svc.id)
	}
}

func TestDecisionBoardAPIEmptyListIsAnArray(t *testing.T) {
	svc := &fakeDecisionService{board: decisionsvc.Board{ServingHostID: "h_a"}}
	srv := newDecisionServer(t, svc)
	body, status := decisionRequest(t, srv, http.MethodGet, "/api/v1/decisions/board", "", "")
	if status != http.StatusOK || !reportContainsAll(string(body), `"calls":[]`, `"needsYouCount":0`) {
		t.Fatalf("empty board status=%d body=%s", status, body)
	}
}

func TestDecisionBoardAPIErrorEnvelope(t *testing.T) {
	svc := &fakeDecisionService{err: errors.New("disk on fire")}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/decisions/board", nil)
	request.Header.Set("X-Request-ID", "req-board-1")
	newDecisionHandler(svc).ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusInternalServerError || !reportContainsAll(body, "DECISION_BOARD_FAILED", "req-board-1") {
		t.Fatalf("board error status=%d body=%s", recorder.Code, body)
	}
	if reportContainsAll(body, "disk on fire") {
		t.Fatalf("board error leaked internal text: %s", body)
	}
}

func TestDecisionSearchLimitAPI(t *testing.T) {
	for _, tc := range []struct {
		path      string
		status    int
		wantLimit int
		searched  bool
	}{
		{"/api/v1/decisions?q=auth", http.StatusOK, 0, true},
		{"/api/v1/decisions?q=auth&limit=50", http.StatusOK, 50, true},
		{"/api/v1/decisions?q=auth&limit=100", http.StatusOK, 100, true},
		{"/api/v1/decisions?q=auth&limit=500", http.StatusOK, 100, true},
		{"/api/v1/decisions?q=auth&limit=0", http.StatusBadRequest, 0, false},
		{"/api/v1/decisions?q=auth&limit=-3", http.StatusBadRequest, 0, false},
		{"/api/v1/decisions?q=auth&limit=ten", http.StatusBadRequest, 0, false},
		// Without q the limit is ignored for backward compatibility.
		{"/api/v1/decisions?scope=needs_you&limit=ten", http.StatusOK, 0, false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			svc := &fakeDecisionService{}
			recorder := httptest.NewRecorder()
			newDecisionHandler(svc).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if recorder.Code != tc.status || svc.limit != tc.wantLimit || (svc.searches == 1) != tc.searched {
				t.Fatalf("status=%d limit=%d searches=%d body=%s", recorder.Code, svc.limit, svc.searches, recorder.Body.String())
			}
			if tc.status == http.StatusBadRequest && !reportContainsAll(recorder.Body.String(), "DECISION_LIMIT_INVALID") {
				t.Fatalf("invalid limit body = %s", recorder.Body.String())
			}
		})
	}
}
