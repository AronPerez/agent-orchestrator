package decision

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// linkedMachine is a second machine (h_b) that links to the fixture's h_a.
// It shares nothing with h_a except the sync exchange.
type linkedMachine struct {
	store   *sqlite.Store
	service *Service
	sync    func(t *testing.T)
}

func linkToFixture(t *testing.T, fixture decisionServiceFixture) linkedMachine {
	t.Helper()
	store := sqlitetest.MustOpen(t)
	clock := func() time.Time { return fixture.now.Add(time.Minute) }
	service := New(Deps{Store: store, HostID: "h_b", MachineLabel: func() string { return "laptop" }, Now: clock})
	serverA := decisionsync.NewServer(decisionsync.ServerDeps{Ledger: fixture.store, HostID: "h_a"})
	engineB := decisionsync.NewEngine(decisionsync.EngineDeps{Ledger: store, HostID: "h_b"})
	return linkedMachine{store: store, service: service, sync: func(t *testing.T) {
		t.Helper()
		if _, err := engineB.Sync(context.Background(), "h_a", &ledgertest.DirectRemote{Server: serverA, CallerID: "h_b"}); err != nil {
			t.Fatalf("sync h_b with h_a: %v", err)
		}
	}}
}

func TestSyncedAnswerIsDeliveredOnceByOwner(t *testing.T) {
	ctx := context.Background()
	fixture := newDecisionService(t)
	call, err := fixture.service.Raise(ctx, Actor{Session: fixture.w1}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	b := linkToFixture(t, fixture)
	b.sync(t)
	if _, err := b.service.Answer(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 2}); err != nil {
		t.Fatalf("answer on h_b: %v", err)
	}
	bMessenger := newFakeDecisionMessenger()
	if pending, err := newTestDeliverer(b.store, bMessenger, "h_b").RunDue(ctx); err != nil || pending != 0 || len(bMessenger.messages) != 0 {
		t.Fatalf("h_b deliverer: pending=%d messages=%+v err=%v", pending, bMessenger.messages, err)
	}
	b.sync(t)

	aMessenger := newFakeDecisionMessenger()
	if pending, err := newTestDeliverer(fixture.store, aMessenger, "h_a").RunDue(ctx); err != nil || pending != 0 || len(aMessenger.messages) != 1 {
		t.Fatalf("h_a deliverer: pending=%d messages=%+v err=%v", pending, aMessenger.messages, err)
	}
	if sent := aMessenger.messages[0]; sent.session != fixture.w1 {
		t.Fatalf("delivered to %s, want the owner %s", sent.session, fixture.w1)
	}
	// A restarted owner does not deliver again.
	if _, err := newTestDeliverer(fixture.store, aMessenger, "h_a").RunDue(ctx); err != nil || len(aMessenger.messages) != 1 {
		t.Fatalf("after restart: messages=%d err=%v", len(aMessenger.messages), err)
	}
	b.sync(t)
	stored, ok, err := b.store.GetDecisionCall(ctx, call.ID)
	if err != nil || !ok || stored.Delivery != domain.DecisionDeliverySent || stored.DeliveredTo != fixture.w1 {
		t.Fatalf("h_b's copy after sync = %+v, %v, %v", stored, ok, err)
	}
	if _, err := newTestDeliverer(b.store, bMessenger, "h_b").RunDue(ctx); err != nil || len(bMessenger.messages) != 0 {
		t.Fatalf("h_b delivered: %+v, %v", bMessenger.messages, err)
	}
}

func TestFallbackOnlyOnOwner(t *testing.T) {
	ctx := context.Background()
	fixture := newDecisionService(t)
	call, err := fixture.service.Raise(ctx, Actor{Session: fixture.w1}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	b := linkToFixture(t, fixture)
	b.sync(t)
	if _, err := b.service.Answer(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1}); err != nil {
		t.Fatal(err)
	}
	b.sync(t)
	aMessenger := newFakeDecisionMessenger()
	aMessenger.errs[fixture.w1] = fmt.Errorf("%w: agent exited", ErrSessionEnded)
	if _, err := newTestDeliverer(fixture.store, aMessenger, "h_a").RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(aMessenger.messages) != 1 || aMessenger.messages[0].session != fixture.orch {
		t.Fatalf("h_a fallback = %+v, want one message to its orchestrator", aMessenger.messages)
	}
	bMessenger := newFakeDecisionMessenger()
	if _, err := newTestDeliverer(b.store, bMessenger, "h_b").RunDue(ctx); err != nil || len(bMessenger.messages) != 0 {
		t.Fatalf("h_b fell back: %+v, %v", bMessenger.messages, err)
	}
}

// Pushing direction: an answer written on h_b reaches h_a within the pokes'
// coalescing delay, without waiting for the idle minute.
func TestAnswerReachesOwnerWithinSeconds(t *testing.T) {
	ctx := context.Background()
	fixture := newDecisionService(t)
	call, err := fixture.service.Raise(ctx, Actor{Session: fixture.w1}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	storeB := sqlitetest.MustOpen(t)
	serverA := decisionsync.NewServer(decisionsync.ServerDeps{Ledger: fixture.store, HostID: "h_a"})
	engineB := decisionsync.NewEngine(decisionsync.EngineDeps{Ledger: storeB, HostID: "h_b"})
	clock := newSyncTestClock()
	sched := decisionsync.NewScheduler(decisionsync.SchedulerDeps{
		Engine: engineB, After: clock.After, Rand: func() float64 { return 0.5 },
		Links: syncTestLinks{{HostID: "h_a"}},
		Remote: func(decisionsync.StoredPeer) decisionsync.Remote {
			return &ledgertest.DirectRemote{Server: serverA, CallerID: "h_b"}
		},
	})
	serviceB := New(Deps{Store: storeB, HostID: "h_b", OnAppended: func(domain.DecisionEntry) { sched.Poke("") },
		Now: func() time.Time { return fixture.now.Add(time.Minute) }})
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sched.Start(runCtx)
	clock.waitAsked(t, 1) // the start-up sync brought the call over
	if _, ok, _ := storeB.GetDecisionCall(ctx, call.ID); !ok {
		t.Fatal("start-up sync did not bring the call to h_b")
	}
	if _, err := serviceB.Answer(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1}); err != nil {
		t.Fatal(err)
	}
	clock.waitAsked(t, 2)
	clock.Advance(2 * time.Second) // within 2 seconds of fake time
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got, _, _ := fixture.store.GetDecisionCall(ctx, call.ID); got.State == domain.DecisionStateInProgress {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the answer did not reach the owning machine within 2 seconds of fake time")
}
