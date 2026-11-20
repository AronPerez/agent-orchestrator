package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type interleavingDecisionStore struct {
	Store
	answerAppending    chan struct{}
	releaseAnswer      chan struct{}
	withdrawRead       chan struct{}
	releaseWithdrawGet chan struct{}
}

func (s *interleavingDecisionStore) GetDecisionCall(ctx context.Context, id string) (domain.DecisionCall, bool, error) {
	call, ok, err := s.Store.GetDecisionCall(ctx, id)
	select {
	case <-s.answerAppending:
		close(s.withdrawRead)
		<-s.releaseWithdrawGet
	default:
	}
	return call, ok, err
}

func (s *interleavingDecisionStore) AppendDecisionEntry(ctx context.Context, entry domain.DecisionEntry, at time.Time) (domain.DecisionEntry, domain.DecisionCall, error) {
	if entry.Kind == domain.DecisionKindAnswered {
		close(s.answerAppending)
		<-s.releaseAnswer
	}
	return s.Store.AppendDecisionEntry(ctx, entry, at)
}

func TestAnswerIsThePersonsAlone(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	call := raiseDecision(t, fixture, fixture.w1)
	answer := AnswerInput{Choice: domain.DecisionChoiceOption, Option: 2, Note: "Rotate monthly"}

	for _, session := range []domain.SessionID{fixture.w1, fixture.w2, fixture.orch} {
		if _, err := fixture.service.Answer(ctx, Actor{Session: session}, call.ID, answer); apiCode(err) != "DECISION_PERSON_ONLY" {
			t.Fatalf("Answer as %s error = %v", session, err)
		}
	}

	answered, err := fixture.service.Answer(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, call.ID, answer)
	if err != nil {
		t.Fatal(err)
	}
	if answered.State != domain.DecisionStateInProgress || answered.Option != 2 || answered.Note != "Rotate monthly" ||
		answered.AnswerSurface != domain.RequestSurfaceDesktop || answered.Delivery != domain.DecisionDeliveryPending {
		t.Fatalf("answered call = %+v", answered)
	}
	entries := *fixture.appended
	entry := entries[len(entries)-1]
	if entry.Kind != domain.DecisionKindAnswered || entry.AuthorKind != domain.DecisionAuthorPerson ||
		entry.AuthorSurface != domain.RequestSurfaceDesktop || entry.Origin != "h_a" {
		t.Fatalf("answered entry = %+v", entry)
	}

	other, err := fixture.service.Answer(ctx, Actor{Surface: domain.RequestSurfaceCLI}, call.ID, AnswerInput{
		Choice: domain.DecisionChoiceOther,
		Note:   "Use the vault",
	})
	if err != nil {
		t.Fatal(err)
	}
	if other.Choice != domain.DecisionChoiceOther || other.ChosenLabel() != "Something else" {
		t.Fatalf("other answer = %+v", other)
	}
}

func TestAnswerRules(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	call := raiseDecision(t, fixture, fixture.w1)
	person := Actor{Surface: domain.RequestSurfaceDesktop}

	tests := []struct {
		name string
		in   AnswerInput
		rule string
	}{
		{name: "choice", in: AnswerInput{Choice: "maybe"}, rule: "choice_invalid"},
		{name: "option", in: AnswerInput{Choice: domain.DecisionChoiceOption, Option: 3}, rule: "option_range"},
		{name: "other note", in: AnswerInput{Choice: domain.DecisionChoiceOther}, rule: "note_required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fixture.service.Answer(ctx, person, call.ID, tt.in)
			assertDecisionAPIError(t, err, apierr.KindInvalid, "DECISION_INVALID", tt.rule, "")
		})
	}
	if _, err := fixture.service.Answer(ctx, person, "dec_missing", AnswerInput{Choice: "maybe"}); apiCode(err) != "DECISION_NOT_FOUND" {
		t.Fatalf("missing call error = %v", err)
	}
}

func TestDismissAndReopen(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	call := raiseDecision(t, fixture, fixture.w1)
	person := Actor{Surface: domain.RequestSurfaceDesktop}

	if _, err := fixture.service.Dismiss(ctx, Actor{Session: fixture.w1}, call.ID, "Not enough to go on"); apiCode(err) != "DECISION_PERSON_ONLY" {
		t.Fatalf("session dismiss error = %v", err)
	}
	dismissed, err := fixture.service.Dismiss(ctx, person, call.ID, "Not enough to go on")
	if err != nil {
		t.Fatal(err)
	}
	if dismissed.State != domain.DecisionStateDismissed || dismissed.Note != "Not enough to go on" {
		t.Fatalf("dismissed call = %+v", dismissed)
	}
	_, err = fixture.service.Dismiss(ctx, person, call.ID, "")
	assertDecisionAPIError(t, err, apierr.KindConflict, "DECISION_WRONG_STATE", "", `Only a call in "Needs you" can be dismissed. This one is "Dismissed".`)

	reopened, err := fixture.service.Answer(ctx, person, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.State != domain.DecisionStateInProgress {
		t.Fatalf("reopened call = %+v", reopened)
	}
}

func TestOnlyTheOwnerOnThisMachineCanWithdraw(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	call := raiseDecision(t, fixture, fixture.w1)

	for _, actor := range []Actor{{Session: fixture.w2}, {Surface: domain.RequestSurfaceDesktop}} {
		if _, err := fixture.service.Withdraw(ctx, actor, call.ID); apiCode(err) != "DECISION_NOT_OWNER" {
			t.Fatalf("Withdraw as %+v error = %v", actor, err)
		}
	}
	appendRemoteRaisedCall(t, fixture, "dec_far")
	if _, err := fixture.service.Withdraw(ctx, Actor{Session: fixture.w1}, "dec_far"); apiCode(err) != "DECISION_NOT_OWNER" {
		t.Fatalf("remote withdraw error = %v", err)
	}
	if _, err := fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w1}, "dec_far", OutcomeInput{Text: "Done"}); apiCode(err) != "DECISION_NOT_OWNER" {
		t.Fatalf("remote outcome error = %v", err)
	}

	withdrawn, err := fixture.service.Withdraw(ctx, Actor{Session: fixture.w1}, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.State != domain.DecisionStateWithdrawn {
		t.Fatalf("withdrawn call = %+v", withdrawn)
	}
	_, err = fixture.service.Answer(ctx, Actor{Surface: domain.RequestSurfaceDesktop}, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1})
	assertDecisionAPIError(t, err, apierr.KindConflict, "DECISION_WRONG_STATE", "", "")
}

func TestOutcomeAndRetire(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	call := raiseDecision(t, fixture, fixture.w1)
	person := Actor{Surface: domain.RequestSurfaceDesktop}

	_, err := fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w1}, call.ID, OutcomeInput{Text: "Done"})
	assertDecisionAPIError(t, err, apierr.KindConflict, "DECISION_WRONG_STATE", "", "")
	answered, err := fixture.service.Answer(ctx, person, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w2}, call.ID, OutcomeInput{Text: "Done"}); apiCode(err) != "DECISION_NOT_OWNER" {
		t.Fatalf("other session outcome error = %v", err)
	}
	_, err = fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w1}, call.ID, OutcomeInput{Text: "Done"})
	assertDecisionAPIError(t, err, apierr.KindConflict, "DECISION_ANSWER_CHANGED", "", "")
	_, err = fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w1}, call.ID, OutcomeInput{ForEntry: answered.StandingEntryID, Text: "a\nb"})
	assertDecisionAPIError(t, err, apierr.KindInvalid, "DECISION_INVALID", "outcome_one_line", "")

	appendDeliveredEntry(t, fixture, answered, fixture.orch)
	completed, err := fixture.service.RecordOutcome(ctx, Actor{Session: fixture.orch}, call.ID, OutcomeInput{
		ForEntry: answered.StandingEntryID,
		Text:     "Shipped in PR 412",
		Link:     "https://example.com/pr/412",
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != domain.DecisionStateDone || completed.Outcome != "Shipped in PR 412" || completed.OutcomeLink != "https://example.com/pr/412" {
		t.Fatalf("completed call = %+v", completed)
	}
	entries, err := fixture.store.ListDecisionEntries(ctx, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	var outcome domain.DecisionOutcomePayload
	if last := entries[len(entries)-1]; last.Kind != domain.DecisionKindOutcome || json.Unmarshal(last.Payload, &outcome) != nil || outcome.ForEntry != answered.StandingEntryID {
		t.Fatalf("outcome entry = %+v payload=%+v", last, outcome)
	}
	if _, err := fixture.service.Retire(ctx, Actor{Session: fixture.w1}, call.ID, true); apiCode(err) != "DECISION_PERSON_ONLY" {
		t.Fatalf("session retire error = %v", err)
	}
	retired, err := fixture.service.Retire(ctx, person, call.ID, true)
	if err != nil || !retired.Retired {
		t.Fatalf("retired call = %+v, err=%v", retired, err)
	}
	active, err := fixture.service.Retire(ctx, person, call.ID, false)
	if err != nil || active.Retired {
		t.Fatalf("active call = %+v, err=%v", active, err)
	}

	unfinished := raiseDecision(t, fixture, fixture.w2)
	_, err = fixture.service.Retire(ctx, person, unfinished.ID, true)
	assertDecisionAPIError(t, err, apierr.KindConflict, "DECISION_WRONG_STATE", "", "")
}

func TestDecisionActionsNormalizeStoredText(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	person := Actor{Surface: domain.RequestSurfaceCLI}

	answered, err := fixture.service.Answer(ctx, person, raiseDecision(t, fixture, fixture.w1).ID, AnswerInput{
		Choice: domain.DecisionChoiceOption, Option: 1, Note: "  Rotate monthly. \n",
	})
	if err != nil || answered.Note != "Rotate monthly." {
		t.Fatalf("answered = %+v, err=%v", answered, err)
	}
	dismissed, err := fixture.service.Dismiss(ctx, person, raiseDecision(t, fixture, fixture.w2).ID, "\t Not enough context.  ")
	if err != nil || dismissed.Note != "Not enough context." {
		t.Fatalf("dismissed = %+v, err=%v", dismissed, err)
	}
	call := raiseDecision(t, fixture, fixture.epic)
	if _, err := fixture.service.Answer(ctx, person, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 2}); err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.service.RecordOutcome(ctx, person, call.ID, OutcomeInput{
		Text: "  Shipped in PR 412  ", Link: "  https://example.com/pr/412  ",
	})
	if err != nil || completed.Outcome != "Shipped in PR 412" || completed.OutcomeLink != "https://example.com/pr/412" {
		t.Fatalf("completed = %+v, err=%v", completed, err)
	}
}

func TestDecisionActionsSerializeStateCheckWithAppend(t *testing.T) {
	fixture := newDecisionService(t)
	call := raiseDecision(t, fixture, fixture.w1)
	store := &interleavingDecisionStore{
		Store: fixture.store, answerAppending: make(chan struct{}), releaseAnswer: make(chan struct{}),
		withdrawRead: make(chan struct{}), releaseWithdrawGet: make(chan struct{}),
	}
	var nextID atomic.Int64
	service := New(Deps{
		Store: store, HostID: "h_a", Now: func() time.Time { return time.UnixMilli(4_000_000).UTC() },
		NewID: func(prefix string) string { return fmt.Sprintf("%sconcurrent-%d", prefix, nextID.Add(1)) },
	})
	answerDone := make(chan error, 1)
	go func() {
		_, err := service.Answer(context.Background(), Actor{Surface: domain.RequestSurfaceCLI}, call.ID, AnswerInput{
			Choice: domain.DecisionChoiceOption, Option: 1,
		})
		answerDone <- err
	}()
	<-store.answerAppending

	withdrawDone := make(chan error, 1)
	go func() {
		_, err := service.Withdraw(context.Background(), Actor{Session: fixture.w1}, call.ID)
		withdrawDone <- err
	}()
	select {
	case <-store.withdrawRead:
	case <-time.After(200 * time.Millisecond):
	}
	close(store.releaseAnswer)
	close(store.releaseWithdrawGet)

	if err := <-answerDone; err != nil {
		t.Fatalf("answer error = %v", err)
	}
	if err := <-withdrawDone; apiCode(err) != "DECISION_WRONG_STATE" {
		t.Fatalf("withdraw error = %v, want DECISION_WRONG_STATE", err)
	}
	stored := getDecisionCall(t, fixture, call.ID)
	if stored.State != domain.DecisionStateInProgress {
		t.Fatalf("stored call = %+v", stored)
	}
}

func TestThePersonCanRecordTheOutcome(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	call := raiseDecision(t, fixture, fixture.w1)
	person := Actor{Surface: domain.RequestSurfaceCLI}
	if _, err := fixture.service.Answer(ctx, person, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1}); err != nil {
		t.Fatal(err)
	}
	completed, err := fixture.service.RecordOutcome(ctx, person, call.ID, OutcomeInput{Text: "Did it by hand"})
	if err != nil || completed.State != domain.DecisionStateDone {
		t.Fatalf("completed call = %+v, err=%v", completed, err)
	}
}

func TestOutcomeCannotCloseANewerAnswer(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	person := Actor{Surface: domain.RequestSurfaceCLI}
	call := raiseDecision(t, fixture, fixture.w1)
	first, err := fixture.service.Answer(ctx, person, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1})
	if err != nil {
		t.Fatal(err)
	}
	appendDeliveredEntry(t, fixture, first, fixture.w1)
	second, err := fixture.service.Answer(ctx, person, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 2})
	if err != nil {
		t.Fatal(err)
	}

	_, err = fixture.service.RecordOutcome(ctx, Actor{Session: fixture.w1}, call.ID, OutcomeInput{ForEntry: first.StandingEntryID, Text: "Finished the first answer"})
	assertDecisionAPIError(t, err, apierr.KindConflict, "DECISION_ANSWER_CHANGED", "", "")
	stored := getDecisionCall(t, fixture, call.ID)
	if stored.State != domain.DecisionStateInProgress || stored.StandingEntryID != second.StandingEntryID || stored.Option != 2 {
		t.Fatalf("stored call = %+v", stored)
	}
}

func TestSearchPutsRetiredDecisionsLast(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	person := Actor{Surface: domain.RequestSurfaceDesktop}
	old := raiseDecision(t, fixture, fixture.w1)
	if _, err := fixture.service.Answer(ctx, person, old.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RecordOutcome(ctx, person, old.ID, OutcomeInput{Text: "Done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Retire(ctx, person, old.ID, true); err != nil {
		t.Fatal(err)
	}
	current := raiseDecision(t, fixture, fixture.w2)

	calls, err := fixture.service.Search(ctx, Actor{Session: fixture.w1}, "auth scheme", 0)
	if err != nil || callIDs(calls) != current.ID+","+old.ID {
		t.Fatalf("search = %q, err=%v", callIDs(calls), err)
	}
}

func raiseDecision(t *testing.T, fixture decisionServiceFixture, session domain.SessionID) domain.DecisionCall {
	t.Helper()
	call, err := fixture.service.Raise(context.Background(), Actor{Session: session}, validRaiseInput())
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func appendRemoteRaisedCall(t *testing.T, fixture decisionServiceFixture, callID string) {
	t.Helper()
	payload := domain.DecisionRaisedPayload{
		Title: "Remote call", Context: "Raised on another machine.", MachineLabel: "other-mac",
		OwnerSessionID: fixture.w1, OwnerSessionName: "w-1",
		Options: []domain.DecisionOption{
			{Label: "One", Consequence: "First consequence.", Recommended: true},
			{Label: "Two", Consequence: "Second consequence."},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = fixture.store.AppendDecisionEntry(context.Background(), domain.DecisionEntry{
		ID: "dent_far", Origin: "h_b", CallID: callID, Kind: domain.DecisionKindRaised,
		Payload: raw, WrittenAt: time.UnixMilli(2_000_000).UTC(),
		AuthorKind: domain.DecisionAuthorSession, AuthorSession: fixture.w1,
	}, time.UnixMilli(2_000_000).UTC())
	if err != nil {
		t.Fatal(err)
	}
}

func appendDeliveredEntry(t *testing.T, fixture decisionServiceFixture, call domain.DecisionCall, session domain.SessionID) {
	t.Helper()
	raw, err := json.Marshal(domain.DecisionDeliveredPayload{
		ForEntry: call.StandingEntryID, Status: domain.DecisionDeliveredSent,
		SessionID: session, SessionName: "Orchestrator", Fallback: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = fixture.store.AppendDecisionEntry(context.Background(), domain.DecisionEntry{
		ID: "dent_delivered", Origin: "h_a", CallID: call.ID, Kind: domain.DecisionKindDelivered,
		Payload: raw, WrittenAt: time.UnixMilli(3_000_000).UTC(), AuthorKind: domain.DecisionAuthorSystem,
	}, time.UnixMilli(3_000_000).UTC())
	if err != nil {
		t.Fatal(err)
	}
}

func assertDecisionAPIError(t *testing.T, err error, kind apierr.Kind, code, rule, message string) {
	t.Helper()
	var apiError *apierr.Error
	if !errors.As(err, &apiError) {
		t.Fatalf("error = %v, want api error", err)
	}
	if apiError.Kind != kind || apiError.Code != code {
		t.Fatalf("api error = %+v, want kind=%v code=%q", apiError, kind, code)
	}
	if rule != "" && apiError.Details["rule"] != rule {
		t.Fatalf("api error rule = %v, want %q", apiError.Details["rule"], rule)
	}
	if code == "DECISION_WRONG_STATE" && apiError.Details["state"] == nil {
		t.Fatalf("api error details = %+v, want state", apiError.Details)
	}
	if message != "" && apiError.Message != message {
		t.Fatalf("api error message = %q, want %q", apiError.Message, message)
	}
}

func answeredEntries(entries []domain.DecisionEntry, callID string) int {
	n := 0
	for _, entry := range entries {
		if entry.CallID == callID && entry.Kind == domain.DecisionKindAnswered {
			n++
		}
	}
	return n
}

func TestAnswerWithTheSameIdempotencyKeyIsRecordedOnce(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	call := raiseDecision(t, fixture, fixture.w1)
	person := Actor{Surface: domain.RequestSurfaceDesktop}

	first, err := fixture.service.Answer(ctx, person, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 2, IdempotencyKey: "ans_1"})
	if err != nil {
		t.Fatal(err)
	}
	// The retry of an uncertain send carries the same key; even with a different
	// pick it returns what was recorded instead of appending a second answer.
	again, err := fixture.service.Answer(ctx, person, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1, IdempotencyKey: "ans_1"})
	if err != nil {
		t.Fatalf("repeat with the same key: %v", err)
	}
	if again.Option != 2 || again.StandingEntryID != first.StandingEntryID {
		t.Fatalf("repeat returned %+v, want the first answer %+v", again, first)
	}
	if n := answeredEntries(*fixture.appended, call.ID); n != 1 {
		t.Fatalf("answered entries = %d, want 1", n)
	}

	// A new key (a new deliberate answer) and a keyless answer still append.
	if _, err := fixture.service.Answer(ctx, person, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1, IdempotencyKey: "ans_2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.Answer(ctx, person, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 2}); err != nil {
		t.Fatal(err)
	}
	if n := answeredEntries(*fixture.appended, call.ID); n != 3 {
		t.Fatalf("answered entries = %d, want 3", n)
	}
}

func TestAnswerIdempotencyKeyIsScopedToItsCall(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	person := Actor{Surface: domain.RequestSurfaceDesktop}
	first := raiseDecision(t, fixture, fixture.w1)
	second := raiseDecision(t, fixture, fixture.w2)
	for _, call := range []domain.DecisionCall{first, second} {
		if _, err := fixture.service.Answer(ctx, person, call.ID, AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1, IdempotencyKey: "same"}); err != nil {
			t.Fatal(err)
		}
		if n := answeredEntries(*fixture.appended, call.ID); n != 1 {
			t.Fatalf("call %s answered entries = %d, want 1", call.ID, n)
		}
	}
}

func TestAnswerRejectsAnUnusableIdempotencyKey(t *testing.T) {
	fixture := newDecisionService(t)
	call := raiseDecision(t, fixture, fixture.w1)
	for _, key := range []string{strings.Repeat("k", domain.MaxDecisionIdempotencyKey+1), "two\nlines"} {
		_, err := fixture.service.Answer(context.Background(), Actor{Surface: domain.RequestSurfaceDesktop}, call.ID,
			AnswerInput{Choice: domain.DecisionChoiceOption, Option: 1, IdempotencyKey: key})
		if apiCode(err) != "DECISION_INVALID" {
			t.Fatalf("key %q: error = %v", key, err)
		}
	}
}

// stallingAnswerStore holds the first answered append until released, like a
// daemon still recording an answer after the client's request timed out.
type stallingAnswerStore struct {
	Store
	appending chan struct{}
	release   chan struct{}
	once      sync.Once
}

func (s *stallingAnswerStore) AppendDecisionEntry(ctx context.Context, entry domain.DecisionEntry, at time.Time) (domain.DecisionEntry, domain.DecisionCall, error) {
	if entry.Kind == domain.DecisionKindAnswered {
		s.once.Do(func() {
			close(s.appending)
			<-s.release
		})
	}
	return s.Store.AppendDecisionEntry(ctx, entry, at)
}

// A proxy can time out while the daemon is still recording the answer. A read
// does not take the call's lock, so it can still show Needs you; the person's
// retry with the same key must then wait for the original and return it.
func TestAnswerRetryAfterADelayedCommitReturnsTheOriginal(t *testing.T) {
	fixture := newDecisionService(t)
	ctx := context.Background()
	call := raiseDecision(t, fixture, fixture.w1)
	stalled := &stallingAnswerStore{Store: fixture.store, appending: make(chan struct{}), release: make(chan struct{})}
	service := New(Deps{Store: stalled, HostID: "h_a", MachineLabel: func() string { return "mac-mini" }})
	person := Actor{Surface: domain.RequestSurfaceDesktop}
	answer := AnswerInput{Choice: domain.DecisionChoiceOption, Option: 2, IdempotencyKey: "ans_delayed"}

	original := make(chan error, 1)
	go func() {
		_, err := service.Answer(ctx, person, call.ID, answer)
		original <- err
	}()
	<-stalled.appending

	// The client's request timed out; its re-read still sees the call waiting.
	seen, ok, err := fixture.store.GetDecisionCall(ctx, call.ID)
	if err != nil || !ok || seen.State != domain.DecisionStateNeedsYou {
		t.Fatalf("read during the stalled commit = %+v ok=%v err=%v", seen, ok, err)
	}
	retried := make(chan domain.DecisionCall, 1)
	retryErr := make(chan error, 1)
	go func() {
		got, err := service.Answer(ctx, person, call.ID, answer)
		retried <- got
		retryErr <- err
	}()
	close(stalled.release)
	if err := <-original; err != nil {
		t.Fatalf("original answer: %v", err)
	}
	if err := <-retryErr; err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := <-retried; got.State != domain.DecisionStateInProgress || got.Option != 2 {
		t.Fatalf("retry returned %+v", got)
	}
	entries, err := fixture.store.ListDecisionEntries(ctx, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n := answeredEntries(entries, call.ID); n != 1 {
		t.Fatalf("answered entries = %d, want 1 (the retry must not append)", n)
	}
}
