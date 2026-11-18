package daemon

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type wiringDecisionLedger struct {
	mu    sync.Mutex
	calls []domain.DecisionCall
}

func (l *wiringDecisionLedger) ListDecisionCallsByState(_ context.Context, state domain.DecisionCallState, _ int) ([]domain.DecisionCall, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []domain.DecisionCall
	for _, c := range l.calls {
		if c.State == state {
			out = append(out, c)
		}
	}
	return out, nil
}

func (l *wiringDecisionLedger) add(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, domain.DecisionCall{
		ID: id, Origin: "h_a", State: domain.DecisionStateNeedsYou,
		Raised: domain.DecisionRaisedPayload{Title: "Ship it?", ProjectName: "Shop"},
	})
}

type wiringNotificationWriter struct {
	mu      sync.Mutex
	created map[string]bool
	gate    chan struct{}
}

func (w *wiringNotificationWriter) Notify(_ context.Context, intent ports.NotificationIntent) error {
	if w.gate != nil {
		<-w.gate
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.created[intent.DecisionCallID] = true
	return nil
}

func (w *wiringNotificationWriter) Reconcile(context.Context) error { return nil }

func (w *wiringNotificationWriter) has(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.created[id]
}

func TestDecisionNotificationWiringRepairsAtStartupAndWakesOnAppend(t *testing.T) {
	ledger := &wiringDecisionLedger{}
	ledger.add("dec_before_start")
	writer := &wiringNotificationWriter{created: map[string]bool{}}
	ctx, cancel := context.WithCancel(context.Background())
	projector, done := startDecisionNotifications(ctx, ledger, writer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer func() {
		cancel()
		<-done
	}()
	waitUntil(t, func() bool { return writer.has("dec_before_start") })

	var delivered int
	onAppended := decisionAppendHook(func() { delivered++ }, projector.Wake)
	ledger.add("dec_after_start")
	onAppended(domain.DecisionEntry{CallID: "dec_after_start"})
	if delivered != 1 {
		t.Fatalf("answer deliverer woke %d times, want 1", delivered)
	}
	waitUntil(t, func() bool { return writer.has("dec_after_start") })
}

func TestDecisionNotificationWiringAppendNeverBlocksOnNotificationIO(t *testing.T) {
	ledger := &wiringDecisionLedger{}
	ledger.add("dec_1")
	writer := &wiringNotificationWriter{created: map[string]bool{}, gate: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	projector, done := startDecisionNotifications(ctx, ledger, writer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	onAppended := decisionAppendHook(projector.Wake)
	returned := make(chan struct{})
	go func() {
		for range 10 {
			onAppended(domain.DecisionEntry{})
		}
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("a ledger append blocked on notification I/O")
	}
	close(writer.gate)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("projector did not stop on cancellation")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
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
