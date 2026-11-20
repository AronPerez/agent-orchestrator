package decision

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeNotificationLedger is the decision-call read model the projector scans.
type fakeNotificationLedger struct {
	mu    sync.Mutex
	calls map[string]domain.DecisionCall
	err   error
}

func (f *fakeNotificationLedger) ListDecisionCallsByState(_ context.Context, state domain.DecisionCallState, limit int) ([]domain.DecisionCall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if limit != -1 {
		return nil, errors.New("projector must scan every call, not a page")
	}
	var out []domain.DecisionCall
	for _, call := range f.calls {
		if call.State == state {
			out = append(out, call)
		}
	}
	return out, nil
}

func (f *fakeNotificationLedger) set(call domain.DecisionCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]domain.DecisionCall{}
	}
	f.calls[call.ID] = call
}

// fakeNotificationWriter mirrors the manager/store contract: one row per call
// source key, resolution of every open row whose call left Needs you.
type fakeNotificationWriter struct {
	mu        sync.Mutex
	ledger    *fakeNotificationLedger
	rows      map[string]*domain.NotificationRecord
	intents   []ports.NotificationIntent
	failNext  int
	block     chan struct{}
	reconcile int
}

func (w *fakeNotificationWriter) Notify(_ context.Context, intent ports.NotificationIntent) error {
	if w.block != nil {
		<-w.block
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failNext > 0 {
		w.failNext--
		return errors.New("store unavailable")
	}
	w.intents = append(w.intents, intent)
	if w.rows == nil {
		w.rows = map[string]*domain.NotificationRecord{}
	}
	if _, ok := w.rows[intent.SourceKey]; ok {
		return nil
	}
	w.rows[intent.SourceKey] = &domain.NotificationRecord{
		Type: intent.Type, DecisionCallID: intent.DecisionCallID, SourceKey: intent.SourceKey,
	}
	return nil
}

func (w *fakeNotificationWriter) Reconcile(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reconcile++
	w.ledger.mu.Lock()
	defer w.ledger.mu.Unlock()
	for _, row := range w.rows {
		call, ok := w.ledger.calls[row.DecisionCallID]
		if !row.Resolved() && (!ok || call.State != domain.DecisionStateNeedsYou) {
			row.ResolvedAt = time.Unix(1, 0)
		}
	}
	return nil
}

func (w *fakeNotificationWriter) snapshot() (open, resolved int, intents []ports.NotificationIntent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, row := range w.rows {
		if row.Resolved() {
			resolved++
		} else {
			open++
		}
	}
	return open, resolved, append([]ports.NotificationIntent(nil), w.intents...)
}

func needsYouCall(id string) domain.DecisionCall {
	return domain.DecisionCall{
		ID: id, Origin: "h_origin", State: domain.DecisionStateNeedsYou, RaisedAt: time.Unix(100, 0),
		Raised: domain.DecisionRaisedPayload{
			Title: "Ship the bell badge now?", Context: "SECRET CONTEXT",
			Options:   []domain.DecisionOption{{Label: "SECRET OPTION"}, {Label: "other"}},
			ProjectID: "remote-project", ProjectName: "Shop", StreamSessionID: "remote-stream",
			StreamName: "Checkout", OwnerSessionID: "remote-owner", MachineLabel: "mac-mini",
		},
	}
}

func newTestProjector(ledger *fakeNotificationLedger, writer *fakeNotificationWriter) *NotificationProjector {
	return NewNotificationProjector(NotificationProjectorDeps{
		Store: ledger, Writer: writer, RetryDelay: 10 * time.Millisecond,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestDecisionNotificationProjectorCreatesNeedsYouOnce(t *testing.T) {
	ledger := &fakeNotificationLedger{}
	ledger.set(needsYouCall("dec_1"))
	writer := &fakeNotificationWriter{ledger: ledger}
	p := newTestProjector(ledger, writer)
	for range 3 {
		if err := p.Reconcile(context.Background()); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
	}
	open, resolved, intents := writer.snapshot()
	if open != 1 || resolved != 0 {
		t.Fatalf("open=%d resolved=%d", open, resolved)
	}
	got := intents[0]
	if got.Type != domain.NotificationDecisionNeeded || got.DecisionCallID != "dec_1" ||
		got.DecisionOriginHostID != "h_origin" || got.SourceKey != "decision:dec_1" {
		t.Fatalf("intent = %+v", got)
	}
}

func TestDecisionNotificationProjectorResolvesAfterAnswer(t *testing.T) {
	ledger := &fakeNotificationLedger{}
	ledger.set(needsYouCall("dec_1"))
	writer := &fakeNotificationWriter{ledger: ledger}
	p := newTestProjector(ledger, writer)
	if err := p.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	answered := needsYouCall("dec_1")
	answered.State = domain.DecisionStateInProgress
	ledger.set(answered)
	if err := p.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	open, resolved, intents := writer.snapshot()
	if open != 0 || resolved != 1 || len(intents) != 1 {
		t.Fatalf("open=%d resolved=%d intents=%d", open, resolved, len(intents))
	}
}

func TestDecisionNotificationProjectorRepairsMissedCreateOnStartup(t *testing.T) {
	// The call committed before a crash; no notification exists yet. The first
	// pass of Run (startup) must create it without any append wake.
	ledger := &fakeNotificationLedger{}
	ledger.set(needsYouCall("dec_1"))
	writer := &fakeNotificationWriter{ledger: ledger}
	p := newTestProjector(ledger, writer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	waitFor(t, func() bool { open, _, _ := writer.snapshot(); return open == 1 })
	cancel()
	<-done
}

func TestDecisionNotificationProjectorRetriesWithoutDuplicating(t *testing.T) {
	ledger := &fakeNotificationLedger{}
	ledger.set(needsYouCall("dec_1"))
	ledger.set(needsYouCall("dec_2"))
	writer := &fakeNotificationWriter{ledger: ledger, failNext: 1}
	p := newTestProjector(ledger, writer)
	if err := p.Reconcile(context.Background()); err == nil {
		t.Fatal("a store failure must surface so the run loop retries")
	}
	// The failure did not stop the other call or the stale-resolution pass.
	if open, _, _ := writer.snapshot(); open != 1 || writer.reconcile != 1 {
		t.Fatalf("after failed pass open=%d reconcile=%d", open, writer.reconcile)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer.mu.Lock()
	writer.failNext = 1
	writer.mu.Unlock()
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	waitFor(t, func() bool { open, _, _ := writer.snapshot(); return open == 2 })
	cancel()
	<-done
	if open, _, _ := writer.snapshot(); open != 2 {
		t.Fatalf("retries duplicated rows: open=%d", open)
	}
}

func TestDecisionNotificationProjectorUsesRecordedLabelsWithoutSessionLookup(t *testing.T) {
	ledger := &fakeNotificationLedger{}
	ledger.set(needsYouCall("dec_1"))
	writer := &fakeNotificationWriter{ledger: ledger}
	// The projector has no session/project store at all: labels come from the
	// immutable raised payload, so a remote call needs nothing local.
	if err := newTestProjector(ledger, writer).Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, intents := writer.snapshot()
	got := intents[0]
	if got.DecisionProjectName != "Shop" || got.DecisionStreamName != "Checkout" ||
		got.DecisionTitle != "Ship the bell badge now?" || got.ProjectID != "remote-project" {
		t.Fatalf("labels = %+v", got)
	}
	if got.SessionID != "" || got.PRURL != "" || got.SessionDisplayName != "" {
		t.Fatalf("intent carried a local target: %+v", got)
	}
}

func TestDecisionNotificationProjectorDoesNotNotifyForInProgressDoneDismissedWithdrawn(t *testing.T) {
	ledger := &fakeNotificationLedger{}
	for _, state := range []domain.DecisionCallState{
		domain.DecisionStateInProgress, domain.DecisionStateDone, domain.DecisionStateDismissed, domain.DecisionStateWithdrawn,
	} {
		call := needsYouCall("dec_" + string(state))
		call.State = state
		ledger.set(call)
	}
	writer := &fakeNotificationWriter{ledger: ledger}
	if err := newTestProjector(ledger, writer).Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, intents := writer.snapshot(); len(intents) != 0 {
		t.Fatalf("intents = %+v", intents)
	}
}

func TestDecisionAppendWakeDoesNotBlockOnNotificationIO(t *testing.T) {
	ledger := &fakeNotificationLedger{}
	ledger.set(needsYouCall("dec_1"))
	writer := &fakeNotificationWriter{ledger: ledger, block: make(chan struct{})}
	p := newTestProjector(ledger, writer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()

	// Notification I/O is stuck; an append's wake must still return at once.
	woke := make(chan struct{})
	go func() {
		for range 100 {
			p.Wake()
		}
		close(woke)
	}()
	select {
	case <-woke:
	case <-time.After(time.Second):
		t.Fatal("Wake blocked on notification I/O")
	}
	close(writer.block)
	cancel()
	<-done
}

func TestDecisionNotificationProjectorWakeAfterRaise(t *testing.T) {
	ledger := &fakeNotificationLedger{}
	writer := &fakeNotificationWriter{ledger: ledger}
	p := newTestProjector(ledger, writer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	waitFor(t, func() bool { writer.mu.Lock(); defer writer.mu.Unlock(); return writer.reconcile >= 1 })
	ledger.set(needsYouCall("dec_new"))
	p.Wake()
	waitFor(t, func() bool { open, _, _ := writer.snapshot(); return open == 1 })
	cancel()
	<-done
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func catchUpProjector(ledger *fakeNotificationLedger, writer *fakeNotificationWriter, now time.Time) *NotificationProjector {
	return NewNotificationProjector(NotificationProjectorDeps{
		Store: ledger, Writer: writer, RetryDelay: 10 * time.Millisecond, HostID: "h_local",
		Now: func() time.Time { return now }, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestProjectorMarksCatchUpCallsRead(t *testing.T) {
	ledger := &fakeNotificationLedger{}
	old, recent := needsYouCall("dec_old"), needsYouCall("dec_recent")
	now := time.Unix(100_000, 0)
	old.RaisedAt = now.Add(-10*time.Minute - time.Second)
	recent.RaisedAt = now.Add(-10*time.Minute + time.Second)
	ledger.set(old)
	ledger.set(recent)
	writer := &fakeNotificationWriter{ledger: ledger}
	if err := catchUpProjector(ledger, writer, now).Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, intents := writer.snapshot()
	read := map[string]bool{}
	for _, intent := range intents {
		read[intent.DecisionCallID] = intent.InitiallyRead
	}
	if !read["dec_old"] || read["dec_recent"] || len(read) != 2 {
		t.Fatalf("initially read = %v, want only the call raised over 10 minutes ago", read)
	}
}

func TestProjectorNeverMarksLocalCallsRead(t *testing.T) {
	ledger := &fakeNotificationLedger{}
	local := needsYouCall("dec_local")
	local.Origin = "h_local"
	local.RaisedAt = time.Unix(1, 0)
	ledger.set(local)
	writer := &fakeNotificationWriter{ledger: ledger}
	if err := catchUpProjector(ledger, writer, time.Unix(100_000, 0)).Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, intents := writer.snapshot(); len(intents) != 1 || intents[0].InitiallyRead {
		t.Fatalf("intents = %+v, want this machine's own call unread", intents)
	}
}
