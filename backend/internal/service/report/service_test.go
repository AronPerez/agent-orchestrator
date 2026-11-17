package report

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/verification"
)

type fakeStore struct {
	session         domain.SessionRecord
	ok              bool
	project         domain.ProjectRecord
	projectOK       bool
	createErr       error
	created         domain.ReportRecord
	contractOutcome domain.SessionContractOutcome
	reports         []domain.ReportRecord
	byID            map[string]domain.ReportRecord
	insertions      int
}

func (f *fakeStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return f.session, f.ok, nil
}
func (f *fakeStore) GetProject(context.Context, string) (domain.ProjectRecord, bool, error) {
	return f.project, f.projectOK, nil
}
func (f *fakeStore) CreateReport(_ context.Context, r domain.ReportRecord) (domain.ReportRecord, error) {
	f.created = r
	return r, nil
}
func (f *fakeStore) CreateReportWithContractOutcome(_ context.Context, r domain.ReportRecord, outcome domain.SessionContractOutcome) (domain.ReportRecord, error) {
	f.created = r
	f.contractOutcome = outcome
	if f.createErr != nil {
		return domain.ReportRecord{}, f.createErr
	}
	return r, nil
}
func (f *fakeStore) CreateReportWithContractOutcomeOnce(_ context.Context, r domain.ReportRecord, outcome domain.SessionContractOutcome) (domain.ReportRecord, bool, error) {
	if f.byID == nil {
		f.byID = make(map[string]domain.ReportRecord)
	}
	if existing, ok := f.byID[r.ID]; ok {
		return existing, false, nil
	}
	f.byID[r.ID] = r
	f.created = r
	f.contractOutcome = outcome
	f.insertions++
	return r, true, nil
}

type fakeVerifier struct {
	requests []verification.Request
	results  []verification.Result
	errs     []error
}

func (f *fakeVerifier) Run(_ context.Context, request verification.Request) (verification.Result, error) {
	f.requests = append(f.requests, request)
	index := len(f.requests) - 1
	if index >= len(f.results) {
		return verification.Result{}, errors.New("unexpected verification run")
	}
	return f.results[index], f.errs[index]
}

type fakeEvidenceArtifacts struct {
	registered   []string
	unregistered []string
}

func (f *fakeEvidenceArtifacts) Register(_ context.Context, id domain.SessionID, reference string) error {
	f.registered = append(f.registered, string(id)+" "+reference)
	return nil
}

func (f *fakeEvidenceArtifacts) Unregister(_ context.Context, id domain.SessionID, reference string) error {
	f.unregistered = append(f.unregistered, string(id)+" "+reference)
	return nil
}

func TestCreateDonePersistsVerificationEvidenceAndReturnsFailureOnce(t *testing.T) {
	dataDir := t.TempDir()
	artifactDir := filepath.Join(dataDir, "artifacts", "ao-7")
	started := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	failed := verification.Result{
		Passed: false, FailedStep: 1, OutputTail: "FAIL expected green\n",
		StartedAt: started, CompletedAt: started.Add(2 * time.Second),
	}
	verifier := &fakeVerifier{results: []verification.Result{failed}, errs: []error{&verification.StepError{Step: 1, Cause: errors.New("exit status 1")}}}
	artifacts := &fakeEvidenceArtifacts{}
	st := &fakeStore{
		ok: true, projectOK: true,
		session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker, Goal: "ship it", Metadata: domain.SessionMetadata{WorkspacePath: "/work/ao", ArtifactDir: artifactDir}},
		project: domain.ProjectRecord{ID: "ao", Config: domain.ProjectConfig{Verify: []string{"go test ./..."}, Env: map[string]string{"CI": "true"}}},
	}
	svc := New(Deps{Store: st, Verifier: verifier, EvidenceArtifacts: artifacts, DataDir: dataDir, NewID: func() string { return "rpt_1" }, Now: func() time.Time { return started }})
	report, err := svc.Create(context.Background(), CreateInput{SessionID: "ao-7", State: domain.ReportDone, Note: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Verification == nil || report.Verification.State != domain.ReportVerificationFailed || report.Verification.OutputTail != failed.OutputTail {
		t.Fatalf("verification = %+v", report.Verification)
	}
	if st.contractOutcome != domain.SessionContractOutcomeNone {
		t.Fatalf("contract outcome = %q, want unsettled", st.contractOutcome)
	}
	if len(verifier.requests) != 1 || verifier.requests[0].ProjectID != "ao" || verifier.requests[0].WorkspacePath != "/work/ao" || verifier.requests[0].Env["CI"] != "true" {
		t.Fatalf("verification request = %+v", verifier.requests)
	}
	if len(report.Outputs) != 0 {
		t.Fatalf("verification changed caller outputs: %+v", report.Outputs)
	}
	if len(artifacts.registered) != 1 || artifacts.registered[0] != "ao-7 "+report.Verification.Evidence {
		t.Fatalf("registered = %q", artifacts.registered)
	}
	evidence, err := os.ReadFile(filepath.Join(artifactDir, filepath.FromSlash(report.Verification.Evidence)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(evidence), "Verification: failed") || !strings.Contains(string(evidence), failed.OutputTail) || !strings.Contains(string(evidence), "exit status 1") {
		t.Fatalf("evidence = %q", evidence)
	}
}

func TestCreateDoneRerunsVerificationAndPassSettlesContract(t *testing.T) {
	dataDir := t.TempDir()
	started := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	deadline := started.Add(time.Hour)
	passed := verification.Result{Passed: true, OutputTail: "ok\n", StartedAt: started, CompletedAt: started.Add(time.Second)}
	verifier := &fakeVerifier{results: []verification.Result{passed, passed}, errs: []error{nil, nil}}
	artifacts := &fakeEvidenceArtifacts{}
	st := &fakeStore{
		ok: true, projectOK: true,
		session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker, Goal: "ship it", TimeboxDeadlineAt: &deadline, Metadata: domain.SessionMetadata{WorkspacePath: "/work/ao"}},
		project: domain.ProjectRecord{ID: "ao", Config: domain.ProjectConfig{Verify: []string{"go test ./..."}}},
	}
	ids := []string{"rpt_1", "rpt_2"}
	svc := New(Deps{Store: st, Verifier: verifier, EvidenceArtifacts: artifacts, DataDir: dataDir, NewID: func() string { id := ids[0]; ids = ids[1:]; return id }, Now: func() time.Time { return started }})
	for i := 0; i < 2; i++ {
		report, err := svc.Create(context.Background(), CreateInput{SessionID: "ao-7", State: domain.ReportDone, Note: "done"})
		if err != nil || report.Verification == nil || report.Verification.State != domain.ReportVerificationPassed || report.Verification.OutputTail != "" {
			t.Fatalf("run %d: report=%+v err=%v", i+1, report, err)
		}
	}
	if len(verifier.requests) != 2 || len(artifacts.registered) != 2 {
		t.Fatalf("runs=%d registrations=%d", len(verifier.requests), len(artifacts.registered))
	}
	if st.contractOutcome != domain.SessionContractOutcomeMet {
		t.Fatalf("contract outcome = %q", st.contractOutcome)
	}
}

func TestCreateDoneCleansEvidenceWhenReportPersistenceFails(t *testing.T) {
	dataDir := t.TempDir()
	started := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	verifier := &fakeVerifier{results: []verification.Result{{Passed: true, StartedAt: started, CompletedAt: started}}, errs: []error{nil}}
	artifacts := &fakeEvidenceArtifacts{}
	st := &fakeStore{
		ok: true, projectOK: true, createErr: errors.New("db unavailable"),
		session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/work/ao"}},
		project: domain.ProjectRecord{ID: "ao", Config: domain.ProjectConfig{Verify: []string{"true"}}},
	}
	_, err := New(Deps{Store: st, Verifier: verifier, EvidenceArtifacts: artifacts, DataDir: dataDir, NewID: func() string { return "rpt_1" }, Now: func() time.Time { return started }}).Create(context.Background(), CreateInput{SessionID: "ao-7", State: domain.ReportDone, Note: "done"})
	if !errors.Is(err, st.createErr) || len(artifacts.unregistered) != 1 {
		t.Fatalf("error=%v unregistered=%q", err, artifacts.unregistered)
	}
	path := filepath.Join(dataDir, "artifacts", "ao-7", "verification", "rpt_1.txt")
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("evidence remained after failed report: %v", statErr)
	}
}

func TestCreateDoneRefusesVerificationEvidenceSymlinkEscape(t *testing.T) {
	dataDir := t.TempDir()
	artifactDir := filepath.Join(dataDir, "artifacts", "ao-7")
	outside := t.TempDir()
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(artifactDir, "verification")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	started := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	verifier := &fakeVerifier{
		results: []verification.Result{{Passed: true, StartedAt: started, CompletedAt: started}},
		errs:    []error{nil},
	}
	artifacts := &fakeEvidenceArtifacts{}
	st := &fakeStore{
		ok: true, projectOK: true,
		session: domain.SessionRecord{
			ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker,
			Metadata: domain.SessionMetadata{WorkspacePath: "/work/ao", ArtifactDir: artifactDir},
		},
		project: domain.ProjectRecord{ID: "ao", Config: domain.ProjectConfig{Verify: []string{"true"}}},
	}

	_, err := New(Deps{
		Store: st, Verifier: verifier, EvidenceArtifacts: artifacts, DataDir: dataDir,
		NewID: func() string { return "rpt_1" }, Now: func() time.Time { return started },
	}).Create(context.Background(), CreateInput{SessionID: "ao-7", State: domain.ReportDone, Note: "done"})
	if err == nil {
		t.Fatal("Create succeeded through a symlinked verification directory")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "rpt_1.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("verification evidence escaped artifact directory: %v", statErr)
	}
	if st.created.ID != "" || len(artifacts.registered) != 0 {
		t.Fatalf("failed evidence write persisted report=%q or registered=%q", st.created.ID, artifacts.registered)
	}
}

func TestCreateDoneSkipsEmptyVerifyConfig(t *testing.T) {
	verifier := &fakeVerifier{}
	st := &fakeStore{
		ok: true, projectOK: true,
		session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker},
		project: domain.ProjectRecord{ID: "ao"},
	}
	report, err := New(Deps{Store: st, Verifier: verifier, NewID: func() string { return "rpt_1" }}).Create(context.Background(), CreateInput{SessionID: "ao-7", State: domain.ReportDone, Note: "done"})
	if err != nil || report.Verification != nil || len(verifier.requests) != 0 {
		t.Fatalf("report=%+v runs=%d err=%v", report, len(verifier.requests), err)
	}
}

func TestCreateDoneCancellationDoesNotPersistReport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	verifier := &fakeVerifier{
		results: []verification.Result{{Passed: false}},
		errs:    []error{context.Canceled},
	}
	st := &fakeStore{
		ok: true, projectOK: true,
		session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/work/ao"}},
		project: domain.ProjectRecord{ID: "ao", Config: domain.ProjectConfig{Verify: []string{"go test ./..."}}},
	}
	_, err := New(Deps{Store: st, Verifier: verifier, EvidenceArtifacts: &fakeEvidenceArtifacts{}, DataDir: t.TempDir(), NewID: func() string { return "rpt_1" }}).Create(ctx, CreateInput{SessionID: "ao-7", State: domain.ReportDone, Note: "done"})
	if !errors.Is(err, context.Canceled) || st.created.ID != "" {
		t.Fatalf("error=%v persisted=%+v", err, st.created)
	}
}
func (f *fakeStore) ListReportsByProject(context.Context, domain.ProjectID) ([]domain.ReportRecord, error) {
	return append([]domain.ReportRecord(nil), f.reports...), nil
}

func TestListProjectIsReadOnlyAndOrdered(t *testing.T) {
	at := time.Date(2026, 9, 15, 1, 2, 3, 0, time.UTC)
	outputs := []domain.ReportOutput{
		{Kind: domain.ReportOutputArtifact, Reference: "first"},
		{Kind: domain.ReportOutputArtifact, Reference: "second"},
	}
	st := &fakeStore{reports: []domain.ReportRecord{
		{ID: "c", CreatedAt: at.Add(time.Second)},
		{ID: "b", CreatedAt: at, Outputs: outputs},
		{ID: "a", CreatedAt: at},
	}}
	got, err := New(Deps{Store: st}).ListProject(context.Background(), "ao")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
		t.Fatalf("report order = %+v", got)
	}
	if len(got[1].Outputs) != 2 || got[1].Outputs[0].Reference != "first" || got[1].Outputs[1].Reference != "second" {
		t.Fatalf("output position order = %+v", got[1].Outputs)
	}
}

func TestCreateDerivesOwnershipAndFixedDeliveryDeadlines(t *testing.T) {
	now := time.Date(2026, 9, 7, 1, 2, 3, 0, time.UTC)
	tests := []struct {
		name      string
		input     CreateInput
		available time.Time
		settles   time.Time
	}{
		{name: "free form batches for one hour", input: CreateInput{Message: "status"}, available: now.Add(time.Hour)},
		{name: "output only batches for one hour", input: CreateInput{Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "result"}}}, available: now.Add(time.Hour)},
		{name: "checkpoint batches for one hour", input: CreateInput{State: domain.ReportCheckpoint, Note: "checkpoint"}, available: now.Add(time.Hour)},
		{name: "needs input is immediate", input: CreateInput{State: domain.ReportNeedsInput, Note: "decision"}, available: now},
		{name: "stuck is immediate", input: CreateInput{State: domain.ReportStuck, Note: "blocked"}, available: now},
		{name: "done settles for five minutes", input: CreateInput{State: domain.ReportDone, Note: "finished"}, available: now.Add(5 * time.Minute), settles: now.Add(5 * time.Minute)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker}}
			s := New(Deps{Store: st, Now: func() time.Time { return now }, NewID: func() string { return "rpt_1" }})
			tc.input.SessionID = "ao-7"
			r, err := s.Create(context.Background(), tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if r.ID != "rpt_1" || r.ProjectID != "ao" || r.DeliveryState != domain.ReportPending || r.RepeatCount != 1 || !r.AvailableAt.Equal(tc.available) || !r.SettlementDeadline.Equal(tc.settles) {
				t.Fatalf("report=%+v", r)
			}
		})
	}
}

func TestCreateImmediateOnceIsReplaySafe(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	st := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker}}
	created := 0
	svc := New(Deps{Store: st, Now: func() time.Time { return now }, OnCreated: func(domain.ReportRecord) { created++ }})
	input := CreateInput{SessionID: "ao-7", Message: "worker stopped without reporting"}
	first, err := svc.CreateImmediateOnce(context.Background(), input, "session-contract-stall:ao-7:1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateImmediateOnce(context.Background(), input, "session-contract-stall:ao-7:1")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.ID != second.ID || !first.AvailableAt.Equal(now) || first.DeliveryState != domain.ReportPending {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if st.insertions != 1 || created != 1 {
		t.Fatalf("insertions=%d onCreated=%d, want one", st.insertions, created)
	}
}

func TestCreateMapsTerminalContractReportsToOutcome(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(time.Hour)
	for _, tc := range []struct {
		name    string
		state   domain.ReportState
		note    string
		message string
		goal    string
		want    domain.SessionContractOutcome
	}{
		{name: "done meets contract", state: domain.ReportDone, note: "finished", goal: "ship it", want: domain.SessionContractOutcomeMet},
		{name: "needs input blocks contract", state: domain.ReportNeedsInput, note: "decision", goal: "ship it", want: domain.SessionContractOutcomeBlocked},
		{name: "stuck blocks contract", state: domain.ReportStuck, note: "blocked", goal: "ship it", want: domain.SessionContractOutcomeBlocked},
		{name: "checkpoint leaves contract open", state: domain.ReportCheckpoint, note: "working", goal: "ship it"},
		{name: "message leaves contract open", message: "progress", goal: "ship it"},
		{name: "ordinary worker has no contract outcome", state: domain.ReportDone, note: "finished"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{ok: true, session: domain.SessionRecord{
				ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker,
				Goal: tc.goal, TimeboxDeadlineAt: func() *time.Time {
					if tc.goal == "" {
						return nil
					}
					return &deadline
				}(),
			}}
			_, err := New(Deps{Store: st, Now: func() time.Time { return now }}).Create(context.Background(), CreateInput{
				SessionID: "ao-7", State: tc.state, Note: tc.note, Message: tc.message,
			})
			if err != nil {
				t.Fatal(err)
			}
			if st.contractOutcome != tc.want {
				t.Fatalf("contract outcome = %q, want %q", st.contractOutcome, tc.want)
			}
		})
	}
}

func TestCreateDefendsValidationAndOwnership(t *testing.T) {
	worker := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-2", ProjectID: "ao", Kind: domain.KindWorker}}
	for _, tc := range []struct {
		name  string
		store *fakeStore
		input CreateInput
	}{
		{"missing session", worker, CreateInput{Message: "message"}},
		{"unknown session", &fakeStore{}, CreateInput{SessionID: "ao-2", State: domain.ReportDone, Note: "done"}},
		{"orchestrator", &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-1", ProjectID: "ao", Kind: domain.KindOrchestrator}}, CreateInput{SessionID: "ao-1", State: domain.ReportDone, Note: "done"}},
		{"bad state", worker, CreateInput{SessionID: "ao-2", State: "bad", Note: "note"}},
		{"bad PR", worker, CreateInput{SessionID: "ao-2", Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputPRCreated, Reference: "https://example.com/x"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Deps{Store: tc.store}).Create(context.Background(), tc.input)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

type artifactCalls []string

func (a *artifactCalls) RecordReportedArtifact(_ context.Context, id domain.SessionID, reference string) {
	*a = append(*a, string(id)+" "+reference)
}

func TestCreateShowsEachReportedArtifactAfterTheReportIsStored(t *testing.T) {
	st := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker}}
	var calls artifactCalls
	r, err := New(Deps{Store: st, Artifacts: &calls}).Create(context.Background(), CreateInput{
		SessionID: "ao-7", State: domain.ReportDone, Note: "done",
		Outputs: []domain.ReportOutput{
			{Kind: domain.ReportOutputArtifact, Reference: "report.html"},
			{Kind: domain.ReportOutputPRCreated, Reference: "https://github.com/o/r/pull/1"},
			{Kind: domain.ReportOutputArtifact, Reference: "/abs/notes.md"},
		},
	})
	if err != nil || r.ID == "" || st.created.ID != r.ID {
		t.Fatalf("report = %+v, %v", r, err)
	}
	if len(calls) != 2 || calls[0] != "ao-7 report.html" || calls[1] != "ao-7 /abs/notes.md" {
		t.Fatalf("recorded = %q, want the two artifacts in order", calls)
	}

	// A refused report shows nothing.
	calls = nil
	if _, err := New(Deps{Store: &fakeStore{}, Artifacts: &calls}).Create(context.Background(), CreateInput{
		SessionID: "ao-7", Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "report.html"}},
	}); err == nil || len(calls) != 0 {
		t.Fatalf("unknown session: err = %v, recorded %q", err, calls)
	}
}
