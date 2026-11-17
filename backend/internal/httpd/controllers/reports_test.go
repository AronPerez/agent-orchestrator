package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	reportsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/report"
)

type fakeReportService struct {
	input        reportsvc.CreateInput
	err          error
	reports      []domain.ReportRecord
	verification *domain.ReportVerification
}

func (f *fakeReportService) ListProject(_ context.Context, projectID domain.ProjectID) ([]domain.ReportRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]domain.ReportRecord(nil), f.reports...), nil
}

func (f *fakeReportService) Create(_ context.Context, input reportsvc.CreateInput) (domain.ReportRecord, error) {
	f.input = input
	if f.err != nil {
		return domain.ReportRecord{}, f.err
	}
	return domain.ReportRecord{ID: "rpt_1", SessionID: input.SessionID, ProjectID: "ao", State: input.State, Note: input.Note, Message: input.Message, Outputs: input.Outputs, CreatedAt: time.Unix(1, 0).UTC(), DeliveryState: domain.ReportPending, Verification: f.verification}, nil
}

func TestReportsAPI_CreateAndEnvelope(t *testing.T) {
	verifiedAt := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	svc := &fakeReportService{verification: &domain.ReportVerification{State: domain.ReportVerificationFailed, Evidence: "verification/rpt_1.txt", OutputTail: "FAIL\n", StartedAt: verifiedAt, CompletedAt: verifiedAt.Add(time.Second)}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Reports: svc}, httpd.ControlDeps{}))
	defer srv.Close()
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/reports", `{"sessionId":"ao-7","state":"done","note":"finished","outputs":[{"kind":"artifact","reference":"result","label":"Result"},{"kind":"pr_reviewed","reference":"https://github.com/o/r/pull/7"}]}`)
	if status != http.StatusCreated || svc.input.SessionID != "ao-7" || svc.input.State != domain.ReportDone || svc.input.Note != "finished" || len(svc.input.Outputs) != 2 || svc.input.Outputs[0].Label != "Result" || svc.input.Outputs[1].Kind != domain.ReportOutputPRReviewed {
		t.Fatalf("status=%d body=%s svc=%+v", status, body, svc)
	}
	if !reportContainsAll(string(body), `"id":"rpt_1"`, `"state":"failed"`, `"evidence":"verification/rpt_1.txt"`, `"outputTail":"FAIL\n"`) {
		t.Fatalf("response body = %s", body)
	}
	svc.err = apierr.Invalid("INVALID_REPORT", "bad report", nil)
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/reports", `{"sessionId":"ao-7","state":"done","note":"x"}`)
	if status != http.StatusBadRequest || !reportContainsAll(string(body), "INVALID_REPORT", "requestId") {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func TestReportsAPI_InvalidJSON(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Reports: &fakeReportService{}}, httpd.ControlDeps{}))
	defer srv.Close()
	_, status, _ := doRequest(t, srv, "POST", "/api/v1/reports", `{"unknown":true}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d", status)
	}
}

func TestReportsAPI_ListIsReadOnlyProjection(t *testing.T) {
	created := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	svc := &fakeReportService{reports: []domain.ReportRecord{{
		ID: "rpt_1", SessionID: "worker", ProjectID: "ao", State: domain.ReportDone,
		Note: "finished", CreatedAt: created, RepeatCount: 2, DeliveryBatchID: "report-batch:rpt_1",
		Outputs:      []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "opaque"}},
		Verification: &domain.ReportVerification{State: domain.ReportVerificationPassed, Evidence: "verification/rpt_1.txt", StartedAt: created, CompletedAt: created.Add(time.Second)},
	}}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Reports: svc}, httpd.ControlDeps{}))
	defer srv.Close()
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/reports?projectId=ao", "")
	if status != http.StatusOK || !reportContainsAll(string(body), `"id":"rpt_1"`, `"sessionId":"worker"`, `"reference":"opaque"`, `"repeatCount":2`, `"deliveryBatchId":"report-batch:rpt_1"`, `"state":"passed"`, `"evidence":"verification/rpt_1.txt"`) {
		t.Fatalf("status=%d body=%s", status, body)
	}
	_, status, _ = doRequest(t, srv, "GET", "/api/v1/reports", "")
	if status != http.StatusBadRequest {
		t.Fatalf("missing project status=%d", status)
	}
}

func reportContainsAll(s string, values ...string) bool {
	for _, v := range values {
		if !strings.Contains(s, v) {
			return false
		}
	}
	return true
}
