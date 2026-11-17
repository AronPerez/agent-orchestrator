package sessioncontract

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeStore struct {
	sessions map[domain.SessionID]domain.SessionRecord
	projects map[string]domain.ProjectRecord
	prs      map[domain.SessionID][]domain.PullRequest
	comments map[string][]domain.PullRequestComment
	listed   chan struct{}
	list     []domain.SessionRecord
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := f.sessions[id]
	return rec, ok, nil
}

func (f *fakeStore) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	if f.listed != nil {
		select {
		case f.listed <- struct{}{}:
		default:
		}
	}
	if f.list != nil {
		return append([]domain.SessionRecord(nil), f.list...), nil
	}
	out := make([]domain.SessionRecord, 0, len(f.sessions))
	for _, rec := range f.sessions {
		out = append(out, rec)
	}
	return out, nil
}

func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	rec, ok := f.projects[id]
	return rec, ok, nil
}

func (f *fakeStore) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	return append([]domain.PullRequest(nil), f.prs[id]...), nil
}

func (f *fakeStore) ListPRComments(_ context.Context, url string) ([]domain.PullRequestComment, error) {
	return append([]domain.PullRequestComment(nil), f.comments[url]...), nil
}

func (f *fakeStore) RecordSessionContractStallNudge(_ context.Context, id domain.SessionID, at time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.ContractOutcome != domain.SessionContractOutcomeNone {
		return false, nil
	}
	rec.ContractStallNudgedAt = &at
	rec.ContractStallEscalatedAt = nil
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) RecordSessionContractStallEscalation(_ context.Context, id domain.SessionID, nudgedAt, at time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.ContractStallNudgedAt == nil || !rec.ContractStallNudgedAt.Equal(nudgedAt) || rec.ContractStallEscalatedAt != nil {
		return false, nil
	}
	rec.ContractStallEscalatedAt = &at
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) ExpireSessionContract(_ context.Context, id domain.SessionID, deadline, at time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.ContractOutcome != domain.SessionContractOutcomeNone || rec.TimeboxDeadlineAt == nil || !rec.TimeboxDeadlineAt.Equal(deadline) {
		return false, nil
	}
	rec.ContractOutcome = domain.SessionContractOutcomeLimitHit
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) RecordSessionContractTimeboxPrompt(_ context.Context, id domain.SessionID, deadline, at time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.ContractOutcome != domain.SessionContractOutcomeLimitHit || rec.ContractTimeboxPromptedAt != nil || rec.TimeboxDeadlineAt == nil || !rec.TimeboxDeadlineAt.Equal(deadline) {
		return false, nil
	}
	rec.ContractTimeboxPromptedAt = &at
	f.sessions[id] = rec
	return true, nil
}

type fakeNudger struct {
	sent    bool
	err     error
	blockID domain.SessionID
	mu      sync.Mutex
	calls   []string
}

func (f *fakeNudger) NudgeSession(ctx context.Context, id domain.SessionID, key, message string) (bool, error) {
	f.mu.Lock()
	f.calls = append(f.calls, string(id)+" "+key+" "+message)
	f.mu.Unlock()
	if id == f.blockID {
		<-ctx.Done()
		return false, ctx.Err()
	}
	return f.sent, f.err
}

func (f *fakeNudger) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeNudger) NudgeIdleSession(ctx context.Context, id domain.SessionID, key, message string) (bool, error) {
	return f.NudgeSession(ctx, id, key, message)
}

type fakeReporter struct {
	calls []string
}

func (f *fakeReporter) ReportSessionContractStall(_ context.Context, id domain.SessionID, key, message string) error {
	f.calls = append(f.calls, string(id)+" "+key+" "+message)
	return nil
}

type fakeNotifier struct {
	intents []ports.NotificationIntent
	err     error
}

func (f *fakeNotifier) Notify(_ context.Context, intent ports.NotificationIntent) error {
	f.intents = append(f.intents, intent)
	return f.err
}

func contractFixture(now time.Time) (*fakeStore, domain.SessionID) {
	id := domain.SessionID("worker-1")
	deadline := now.Add(time.Hour)
	return &fakeStore{
		sessions: map[domain.SessionID]domain.SessionRecord{id: {
			ID: id, ProjectID: "ao", Kind: domain.KindWorker, Goal: "ship it", TimeboxDeadlineAt: &deadline,
			Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-20 * time.Minute)},
		}},
		projects: map[string]domain.ProjectRecord{"ao": {ID: "ao", Config: domain.ProjectConfig{WorkerContracts: domain.WorkerContractConfig{StallMinutes: 10}}}},
		prs:      map[domain.SessionID][]domain.PullRequest{id: {{URL: "pr-1", CI: domain.CIFailing, Mergeability: domain.MergeMergeable}}},
		comments: map[string][]domain.PullRequestComment{},
	}, id
}

func TestEvaluateSessionNudgesOneStallEpisode(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	store, id := contractFixture(now)
	nudger := &fakeNudger{sent: true}
	c := New(store, nudger, nil, nil, Config{Clock: func() time.Time { return now }})

	result, err := c.EvaluateSession(context.Background(), id)
	if err != nil || result.Action != ActionNudged || len(nudger.calls) != 1 {
		t.Fatalf("first result=%+v calls=%v err=%v", result, nudger.calls, err)
	}
	rec := store.sessions[id]
	if rec.ContractStallNudgedAt == nil || !rec.ContractStallNudgedAt.Equal(now) {
		t.Fatalf("nudge stamp = %v, want %v", rec.ContractStallNudgedAt, now)
	}
	result, err = c.EvaluateSession(context.Background(), id)
	if err != nil || result.Action != ActionNone || len(nudger.calls) != 1 {
		t.Fatalf("repeat result=%+v calls=%v err=%v", result, nudger.calls, err)
	}
}

func TestEvaluateSessionSuppressedNudgeDoesNotConsumeEpisode(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	store, id := contractFixture(now)
	nudger := &fakeNudger{}
	result, err := New(store, nudger, nil, nil, Config{Clock: func() time.Time { return now }}).EvaluateSession(context.Background(), id)
	if err != nil || result.Reason != "nudge_suppressed" || store.sessions[id].ContractStallNudgedAt != nil {
		t.Fatalf("result=%+v session=%+v err=%v", result, store.sessions[id], err)
	}
}

func TestEvaluateSessionEscalatesUnchangedStallAfterOneMoreThreshold(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	store, id := contractFixture(now)
	nudgedAt := now.Add(-21 * time.Minute)
	rec := store.sessions[id]
	rec.ContractStallNudgedAt = &nudgedAt
	store.sessions[id] = rec
	reporter := &fakeReporter{}
	notifier := &fakeNotifier{}
	result, err := New(store, &fakeNudger{sent: true}, reporter, notifier, Config{Clock: func() time.Time { return now }}).EvaluateSession(context.Background(), id)
	if err != nil || result.Action != ActionEscalated {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(reporter.calls) != 1 || len(notifier.intents) != 1 || notifier.intents[0].Type != domain.NotificationSessionStalled {
		t.Fatalf("reports=%v notifications=%+v", reporter.calls, notifier.intents)
	}
	if got := store.sessions[id].ContractStallEscalatedAt; got == nil || !got.Equal(now) {
		t.Fatalf("escalated at = %v, want %v", got, now)
	}
}

func TestEvaluateSessionEscalationNotifiesBeforeCreatingReport(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	store, id := contractFixture(now)
	nudgedAt := now.Add(-11 * time.Minute)
	rec := store.sessions[id]
	rec.ContractStallNudgedAt = &nudgedAt
	store.sessions[id] = rec
	reporter := &fakeReporter{}
	notifier := &fakeNotifier{err: errors.New("notification unavailable")}
	c := New(store, &fakeNudger{sent: true}, reporter, notifier, Config{Clock: func() time.Time { return now }})

	if _, err := c.EvaluateSession(context.Background(), id); err == nil {
		t.Fatal("expected notification error")
	}
	if len(reporter.calls) != 0 || store.sessions[id].ContractStallEscalatedAt != nil {
		t.Fatalf("reporter calls=%v session=%+v, want no report or escalation stamp", reporter.calls, store.sessions[id])
	}

	notifier.err = nil
	result, err := c.EvaluateSession(context.Background(), id)
	if err != nil || result.Action != ActionEscalated || len(reporter.calls) != 1 {
		t.Fatalf("retry result=%+v reporter calls=%v err=%v", result, reporter.calls, err)
	}
}

func TestEvaluateSessionActivityAfterNudgeStartsNewEpisode(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	store, id := contractFixture(now)
	oldNudge := now.Add(-30 * time.Minute)
	rec := store.sessions[id]
	rec.ContractStallNudgedAt = &oldNudge
	rec.Activity.LastActivityAt = now.Add(-20 * time.Minute)
	rec.Metadata.LatestUserPromptAt = now.Add(-20 * time.Minute)
	store.sessions[id] = rec
	nudger := &fakeNudger{sent: true}
	result, err := New(store, nudger, nil, nil, Config{Clock: func() time.Time { return now }}).EvaluateSession(context.Background(), id)
	if err != nil || result.Action != ActionNudged || len(nudger.calls) != 1 {
		t.Fatalf("result=%+v calls=%v err=%v", result, nudger.calls, err)
	}
}

func TestEvaluateSessionNudgeGeneratedTurnDoesNotStartNewEpisode(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	store, id := contractFixture(now)
	nudgedAt := now.Add(-11 * time.Minute)
	rec := store.sessions[id]
	rec.ContractStallNudgedAt = &nudgedAt
	// The automatic nudge started and completed a turn, advancing activity, but
	// no human or peer supplied fresh direction.
	rec.Activity.LastActivityAt = now.Add(-11 * time.Minute)
	store.sessions[id] = rec
	nudger := &fakeNudger{sent: true}
	reporter := &fakeReporter{}
	result, err := New(store, nudger, reporter, &fakeNotifier{}, Config{Clock: func() time.Time { return now }}).EvaluateSession(context.Background(), id)
	if err != nil || result.Action != ActionEscalated || len(nudger.calls) != 0 || len(reporter.calls) != 1 {
		t.Fatalf("result=%+v nudges=%v reports=%v err=%v", result, nudger.calls, reporter.calls, err)
	}
}

func TestEvaluateSessionExpiresTimeboxExactlyOnce(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	store, id := contractFixture(now)
	rec := store.sessions[id]
	deadline := now.Add(-time.Second)
	rec.TimeboxDeadlineAt = &deadline
	rec.Activity.State = domain.ActivityActive
	store.sessions[id] = rec
	nudger := &fakeNudger{sent: true}
	c := New(store, nudger, nil, nil, Config{Clock: func() time.Time { return now }})
	result, err := c.EvaluateSession(context.Background(), id)
	if err != nil || result.Action != ActionLimitHit || store.sessions[id].ContractOutcome != domain.SessionContractOutcomeLimitHit || len(nudger.calls) != 1 {
		t.Fatalf("first result=%+v session=%+v calls=%v err=%v", result, store.sessions[id], nudger.calls, err)
	}
	result, err = c.EvaluateSession(context.Background(), id)
	if err != nil || result.Action != ActionNone || len(nudger.calls) != 1 {
		t.Fatalf("repeat result=%+v calls=%v err=%v", result, nudger.calls, err)
	}
}

func TestEvaluateSessionRetriesSuppressedTimeboxPrompt(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	store, id := contractFixture(now)
	rec := store.sessions[id]
	deadline := now.Add(-time.Second)
	rec.TimeboxDeadlineAt = &deadline
	rec.Activity.State = domain.ActivityWaitingInput
	store.sessions[id] = rec
	nudger := &fakeNudger{}
	c := New(store, nudger, nil, nil, Config{Clock: func() time.Time { return now }})

	result, err := c.EvaluateSession(context.Background(), id)
	if err != nil || result.Action != ActionLimitHit || result.Reason != "timebox_prompt_pending" {
		t.Fatalf("first result=%+v err=%v", result, err)
	}
	got := store.sessions[id]
	if got.ContractOutcome != domain.SessionContractOutcomeLimitHit || got.ContractTimeboxPromptedAt != nil || len(nudger.calls) != 1 {
		t.Fatalf("first session=%+v calls=%v", got, nudger.calls)
	}

	nudger.sent = true
	result, err = c.EvaluateSession(context.Background(), id)
	if err != nil || result.Action != ActionTimeboxPrompted || result.Reason != "" {
		t.Fatalf("retry result=%+v err=%v", result, err)
	}
	got = store.sessions[id]
	if got.ContractTimeboxPromptedAt == nil || !got.ContractTimeboxPromptedAt.Equal(now) || len(nudger.calls) != 2 {
		t.Fatalf("retry session=%+v calls=%v", got, nudger.calls)
	}

	result, err = c.EvaluateSession(context.Background(), id)
	if err != nil || result.Reason != "contract_finished" || len(nudger.calls) != 2 {
		t.Fatalf("settled result=%+v calls=%v err=%v", result, nudger.calls, err)
	}
}

func TestEvaluateSessionStallGates(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	ready := domain.PullRequest{URL: "pr-1", CI: domain.CIPassing, Mergeability: domain.MergeMergeable}
	for _, tc := range []struct {
		name   string
		mutate func(*fakeStore, domain.SessionID)
		reason string
	}{
		{name: "not worker", mutate: func(s *fakeStore, id domain.SessionID) {
			r := s.sessions[id]
			r.Kind = domain.KindOrchestrator
			s.sessions[id] = r
		}, reason: "not_worker"},
		{name: "terminated", mutate: func(s *fakeStore, id domain.SessionID) {
			r := s.sessions[id]
			r.IsTerminated = true
			s.sessions[id] = r
		}, reason: "terminated"},
		{name: "active", mutate: func(s *fakeStore, id domain.SessionID) {
			r := s.sessions[id]
			r.Activity.State = domain.ActivityActive
			s.sessions[id] = r
		}, reason: "not_idle"},
		{name: "waiting for input", mutate: func(s *fakeStore, id domain.SessionID) {
			r := s.sessions[id]
			r.Activity.State = domain.ActivityWaitingInput
			s.sessions[id] = r
		}, reason: "not_idle"},
		{name: "outcome recorded", mutate: func(s *fakeStore, id domain.SessionID) {
			r := s.sessions[id]
			r.ContractOutcome = domain.SessionContractOutcomeMet
			s.sessions[id] = r
		}, reason: "contract_finished"},
		{name: "no contract", mutate: func(s *fakeStore, id domain.SessionID) {
			r := s.sessions[id]
			r.Goal = ""
			r.TimeboxDeadlineAt = nil
			s.sessions[id] = r
		}, reason: "no_contract"},
		{name: "below threshold", mutate: func(s *fakeStore, id domain.SessionID) {
			r := s.sessions[id]
			r.Activity.LastActivityAt = now.Add(-9 * time.Minute)
			s.sessions[id] = r
		}, reason: "stall_threshold_not_met"},
		{name: "waiting for CI", mutate: func(s *fakeStore, id domain.SessionID) {
			s.prs[id] = []domain.PullRequest{{URL: "pr-1", CI: domain.CIPending, Mergeability: domain.MergeMergeable}}
		}, reason: "waiting_on_ci_or_review"},
		{name: "waiting for review", mutate: func(s *fakeStore, id domain.SessionID) {
			s.prs[id] = []domain.PullRequest{{URL: "pr-1", CI: domain.CIPassing, Review: domain.ReviewRequired, Mergeability: domain.MergeBlocked}}
		}, reason: "waiting_on_ci_or_review"},
		{name: "actionable review feedback wins over pending CI", mutate: func(s *fakeStore, id domain.SessionID) {
			s.prs[id] = []domain.PullRequest{{URL: "pr-1", CI: domain.CIPending, Review: domain.ReviewChangesRequest, Mergeability: domain.MergeBlocked}}
		}},
		{name: "ready PR", mutate: func(s *fakeStore, id domain.SessionID) { s.prs[id] = []domain.PullRequest{ready} }, reason: "work_ready"},
		{name: "merged PR set behaves like no open PR", mutate: func(s *fakeStore, id domain.SessionID) {
			s.prs[id] = []domain.PullRequest{{URL: "pr-1", Merged: true, Closed: true}}
		}},
		{name: "closed unmerged PR set behaves like no open PR", mutate: func(s *fakeStore, id domain.SessionID) {
			s.prs[id] = []domain.PullRequest{{URL: "pr-1", Closed: true}}
		}},
		{name: "ready PR with unresolved comment is still open", mutate: func(s *fakeStore, id domain.SessionID) {
			s.prs[id] = []domain.PullRequest{ready}
			s.comments[ready.URL] = []domain.PullRequestComment{{File: "x.go", Line: 1}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, id := contractFixture(now)
			tc.mutate(store, id)
			nudger := &fakeNudger{sent: true}
			result, err := New(store, nudger, nil, nil, Config{Clock: func() time.Time { return now }}).EvaluateSession(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := 0
			if tc.reason == "" {
				wantCalls = 1
			}
			if result.Reason != tc.reason || len(nudger.calls) != wantCalls {
				t.Fatalf("result=%+v calls=%v, want reason=%q calls=%d", result, nudger.calls, tc.reason, wantCalls)
			}
		})
	}
}

func TestStartWaitsForStartupReconciliation(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	store, _ := contractFixture(now)
	store.listed = make(chan struct{}, 1)
	start := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := New(store, nil, nil, nil, Config{
		Clock: func() time.Time { return now }, SweepInterval: time.Millisecond, StartAfter: start,
	}).Start(ctx)

	select {
	case <-store.listed:
		t.Fatal("contract coordinator polled before startup reconciliation")
	case <-time.After(20 * time.Millisecond):
	}
	close(start)
	select {
	case <-store.listed:
	case <-time.After(time.Second):
		t.Fatal("contract coordinator did not poll after startup reconciliation")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("contract coordinator did not stop after cancellation")
	}
}

func TestSweepBoundsEachWorkerEvaluation(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	store, firstID := contractFixture(now)
	first := store.sessions[firstID]
	firstDeadline := now.Add(-time.Minute)
	first.TimeboxDeadlineAt = &firstDeadline
	first.ContractOutcome = domain.SessionContractOutcomeLimitHit
	store.sessions[firstID] = first

	secondID := domain.SessionID("worker-2")
	second := first
	second.ID = secondID
	store.sessions[secondID] = second
	store.list = []domain.SessionRecord{first, second}
	nudger := &fakeNudger{blockID: firstID}
	c := New(store, nudger, nil, nil, Config{
		Clock: func() time.Time { return now }, EvaluationTimeout: 5 * time.Millisecond,
	})

	if err := c.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if calls := nudger.callLog(); len(calls) != 2 {
		t.Fatalf("nudge calls = %v, want both workers evaluated", calls)
	}
}
