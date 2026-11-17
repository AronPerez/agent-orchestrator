package decision

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type deliveredMessage struct {
	session domain.SessionID
	text    string
	at      time.Time
}

type fakeDecisionMessenger struct {
	messages []deliveredMessage
	errs     map[domain.SessionID]error
}

func (m *fakeDecisionMessenger) DeliverAsPerson(_ context.Context, id domain.SessionID, message string, at time.Time) error {
	if err := m.errs[id]; err != nil {
		delete(m.errs, id)
		return err
	}
	m.messages = append(m.messages, deliveredMessage{session: id, text: message, at: at})
	return nil
}

type failingAppendStore struct {
	DeliveryStore
	fail bool
}

func (s *failingAppendStore) AppendDecisionEntry(ctx context.Context, entry domain.DecisionEntry, at time.Time) (domain.DecisionEntry, domain.DecisionCall, error) {
	if s.fail && entry.Kind == domain.DecisionKindDelivered {
		s.fail = false
		return domain.DecisionEntry{}, domain.DecisionCall{}, errors.New("recording interrupted")
	}
	return s.DeliveryStore.AppendDecisionEntry(ctx, entry, at)
}

func TestDelivererSendsEachAnswerOnce(t *testing.T) {
	fixture := newDecisionService(t)
	messenger := newFakeDecisionMessenger()
	deliverer := newTestDeliverer(fixture.store, messenger, "h_a")
	call := answeredCall(t, fixture, fixture.w1)

	pending, err := deliverer.RunDue(context.Background())
	if err != nil || pending != 0 || len(messenger.messages) != 1 {
		t.Fatalf("RunDue() pending=%d messages=%d err=%v", pending, len(messenger.messages), err)
	}
	if got := messenger.messages[0]; got.session != fixture.w1 || got.text != DeliveryMessage(call, false) {
		t.Fatalf("message = %+v", got)
	}
	stored := getDecisionCall(t, fixture, call.ID)
	if stored.Delivery != domain.DecisionDeliverySent || stored.DeliveredTo != fixture.w1 || stored.DeliveredFallback {
		t.Fatalf("stored call = %+v", stored)
	}
	entries, err := fixture.store.ListDecisionEntries(context.Background(), call.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := entries[len(entries)-1]
	if last.Kind != domain.DecisionKindDelivered || last.AuthorKind != domain.DecisionAuthorSystem || last.Origin != "h_a" {
		t.Fatalf("last entry = %+v", last)
	}

	if pending, err = deliverer.RunDue(context.Background()); err != nil || pending != 0 || len(messenger.messages) != 1 {
		t.Fatalf("restart RunDue() pending=%d messages=%d err=%v", pending, len(messenger.messages), err)
	}
	changed, err := fixture.service.Answer(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, call.ID, AnswerInput{
		Choice: domain.DecisionChoiceOption, Option: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pending, err = deliverer.RunDue(context.Background()); err != nil || pending != 0 || len(messenger.messages) != 2 {
		t.Fatalf("changed RunDue() pending=%d messages=%d err=%v", pending, len(messenger.messages), err)
	}
	if !strings.HasPrefix(messenger.messages[1].text, "Changed decision on your call") || messenger.messages[1].text != DeliveryMessage(changed, false) {
		t.Fatalf("changed message = %q", messenger.messages[1].text)
	}
}

func TestDelivererDeliversADismissal(t *testing.T) {
	fixture := newDecisionService(t)
	call := raiseDecision(t, fixture, fixture.w1)
	dismissed, err := fixture.service.Dismiss(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, call.ID, "Not enough to go on")
	if err != nil {
		t.Fatal(err)
	}
	messenger := newFakeDecisionMessenger()
	pending, err := newTestDeliverer(fixture.store, messenger, "h_a").RunDue(context.Background())
	if err != nil || pending != 0 || len(messenger.messages) != 1 || !strings.HasPrefix(messenger.messages[0].text, "I dismissed your call") || messenger.messages[0].text != DeliveryMessage(dismissed, false) {
		t.Fatalf("pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
}

func TestDelivererFallsBackToTheOrchestrator(t *testing.T) {
	fixture := newDecisionService(t)
	ended := answeredCall(t, fixture, fixture.w1)
	refused := answeredCall(t, fixture, fixture.w2)
	terminateDecisionSession(t, fixture, fixture.w1)
	messenger := newFakeDecisionMessenger()
	messenger.errs[fixture.w2] = fmt.Errorf("%w: agent exited", ErrSessionEnded)

	pending, err := newTestDeliverer(fixture.store, messenger, "h_a").RunDue(context.Background())
	if err != nil || pending != 0 || len(messenger.messages) != 2 {
		t.Fatalf("RunDue() pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
	for _, sent := range messenger.messages {
		if sent.session != fixture.orch || !strings.HasPrefix(sent.text, "Decision on a call raised by") {
			t.Fatalf("fallback message = %+v", sent)
		}
	}
	for _, call := range []domain.DecisionCall{ended, refused} {
		stored := getDecisionCall(t, fixture, call.ID)
		if stored.Delivery != domain.DecisionDeliverySent || stored.DeliveredTo != fixture.orch || !stored.DeliveredFallback {
			t.Fatalf("stored fallback = %+v", stored)
		}
	}
}

func TestDelivererRecordsThatNoSessionCanTakeIt(t *testing.T) {
	fixture := newDecisionService(t)
	projectCall := answeredCall(t, fixture, fixture.w1)
	looseCall := answeredCall(t, fixture, fixture.loose)
	for _, session := range []domain.SessionID{fixture.w1, fixture.loose, fixture.orch} {
		terminateDecisionSession(t, fixture, session)
	}
	messenger := newFakeDecisionMessenger()
	pending, err := newTestDeliverer(fixture.store, messenger, "h_a").RunDue(context.Background())
	if err != nil || pending != 0 || len(messenger.messages) != 0 {
		t.Fatalf("RunDue() pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
	for _, call := range []domain.DecisionCall{projectCall, looseCall} {
		if stored := getDecisionCall(t, fixture, call.ID); stored.Delivery != domain.DecisionDeliveryNoSession {
			t.Fatalf("stored no-session = %+v", stored)
		}
	}
}

func TestDelivererRetriesAfterAPassingFailure(t *testing.T) {
	fixture := newDecisionService(t)
	call := answeredCall(t, fixture, fixture.w1)
	messenger := newFakeDecisionMessenger()
	messenger.errs[fixture.w1] = errors.New("composer busy")
	deliverer := newTestDeliverer(fixture.store, messenger, "h_a")

	pending, err := deliverer.RunDue(context.Background())
	if err != nil || pending != 1 || len(messenger.messages) != 0 || getDecisionCall(t, fixture, call.ID).Delivery != domain.DecisionDeliveryPending {
		t.Fatalf("first RunDue() pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
	pending, err = deliverer.RunDue(context.Background())
	if err != nil || pending != 0 || len(messenger.messages) != 1 {
		t.Fatalf("retry RunDue() pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
}

func TestDelivererSendsAgainWhenRecordingFailed(t *testing.T) {
	fixture := newDecisionService(t)
	call := answeredCall(t, fixture, fixture.w1)
	messenger := newFakeDecisionMessenger()
	store := &failingAppendStore{DeliveryStore: fixture.store, fail: true}
	deliverer := newTestDeliverer(store, messenger, "h_a")

	pending, err := deliverer.RunDue(context.Background())
	if err != nil || pending != 1 || len(messenger.messages) != 1 {
		t.Fatalf("first RunDue() pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
	pending, err = deliverer.RunDue(context.Background())
	if err != nil || pending != 0 || len(messenger.messages) != 2 || messenger.messages[0].text != messenger.messages[1].text {
		t.Fatalf("retry RunDue() pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
	entries, err := fixture.store.ListDecisionEntries(context.Background(), call.ID)
	if err != nil {
		t.Fatal(err)
	}
	delivered := 0
	for _, entry := range entries {
		if entry.Kind == domain.DecisionKindDelivered {
			delivered++
		}
	}
	if delivered != 1 {
		t.Fatalf("delivered entries=%d, want 1", delivered)
	}
}

func TestDelivererLeavesOtherMachinesCallsAlone(t *testing.T) {
	fixture := newDecisionService(t)
	answeredCall(t, fixture, fixture.w1)
	messenger := newFakeDecisionMessenger()
	pending, err := newTestDeliverer(fixture.store, messenger, "h_b").RunDue(context.Background())
	if err != nil || pending != 0 || len(messenger.messages) != 0 {
		t.Fatalf("RunDue() pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
}

func newFakeDecisionMessenger() *fakeDecisionMessenger {
	return &fakeDecisionMessenger{errs: make(map[domain.SessionID]error)}
}

func newTestDeliverer(store DeliveryStore, messenger Messenger, hostID string) *Deliverer {
	nextID := 0
	return NewDeliverer(DelivererDeps{
		Store: store, Messenger: messenger, HostID: hostID,
		Now: func() time.Time { return time.UnixMilli(5_000_000).UTC() },
		NewID: func(prefix string) string {
			nextID++
			return fmt.Sprintf("%stest-%d", prefix, nextID)
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func answeredCall(t *testing.T, fixture decisionServiceFixture, session domain.SessionID) domain.DecisionCall {
	t.Helper()
	call := raiseDecision(t, fixture, session)
	answered, err := fixture.service.Answer(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, call.ID, AnswerInput{
		Choice: domain.DecisionChoiceOption, Option: 1, Note: "Rotate monthly.",
	})
	if err != nil {
		t.Fatal(err)
	}
	return answered
}

func terminateDecisionSession(t *testing.T, fixture decisionServiceFixture, id domain.SessionID) {
	t.Helper()
	record, ok, err := fixture.store.GetSession(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("GetSession(%s) = %+v, %v, %v", id, record, ok, err)
	}
	record.IsTerminated = true
	if err := fixture.store.UpdateSession(context.Background(), record); err != nil {
		t.Fatalf("terminate %s: %v", id, err)
	}
}

func getDecisionCall(t *testing.T, fixture decisionServiceFixture, id string) domain.DecisionCall {
	t.Helper()
	call, ok, err := fixture.store.GetDecisionCall(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("GetDecisionCall(%s) = %+v, %v, %v", id, call, ok, err)
	}
	return call
}
