package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type deliveredMessage struct {
	session domain.SessionID
	text    string
	entryID string
	at      time.Time
}

type fakeDecisionMessenger struct {
	messages []deliveredMessage
	attempts []domain.SessionID
	errs     map[domain.SessionID]error
}

func (m *fakeDecisionMessenger) DeliverAsPerson(_ context.Context, id domain.SessionID, message, deliveryID string, at time.Time) error {
	m.attempts = append(m.attempts, id)
	if err := m.errs[id]; err != nil {
		delete(m.errs, id)
		return err
	}
	m.messages = append(m.messages, deliveredMessage{session: id, text: message, entryID: deliveryID, at: at})
	return nil
}

type blockingDecisionMessenger struct {
	*fakeDecisionMessenger
	block   domain.SessionID
	started chan struct{}
	release chan struct{}
}

type unconfirmedDecisionMessenger struct {
	*fakeDecisionMessenger
	unconfirmed domain.SessionID
}

func (m *unconfirmedDecisionMessenger) DeliverAsPerson(ctx context.Context, id domain.SessionID, message, deliveryID string, at time.Time) error {
	if err := m.fakeDecisionMessenger.DeliverAsPerson(ctx, id, message, deliveryID, at); err != nil {
		return err
	}
	if id == m.unconfirmed {
		return fmt.Errorf("confirmation lost after write: %w", ports.ErrAgentDeliveryUnconfirmed)
	}
	return nil
}

func (m *blockingDecisionMessenger) DeliverAsPerson(ctx context.Context, id domain.SessionID, message, deliveryID string, at time.Time) error {
	if id == m.block {
		m.started <- struct{}{}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-m.release:
		}
	}
	return m.fakeDecisionMessenger.DeliverAsPerson(ctx, id, message, deliveryID, at)
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

func TestDelivererTracksAChangedAnswerAfterTheEarlierDeliveryRecordFailed(t *testing.T) {
	fixture := newDecisionService(t)
	first := answeredCall(t, fixture, fixture.w1)
	messenger := newFakeDecisionMessenger()
	store := &failingAppendStore{DeliveryStore: fixture.store, fail: true}
	deliverer := newTestDeliverer(store, messenger, "h_a")

	pending, err := deliverer.RunDue(context.Background())
	if err != nil || pending != 1 || len(messenger.messages) != 1 {
		t.Fatalf("first RunDue() pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
	second, err := fixture.service.Answer(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, first.ID, AnswerInput{
		Choice: domain.DecisionChoiceOption, Option: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	pending, err = deliverer.RunDue(context.Background())
	if err != nil || pending != 0 || len(messenger.messages) != 3 {
		t.Fatalf("changed RunDue() pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
	second.Redelivery = true
	if messenger.messages[1].text != DeliveryMessage(first, false) || messenger.messages[2].text != DeliveryMessage(second, false) ||
		!strings.HasPrefix(messenger.messages[2].text, "Changed decision on your call") {
		t.Fatalf("messages = %+v", messenger.messages)
	}
	entries, err := fixture.store.ListDecisionEntries(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	delivered := make(map[string]bool)
	for _, entry := range entries {
		if entry.Kind != domain.DecisionKindDelivered {
			continue
		}
		var payload domain.DecisionDeliveredPayload
		if json.Unmarshal(entry.Payload, &payload) == nil {
			delivered[payload.ForEntry] = true
		}
	}
	if !delivered[first.StandingEntryID] || !delivered[second.StandingEntryID] || len(delivered) != 2 {
		t.Fatalf("delivered entries = %+v", delivered)
	}
}

func TestDelivererUsesDismissalEntryAfterTheCallClosed(t *testing.T) {
	fixture := newDecisionService(t)
	call := raiseDecision(t, fixture, fixture.w1)
	at := time.UnixMilli(4_000_000).UTC()
	for i, fact := range []struct {
		id      string
		kind    domain.DecisionEntryKind
		payload any
		author  domain.DecisionAuthorKind
	}{
		{id: "withdrawn", kind: domain.DecisionKindWithdrawn, payload: struct{}{}, author: domain.DecisionAuthorSession},
		{id: "dismissed", kind: domain.DecisionKindDismissed, payload: domain.DecisionDismissedPayload{Note: "No longer needed."}, author: domain.DecisionAuthorPerson},
	} {
		raw, err := json.Marshal(fact.payload)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = fixture.store.AppendDecisionEntry(context.Background(), domain.DecisionEntry{
			ID: fact.id, Origin: "h_a", CallID: call.ID, Kind: fact.kind, Payload: raw,
			WrittenAt: at.Add(time.Duration(i) * time.Second), AuthorKind: fact.author,
		}, at.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	messenger := newFakeDecisionMessenger()
	pending, err := newTestDeliverer(fixture.store, messenger, "h_a").RunDue(context.Background())
	if err != nil || pending != 0 || len(messenger.messages) != 1 || !strings.HasPrefix(messenger.messages[0].text, "I dismissed your call") {
		t.Fatalf("pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
}

func TestDelivererLetsTheMessengerVerifyAStaleExitBeforeFallback(t *testing.T) {
	fixture := newDecisionService(t)
	call := answeredCall(t, fixture, fixture.w1)
	owner, ok, err := fixture.store.GetSession(context.Background(), fixture.w1)
	if err != nil || !ok {
		t.Fatalf("GetSession() = %+v, %v, %v", owner, ok, err)
	}
	owner.Activity.State = domain.ActivityExited
	if err := fixture.store.UpdateSession(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	messenger := newFakeDecisionMessenger()
	pending, err := newTestDeliverer(fixture.store, messenger, "h_a").RunDue(context.Background())
	if err != nil || pending != 0 || len(messenger.messages) != 1 || messenger.messages[0].session != fixture.w1 {
		t.Fatalf("stale exit pending=%d messages=%+v attempts=%v err=%v call=%+v", pending, messenger.messages, messenger.attempts, err, call)
	}
}

func TestDelivererFallsBackOnlyAfterTheMessengerConfirmsExit(t *testing.T) {
	fixture := newDecisionService(t)
	call := answeredCall(t, fixture, fixture.w1)
	messenger := newFakeDecisionMessenger()
	messenger.errs[fixture.w1] = errors.New("no live chat controller")
	deliverer := newTestDeliverer(fixture.store, messenger, "h_a")

	pending, err := deliverer.RunDue(context.Background())
	if err != nil || pending != 1 || len(messenger.messages) != 0 {
		t.Fatalf("temporary failure pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
	owner, ok, err := fixture.store.GetSession(context.Background(), fixture.w1)
	if err != nil || !ok {
		t.Fatalf("GetSession() = %+v, %v, %v", owner, ok, err)
	}
	owner.Activity.State = domain.ActivityExited
	if err := fixture.store.UpdateSession(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	messenger.errs[fixture.w1] = fmt.Errorf("%w: confirmed stopped", ErrSessionEnded)
	pending, err = deliverer.RunDue(context.Background())
	if err != nil || pending != 0 || len(messenger.messages) != 1 || messenger.messages[0].session != fixture.orch ||
		!strings.HasPrefix(messenger.messages[0].text, "Decision on a call raised by") ||
		len(messenger.attempts) != 3 || messenger.attempts[1] != fixture.w1 || messenger.attempts[2] != fixture.orch {
		t.Fatalf("confirmed exit pending=%d messages=%+v attempts=%v err=%v call=%+v", pending, messenger.messages, messenger.attempts, err, call)
	}
}

func TestDelivererKeepsPlanApprovalPendingWhileWorkerIsStopped(t *testing.T) {
	fixture := newDecisionService(t)
	if updated, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.loose, domain.PlanFirstPending); err != nil || !updated {
		t.Fatalf("set plan-first state: updated=%v err=%v", updated, err)
	}
	call := answeredPlanFirstCall(t, fixture, fixture.loose, 1, "")
	messenger := newFakeDecisionMessenger()
	messenger.errs[fixture.loose] = fmt.Errorf("%w: worker stopped", ErrSessionEnded)
	deliverer := newTestDeliverer(fixture.store, messenger, "h_a")

	pending, err := deliverer.RunDue(context.Background())
	if err != nil || pending != 1 || len(messenger.messages) != 0 || getDecisionCall(t, fixture, call.ID).Delivery != domain.DecisionDeliveryPending {
		t.Fatalf("stopped worker pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
	record, ok, err := fixture.store.GetSession(context.Background(), fixture.loose)
	if err != nil || !ok || record.PlanFirstState != domain.PlanFirstPending {
		t.Fatalf("stopped worker state=%q ok=%v err=%v", record.PlanFirstState, ok, err)
	}

	pending, err = deliverer.RunDue(context.Background())
	if err != nil || pending != 0 || len(messenger.messages) != 1 || messenger.messages[0].session != fixture.loose {
		t.Fatalf("resumed worker pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
	record, ok, err = fixture.store.GetSession(context.Background(), fixture.loose)
	if err != nil || !ok || record.PlanFirstState != domain.PlanFirstApproved {
		t.Fatalf("approved worker state=%q ok=%v err=%v", record.PlanFirstState, ok, err)
	}
}

func TestDelivererKeepsPlanApprovalPendingUntilDeliveryIsConfirmed(t *testing.T) {
	fixture := newDecisionService(t)
	if updated, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.w1, domain.PlanFirstPending); err != nil || !updated {
		t.Fatalf("set plan-first state: updated=%v err=%v", updated, err)
	}
	call := answeredPlanFirstCall(t, fixture, fixture.w1, 1, "")
	messenger := &unconfirmedDecisionMessenger{fakeDecisionMessenger: newFakeDecisionMessenger(), unconfirmed: fixture.w1}

	pending, err := newTestDeliverer(fixture.store, messenger, "h_a").RunDue(context.Background())
	if err != nil || pending != 1 {
		t.Fatalf("unconfirmed delivery pending=%d err=%v", pending, err)
	}
	record, ok, err := fixture.store.GetSession(context.Background(), fixture.w1)
	if err != nil || !ok || record.PlanFirstState != domain.PlanFirstPending {
		t.Fatalf("unconfirmed worker state=%q ok=%v err=%v", record.PlanFirstState, ok, err)
	}
	if got := getDecisionCall(t, fixture, call.ID).Delivery; got != domain.DecisionDeliveryPending {
		t.Fatalf("delivery=%q, want pending", got)
	}
}

func TestDelivererKeepsChangedPlanAnswerForStoppedApprovedWorker(t *testing.T) {
	fixture := newDecisionService(t)
	if updated, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.loose, domain.PlanFirstPending); err != nil || !updated {
		t.Fatalf("set plan-first state: updated=%v err=%v", updated, err)
	}
	call := answeredPlanFirstCall(t, fixture, fixture.loose, 1, "")
	messenger := newFakeDecisionMessenger()
	deliverer := newTestDeliverer(fixture.store, messenger, "h_a")
	if pending, err := deliverer.RunDue(context.Background()); err != nil || pending != 0 {
		t.Fatalf("deliver approval: pending=%d err=%v", pending, err)
	}

	changed, err := fixture.service.Answer(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, call.ID, AnswerInput{
		Choice: domain.DecisionChoiceOption, Option: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, ok, err := fixture.store.GetSession(context.Background(), fixture.loose)
	if err != nil || !ok || record.PlanFirstState != domain.PlanFirstDeclined {
		t.Fatalf("authoritative changed answer state=%q ok=%v err=%v", record.PlanFirstState, ok, err)
	}
	messenger.errs[fixture.loose] = fmt.Errorf("%w: worker stopped", ErrSessionEnded)
	if pending, err := deliverer.RunDue(context.Background()); err != nil || pending != 1 {
		t.Fatalf("stopped changed answer: pending=%d err=%v", pending, err)
	}
	if got := getDecisionCall(t, fixture, call.ID); got.StandingEntryID != changed.StandingEntryID || got.Delivery != domain.DecisionDeliveryPending {
		t.Fatalf("changed call=%+v", got)
	}
	record, ok, err = fixture.store.GetSession(context.Background(), fixture.loose)
	if err != nil || !ok || record.PlanFirstState != domain.PlanFirstDeclined {
		t.Fatalf("stopped worker state=%q ok=%v err=%v", record.PlanFirstState, ok, err)
	}

	if pending, err := deliverer.RunDue(context.Background()); err != nil || pending != 0 {
		t.Fatalf("resumed changed answer: pending=%d err=%v", pending, err)
	}
	record, ok, err = fixture.store.GetSession(context.Background(), fixture.loose)
	if err != nil || !ok || record.PlanFirstState != domain.PlanFirstDeclined {
		t.Fatalf("declined worker state=%q ok=%v err=%v", record.PlanFirstState, ok, err)
	}
}

func TestDelivererSkipsSupersededPlanApprovalWhenWorkerResumes(t *testing.T) {
	fixture := newDecisionService(t)
	if updated, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.loose, domain.PlanFirstPending); err != nil || !updated {
		t.Fatalf("set plan-first state: updated=%v err=%v", updated, err)
	}
	approval := answeredPlanFirstCall(t, fixture, fixture.loose, 1, "")
	messenger := newFakeDecisionMessenger()
	messenger.errs[fixture.loose] = fmt.Errorf("%w: worker stopped", ErrSessionEnded)
	deliverer := newTestDeliverer(fixture.store, messenger, "h_a")
	if pending, err := deliverer.RunDue(context.Background()); err != nil || pending != 1 {
		t.Fatalf("stopped approval: pending=%d err=%v", pending, err)
	}

	decline, err := fixture.service.Answer(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, approval.ID, AnswerInput{
		Choice: domain.DecisionChoiceOption, Option: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	delete(messenger.errs, fixture.loose)
	if pending, err := deliverer.RunDue(context.Background()); err != nil || pending != 0 {
		t.Fatalf("resumed delivery: pending=%d err=%v", pending, err)
	}
	if len(messenger.messages) != 1 || messenger.messages[0].entryID != decline.StandingEntryID ||
		!strings.Contains(messenger.messages[0].text, "Do not proceed") {
		t.Fatalf("delivered messages = %+v, want only current decline %s", messenger.messages, decline.StandingEntryID)
	}
	record, ok, err := fixture.store.GetSession(context.Background(), fixture.loose)
	if err != nil || !ok || record.PlanFirstState != domain.PlanFirstDeclined {
		t.Fatalf("declined worker state=%q ok=%v err=%v", record.PlanFirstState, ok, err)
	}
	entries, err := fixture.store.ListDecisionEntries(context.Background(), approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, entry := range entries {
		if entry.Kind != domain.DecisionKindDelivered {
			continue
		}
		var payload domain.DecisionDeliveredPayload
		if json.Unmarshal(entry.Payload, &payload) == nil {
			statuses[payload.ForEntry] = payload.Status
		}
	}
	if statuses[approval.StandingEntryID] != "superseded" || statuses[decline.StandingEntryID] != domain.DecisionDeliveredSent {
		t.Fatalf("delivery statuses = %+v", statuses)
	}
}

func TestDelivererSerializesPlanApprovalWithChangedAnswer(t *testing.T) {
	fixture := newDecisionService(t)
	if updated, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.loose, domain.PlanFirstPending); err != nil || !updated {
		t.Fatalf("set plan-first state: updated=%v err=%v", updated, err)
	}
	approval := answeredPlanFirstCall(t, fixture, fixture.loose, 1, "")
	messenger := &blockingDecisionMessenger{
		fakeDecisionMessenger: newFakeDecisionMessenger(), block: fixture.loose,
		started: make(chan struct{}, 1), release: make(chan struct{}),
	}
	deliverer := newTestDeliverer(fixture.store, messenger, "h_a")
	deliverer.mutationGate = fixture.service.mutationGate
	type deliveryResult struct {
		pending int
		err     error
	}
	delivered := make(chan deliveryResult, 1)
	go func() {
		pending, err := deliverer.RunDue(context.Background())
		delivered <- deliveryResult{pending: pending, err: err}
	}()
	<-messenger.started

	type answerResult struct {
		call domain.DecisionCall
		err  error
	}
	answered := make(chan answerResult, 1)
	go func() {
		call, err := fixture.service.Answer(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, approval.ID, AnswerInput{
			Choice: domain.DecisionChoiceOption, Option: 3,
		})
		answered <- answerResult{call: call, err: err}
	}()
	answeredBeforeDelivery := false
	var answer answerResult
	select {
	case answer = <-answered:
		answeredBeforeDelivery = true
	case <-time.After(50 * time.Millisecond):
	}
	close(messenger.release)
	result := <-delivered
	if !answeredBeforeDelivery {
		answer = <-answered
	}
	if result.err != nil || result.pending != 0 || answer.err != nil {
		t.Fatalf("delivery=%+v answer=%+v", result, answer)
	}
	record, ok, err := fixture.store.GetSession(context.Background(), fixture.loose)
	if answeredBeforeDelivery || err != nil || !ok || record.PlanFirstState != domain.PlanFirstDeclined ||
		answer.call.Option != 3 {
		t.Fatalf("answeredBeforeDelivery=%v state=%q ok=%v err=%v answer=%+v", answeredBeforeDelivery, record.PlanFirstState, ok, err, answer.call)
	}
}

func TestBlockedPlanDeliveryDoesNotBlockUnrelatedDecisionMutation(t *testing.T) {
	fixture := newDecisionService(t)
	if updated, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.loose, domain.PlanFirstPending); err != nil || !updated {
		t.Fatalf("set plan-first state: updated=%v err=%v", updated, err)
	}
	answeredPlanFirstCall(t, fixture, fixture.loose, 1, "")
	unrelated := raiseDecision(t, fixture, fixture.w1)
	messenger := &blockingDecisionMessenger{
		fakeDecisionMessenger: newFakeDecisionMessenger(), block: fixture.loose,
		started: make(chan struct{}, 1), release: make(chan struct{}),
	}
	deliverer := newTestDeliverer(fixture.store, messenger, "h_a")
	deliverer.mutationGate = fixture.service.mutationGate
	deliverer.attemptTimeout = time.Second
	delivered := make(chan error, 1)
	go func() {
		_, err := deliverer.RunDue(context.Background())
		delivered <- err
	}()
	<-messenger.started

	answered := make(chan error, 1)
	go func() {
		_, err := fixture.service.Answer(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, unrelated.ID, AnswerInput{
			Choice: domain.DecisionChoiceOption, Option: 1,
		})
		answered <- err
	}()
	var answerErr error
	blocked := false
	select {
	case answerErr = <-answered:
	case <-time.After(100 * time.Millisecond):
		blocked = true
	}
	close(messenger.release)
	if blocked {
		answerErr = <-answered
	}
	if deliveryErr := <-delivered; deliveryErr != nil || answerErr != nil || blocked {
		t.Fatalf("unrelated mutation blocked=%v answerErr=%v deliveryErr=%v", blocked, answerErr, deliveryErr)
	}
}

func TestDelivererBoundsBlockedPlanDelivery(t *testing.T) {
	fixture := newDecisionService(t)
	if updated, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.loose, domain.PlanFirstPending); err != nil || !updated {
		t.Fatalf("set plan-first state: updated=%v err=%v", updated, err)
	}
	answeredPlanFirstCall(t, fixture, fixture.loose, 1, "")
	messenger := &blockingDecisionMessenger{
		fakeDecisionMessenger: newFakeDecisionMessenger(), block: fixture.loose,
		started: make(chan struct{}, 1), release: make(chan struct{}),
	}
	deliverer := newTestDeliverer(fixture.store, messenger, "h_a")
	deliverer.mutationGate = fixture.service.mutationGate
	deliverer.attemptTimeout = 20 * time.Millisecond

	pending, err := deliverer.RunDue(context.Background())
	if err != nil || pending != 1 {
		t.Fatalf("RunDue() pending=%d err=%v, want one retryable delivery", pending, err)
	}
}

func TestDelivererRequiresANoteThenAcceptsTheReplacementPlanCall(t *testing.T) {
	fixture := newDecisionService(t)
	if updated, err := fixture.store.SetSessionPlanFirstState(context.Background(), fixture.w1, domain.PlanFirstPending); err != nil || !updated {
		t.Fatalf("set plan-first state: updated=%v err=%v", updated, err)
	}
	answeredPlanFirstCall(t, fixture, fixture.w1, 2, "")
	messenger := newFakeDecisionMessenger()
	deliverer := newTestDeliverer(fixture.store, messenger, "h_a")
	if pending, err := deliverer.RunDue(context.Background()); err != nil || pending != 0 {
		t.Fatalf("deliver missing-note answer: pending=%d err=%v", pending, err)
	}
	record, ok, err := fixture.store.GetSession(context.Background(), fixture.w1)
	if err != nil || !ok || record.PlanFirstState != domain.PlanFirstPending {
		t.Fatalf("missing-note state=%q ok=%v err=%v", record.PlanFirstState, ok, err)
	}

	second := answeredPlanFirstCall(t, fixture, fixture.w1, 2, "Keep the existing API shape.")
	if pending, err := deliverer.RunDue(context.Background()); err != nil || pending != 0 {
		t.Fatalf("deliver noted answer: pending=%d err=%v", pending, err)
	}
	record, ok, err = fixture.store.GetSession(context.Background(), fixture.w1)
	if err != nil || !ok || record.PlanFirstState != domain.PlanFirstApproved {
		t.Fatalf("noted state=%q ok=%v err=%v second=%s", record.PlanFirstState, ok, err, second.ID)
	}
}

func answeredPlanFirstCall(t *testing.T, fixture decisionServiceFixture, session domain.SessionID, option int, note string) domain.DecisionCall {
	t.Helper()
	input := validRaiseInput()
	input.Title = "Approve this plan?"
	input.Reference = domain.PlanFirstDecisionReference
	input.Options = []domain.DecisionOption{
		{Label: "Approve", Consequence: "Start implementation exactly as planned.", Recommended: true},
		{Label: "Approve with changes", Consequence: "Apply the approval note, then start implementation."},
		{Label: "Do not proceed", Consequence: "Stop without implementation changes."},
	}
	call, err := fixture.service.Raise(context.Background(), Actor{Session: session}, input)
	if err != nil {
		t.Fatal(err)
	}
	call, err = fixture.service.Answer(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, call.ID, AnswerInput{
		Choice: domain.DecisionChoiceOption, Option: option, Note: note,
	})
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func TestFallbackRecipientCanRecordOutcomeWhileDeliveryIsInFlight(t *testing.T) {
	fixture := newDecisionService(t)
	call := answeredCall(t, fixture, fixture.w1)
	terminateDecisionSession(t, fixture, fixture.w1)
	messenger := &blockingDecisionMessenger{
		fakeDecisionMessenger: newFakeDecisionMessenger(),
		block:                 fixture.orch, started: make(chan struct{}, 1), release: make(chan struct{}),
	}
	deliverer := newTestDelivererWithAuthorizer(fixture.store, messenger, "h_a", fixture.service)
	type deliveryResult struct {
		pending int
		err     error
	}
	done := make(chan deliveryResult, 1)
	go func() {
		pending, err := deliverer.RunDue(context.Background())
		done <- deliveryResult{pending: pending, err: err}
	}()
	<-messenger.started
	completed, outcomeErr := fixture.service.RecordOutcome(context.Background(), Actor{Session: fixture.orch}, call.ID, OutcomeInput{ForEntry: call.StandingEntryID, Text: "Carried it out"})
	close(messenger.release)
	result := <-done
	if outcomeErr != nil {
		t.Fatalf("RecordOutcome while delivery blocked: %v", outcomeErr)
	}
	if completed.State != domain.DecisionStateDone || result.err != nil || result.pending != 0 {
		t.Fatalf("completed=%+v delivery=%+v", completed, result)
	}
}

func TestAcceptedFallbackGrantSurvivesAFailedRetry(t *testing.T) {
	fixture := newDecisionService(t)
	call := answeredCall(t, fixture, fixture.w1)
	terminateDecisionSession(t, fixture, fixture.w1)
	messenger := newFakeDecisionMessenger()
	store := &failingAppendStore{DeliveryStore: fixture.store, fail: true}
	deliverer := newTestDelivererWithAuthorizer(store, messenger, "h_a", fixture.service)

	pending, err := deliverer.RunDue(context.Background())
	if err != nil || pending != 1 || len(messenger.messages) != 1 || messenger.messages[0].session != fixture.orch {
		t.Fatalf("accepted send pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
	messenger.errs[fixture.orch] = errors.New("composer busy")
	pending, err = deliverer.RunDue(context.Background())
	if err != nil || pending != 1 {
		t.Fatalf("failed retry pending=%d err=%v", pending, err)
	}

	completed, err := fixture.service.RecordOutcome(context.Background(), Actor{Session: fixture.orch}, call.ID, OutcomeInput{ForEntry: call.StandingEntryID, Text: "Carried it out"})
	if err != nil || completed.State != domain.DecisionStateDone {
		t.Fatalf("RecordOutcome after failed retry = %+v, %v", completed, err)
	}
}

func TestFallbackRecipientKeepsOutcomeAuthorityAfterUnconfirmedDelivery(t *testing.T) {
	fixture := newDecisionService(t)
	call := answeredCall(t, fixture, fixture.w1)
	terminateDecisionSession(t, fixture, fixture.w1)
	messenger := &unconfirmedDecisionMessenger{
		fakeDecisionMessenger: newFakeDecisionMessenger(),
		unconfirmed:           fixture.orch,
	}
	deliverer := newTestDelivererWithAuthorizer(fixture.store, messenger, "h_a", fixture.service)

	pending, err := deliverer.RunDue(context.Background())
	if err != nil || pending != 1 || len(messenger.messages) != 1 || messenger.messages[0].session != fixture.orch {
		t.Fatalf("unconfirmed send pending=%d messages=%+v err=%v", pending, messenger.messages, err)
	}
	completed, err := fixture.service.RecordOutcome(context.Background(), Actor{Session: fixture.orch}, call.ID, OutcomeInput{
		ForEntry: call.StandingEntryID,
		Text:     "Carried it out",
	})
	if err != nil || completed.State != domain.DecisionStateDone {
		t.Fatalf("RecordOutcome after unconfirmed delivery = %+v, %v", completed, err)
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
	return newTestDelivererWithAuthorizer(store, messenger, hostID, nil)
}

func newTestDelivererWithAuthorizer(store DeliveryStore, messenger Messenger, hostID string, authorizer FallbackOutcomeAuthorizer) *Deliverer {
	nextID := 0
	return NewDeliverer(DelivererDeps{
		Store: store, Messenger: messenger, HostID: hostID,
		FallbackAuthorizer: authorizer,
		Now:                func() time.Time { return time.UnixMilli(5_000_000).UTC() },
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
