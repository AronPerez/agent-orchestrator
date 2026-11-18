package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestReportStorePersistsOutputsAndDeadlinesAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	want := domain.ReportRecord{
		ID: "rpt_1", SessionID: sess.ID, ProjectID: sess.ProjectID,
		Outputs: []domain.ReportOutput{
			{Kind: domain.ReportOutputArtifact, Reference: "opaque-ref", Label: "Result"},
			{Kind: domain.ReportOutputArtifact, Reference: "second"},
			{Kind: domain.ReportOutputPRCreated, Reference: "https://github.com/o/r/pull/1"},
		},
		CreatedAt: now, DeliveryState: domain.ReportPending,
		AvailableAt: now.Add(domain.ReportBatchFallback), RepeatCount: 1,
	}
	if _, err = s.CreateReport(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, ok, err := s.GetReport(ctx, want.ID)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.SessionID != sess.ID || got.ProjectID != sess.ProjectID || len(got.Outputs) != 3 || got.Outputs[0].Label != "Result" || got.Outputs[1].Reference != "second" || got.Outputs[2].Kind != domain.ReportOutputPRCreated || !got.AvailableAt.Equal(want.AvailableAt) {
		t.Fatalf("got=%+v", got)
	}
	if pending, err := s.ListPendingReports(ctx, now, 10); err != nil || len(pending) != 0 {
		t.Fatalf("early pending=%+v err=%v", pending, err)
	}
	urls, err := s.ListReportedPRURLs(ctx, sess.ID)
	if err != nil || len(urls) != 1 || urls[0] != "https://github.com/o/r/pull/1" {
		t.Fatalf("reported PR URLs=%v err=%v", urls, err)
	}
	events, err := s.EventsAfter(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var reportEvents int
	for _, event := range events {
		if event.SessionID == string(sess.ID) && event.Type == "session_updated" {
			reportEvents++
		}
	}
	if reportEvents != 1 {
		t.Fatalf("reported PR CDC events=%d, want 1", reportEvents)
	}
	pending, err := s.ListPendingReports(ctx, want.AvailableAt, 10)
	if err != nil || len(pending) != 1 || pending[0].ID != want.ID || len(pending[0].Outputs) != 3 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
}

func TestReportStoreRejectsInvalidRecord(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateReport(context.Background(), domain.ReportRecord{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestReportStorePersistsVerificationFact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	report := domain.ReportRecord{
		ID: "rpt_verified", SessionID: sess.ID, ProjectID: sess.ProjectID,
		State: domain.ReportDone, Note: "done", CreatedAt: now,
		DeliveryState: domain.ReportPending, AvailableAt: now.Add(domain.ReportSettlementWindow),
		SettlementDeadline: now.Add(domain.ReportSettlementWindow), RepeatCount: 1,
		Verification: &domain.ReportVerification{
			State: domain.ReportVerificationFailed, Evidence: "verification/rpt_verified.txt",
			StartedAt: now, CompletedAt: now.Add(3 * time.Second),
			WorkspaceRevision: "head-1", InteractionAt: now.Add(-time.Minute),
		},
	}
	if _, err := s.CreateReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetReport(ctx, report.ID)
	if err != nil || !ok || got.Verification == nil {
		t.Fatalf("report=%+v ok=%v err=%v", got, ok, err)
	}
	if *got.Verification != *report.Verification {
		t.Fatalf("verification=%+v want %+v", got.Verification, report.Verification)
	}
}

func TestListLatestDoneReportsForSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	one, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	create := func(id string, sessionID domain.SessionID, state domain.ReportState, created time.Time, verification *domain.ReportVerification) {
		t.Helper()
		report := domain.ReportRecord{
			ID: id, SessionID: sessionID, ProjectID: "ao", State: state, Note: string(state), CreatedAt: created,
			DeliveryState: domain.ReportPending, AvailableAt: created, RepeatCount: 1, Verification: verification,
		}
		if state == domain.ReportDone {
			report.SettlementDeadline = created.Add(domain.ReportSettlementWindow)
		}
		if _, err := s.CreateReport(ctx, report); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	create("done-old", one.ID, domain.ReportDone, base, &domain.ReportVerification{
		State: domain.ReportVerificationPassed, Evidence: "verification/done-old.txt", StartedAt: base, CompletedAt: base.Add(time.Second), WorkspaceRevision: "head-old",
	})
	create("checkpoint-newer", one.ID, domain.ReportCheckpoint, base.Add(2*time.Minute), nil)
	wantVerification := &domain.ReportVerification{
		State: domain.ReportVerificationFailed, Evidence: "verification/done-new.txt", StartedAt: base.Add(3 * time.Minute), CompletedAt: base.Add(3*time.Minute + time.Second), WorkspaceRevision: "head-new",
	}
	create("done-new", one.ID, domain.ReportDone, base.Add(3*time.Minute), wantVerification)
	create("checkpoint-after-done", one.ID, domain.ReportCheckpoint, base.Add(4*time.Minute), nil)
	create("done-unverified", two.ID, domain.ReportDone, base.Add(time.Minute), nil)

	got, err := s.ListLatestDoneReportsForSessions(ctx, []domain.SessionID{one.ID, two.ID, "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if latest, ok := got[one.ID]; !ok || latest.ID != "done-new" || latest.Verification == nil || *latest.Verification != *wantVerification || !latest.Superseded {
		t.Fatalf("latest verified report = %+v", latest)
	}
	if latest, ok := got[two.ID]; !ok || latest.ID != "done-unverified" || latest.Verification != nil {
		t.Fatalf("latest unverified report = %+v, present=%v", latest, ok)
	}
	if _, ok := got["missing"]; ok {
		t.Fatalf("missing session received latest report: %+v", got["missing"])
	}
}

func TestReportStoreCreatesReportWithFirstContractOutcome(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	deadline := time.Date(2026, 11, 17, 14, 0, 0, 0, time.UTC)
	rec := sampleRecord("ao")
	rec.Goal = "ship the smallest version"
	rec.TimeboxDeadlineAt = &deadline
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	now := deadline.Add(-time.Hour)
	report := domain.ReportRecord{
		ID: "rpt_contract_done", SessionID: session.ID, ProjectID: session.ProjectID,
		State: domain.ReportDone, Note: "done", CreatedAt: now,
		DeliveryState: domain.ReportPending, AvailableAt: now.Add(domain.ReportSettlementWindow),
		SettlementDeadline: now.Add(domain.ReportSettlementWindow), RepeatCount: 1,
	}
	if _, err = s.CreateReportWithContractOutcome(ctx, report, domain.SessionContractOutcomeMet); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetSession(ctx, session.ID)
	if err != nil || !ok || got.ContractOutcome != domain.SessionContractOutcomeMet {
		t.Fatalf("session = %+v, ok=%v err=%v", got, ok, err)
	}
	if expired, err := s.ExpireSessionContract(ctx, session.ID, deadline, deadline); err != nil || expired {
		t.Fatalf("deadline overwrote final report: expired=%v err=%v", expired, err)
	}
	storedReport, ok, err := s.GetReport(ctx, report.ID)
	if err != nil || !ok || storedReport.State != domain.ReportDone {
		t.Fatalf("report = %+v, ok=%v err=%v", storedReport, ok, err)
	}

	blocked := report
	blocked.ID = "rpt_contract_blocked"
	blocked.State = domain.ReportStuck
	blocked.Note = "later"
	blocked.SettlementDeadline = time.Time{}
	blocked.AvailableAt = now
	if _, err = s.CreateReportWithContractOutcome(ctx, blocked, domain.SessionContractOutcomeBlocked); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.GetSession(ctx, session.ID)
	if err != nil || got.ContractOutcome != domain.SessionContractOutcomeMet {
		t.Fatalf("later report overwrote outcome: %+v err=%v", got, err)
	}
}

func TestReportStoreCreateOnceReturnsExistingReport(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.ReportRecord{
		ID: "rpt_contract_once", SessionID: sess.ID, ProjectID: sess.ProjectID,
		State: domain.ReportCheckpoint, Note: "first", CreatedAt: now, DeliveryState: domain.ReportPending,
		AvailableAt: now, RepeatCount: 1,
		Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "result.md"}},
	}
	first, inserted, err := s.CreateReportWithContractOutcomeOnce(ctx, rec, domain.SessionContractOutcomeNone)
	if err != nil || !inserted || first.Note != "first" || len(first.Outputs) != 1 {
		t.Fatalf("first=%+v inserted=%v err=%v", first, inserted, err)
	}
	replay := rec
	replay.Note = "retry must not replace durable content"
	second, inserted, err := s.CreateReportWithContractOutcomeOnce(ctx, replay, domain.SessionContractOutcomeNone)
	if err != nil || inserted || second.Note != "first" || len(second.Outputs) != 1 || second.Outputs[0].Reference != "result.md" {
		t.Fatalf("second=%+v inserted=%v err=%v", second, inserted, err)
	}
	rows, err := s.ListReportsBySession(ctx, sess.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestReportStoreClaimAcknowledgeAndTokenFence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.ReportRecord{ID: "rpt_lease", SessionID: sess.ID, ProjectID: sess.ProjectID, State: domain.ReportDone, Note: "done", CreatedAt: now.Add(-domain.ReportSettlementWindow), DeliveryState: domain.ReportPending, AvailableAt: now, SettlementDeadline: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, rec); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := s.ClaimReport(ctx, rec.ID, "lease-1", now)
	if err != nil || !ok || claimed.DeliveryAttempts != 1 || claimed.DeliveryState != domain.ReportClaimed {
		t.Fatalf("claimed=%+v ok=%v err=%v", claimed, ok, err)
	}
	if _, ok, err = s.ClaimReport(ctx, rec.ID, "lease-2", now); err != nil || ok {
		t.Fatalf("second claim ok=%v err=%v", ok, err)
	}
	if _, ok, err = s.AcknowledgeReport(ctx, rec.ID, "wrong", now.Add(time.Second)); err != nil || ok {
		t.Fatalf("wrong ack ok=%v err=%v", ok, err)
	}
	acked, ok, err := s.AcknowledgeReport(ctx, rec.ID, "lease-1", now.Add(time.Second))
	if err != nil || !ok || acked.DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("acked=%+v ok=%v err=%v", acked, ok, err)
	}
}

func TestReportStoreRequeuesInterruptedClaimOnRestartAndFencesOldToken(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.ReportRecord{ID: "rpt_restart", SessionID: sess.ID, ProjectID: sess.ProjectID, Message: "information", CreatedAt: now.Add(-time.Hour), DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimReport(ctx, rec.ID, "old-token", now); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if count, err := s.RequeueClaimedReports(ctx); err != nil || count != 1 {
		t.Fatalf("restart recovery count=%d err=%v", count, err)
	}
	recovered, ok, err := s.GetReport(ctx, rec.ID)
	if err != nil || !ok || recovered.DeliveryState != domain.ReportPending || recovered.ClaimToken != "" || !recovered.ClaimedAt.IsZero() || recovered.DeliveryAttempts != 1 || !strings.Contains(recovered.LastError, "restart") {
		t.Fatalf("recovered=%+v ok=%v err=%v", recovered, ok, err)
	}
	if _, ok, err := s.AcknowledgeReport(ctx, rec.ID, "old-token", now.Add(time.Second)); err != nil || ok {
		t.Fatalf("stale acknowledge ok=%v err=%v", ok, err)
	}
	claimed, ok, err := s.ClaimReport(ctx, rec.ID, "new-token", now.Add(2*time.Second))
	if err != nil || !ok || claimed.DeliveryAttempts != 2 {
		t.Fatalf("reclaim=%+v ok=%v err=%v", claimed, ok, err)
	}
	if _, ok, err := s.AcknowledgeReport(ctx, rec.ID, "old-token", now.Add(3*time.Second)); err != nil || ok {
		t.Fatalf("old token crossed fence ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.AcknowledgeReport(ctx, rec.ID, "new-token", now.Add(4*time.Second)); err != nil || !ok {
		t.Fatalf("new acknowledge ok=%v err=%v", ok, err)
	}
}

func TestReportStoreDurableBatchIdentityExcludesLaterReportsOnRetry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	create := func(id string) {
		t.Helper()
		_, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: "ao", Message: id, CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	create("first")
	create("second")
	claimed, err := s.ClaimPendingReportBatch(ctx, "ao", "token-1", now)
	if err != nil || len(claimed) != 2 || claimed[0].DeliveryBatchID == "" || claimed[0].DeliveryBatchID != claimed[1].DeliveryBatchID {
		t.Fatalf("claimed = %+v, err = %v", claimed, err)
	}
	batchID := claimed[0].DeliveryBatchID
	if _, err := s.ReleaseReportBatch(ctx, "ao", "token-1", now, "accepted response lost"); err != nil {
		t.Fatal(err)
	}
	create("later")
	retried, err := s.ClaimPendingReportBatch(ctx, "ao", "token-2", now)
	if err != nil || len(retried) != 2 || retried[0].DeliveryBatchID != batchID {
		t.Fatalf("retried = %+v, err = %v", retried, err)
	}
	if _, err := s.AcknowledgeReportBatch(ctx, "ao", "token-2", now); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimPendingReportBatch(ctx, "ao", "token-3", now)
	if err != nil || len(next) != 1 || next[0].ID != "later" || next[0].DeliveryBatchID == batchID {
		t.Fatalf("next = %+v, err = %v", next, err)
	}
}

func TestReportStoreInterruptLimitSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if acquired, _, err := s.AcquireReportInterrupt(ctx, sess.ID, now, 3*time.Minute); err != nil || !acquired {
		t.Fatalf("first acquired = %v, err = %v", acquired, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if acquired, next, err := s.AcquireReportInterrupt(ctx, sess.ID, now.Add(2*time.Minute), 3*time.Minute); err != nil || acquired || !next.Equal(now.Add(3*time.Minute)) {
		t.Fatalf("restart acquired = %v, err = %v", acquired, err)
	}
	if acquired, _, err := s.AcquireReportInterrupt(ctx, sess.ID, now.Add(3*time.Minute), 3*time.Minute); err != nil || !acquired {
		t.Fatalf("boundary acquired = %v, err = %v", acquired, err)
	}
}

func TestReportStoreDeferredBatchYieldsScheduleToOtherProjects(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "ao")
	seedProject(t, s, "other")
	refusing, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	healthy, err := s.CreateSession(ctx, sampleRecord("other"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, rec := range []domain.ReportRecord{
		{ID: "rpt_refused", SessionID: refusing.ID, ProjectID: "ao", Message: "stuck", CreatedAt: now.Add(-time.Minute), DeliveryState: domain.ReportPending, AvailableAt: now.Add(-time.Minute), RepeatCount: 1},
		{ID: "rpt_waiting", SessionID: healthy.ID, ProjectID: "other", Message: "done", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1},
	} {
		if _, err := s.CreateReport(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if claimed, err := s.ClaimPendingReportBatch(ctx, "ao", "token-1", now); err != nil || len(claimed) != 1 {
		t.Fatalf("claimed = %+v, err = %v", claimed, err)
	}
	if _, err := s.DeferReportBatch(ctx, "ao", "token-1", now.Add(5*time.Second), "composer is not empty"); err != nil {
		t.Fatal(err)
	}
	next, err := s.ListPendingReportSchedule(ctx, 1)
	if err != nil || len(next) != 1 || next[0].ID != "rpt_waiting" {
		t.Fatalf("next = %+v, err = %v; the refused batch kept the front of the schedule", next, err)
	}
	refused, ok, err := s.GetReport(ctx, "rpt_refused")
	if err != nil || !ok || refused.DeliveryState != domain.ReportPending || refused.LastError != "composer is not empty" || !refused.AvailableAt.Equal(now.Add(5*time.Second)) || refused.DeliveryBatchID == "" {
		t.Fatalf("refused = %+v ok=%v err=%v", refused, ok, err)
	}
}
