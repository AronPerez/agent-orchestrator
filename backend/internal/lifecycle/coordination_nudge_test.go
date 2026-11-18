package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

func TestNudgeCoordinationUsesSharedGuardActiveSteeringPolicy(t *testing.T) {
	m, store, messenger := newManager()
	store.sessions["mer-1"] = working("mer-1")

	outcome, err := m.NudgeCoordination(context.Background(), "mer-1", "coordinate", "collision-1")
	if err != nil {
		t.Fatalf("suppressed nudge: %v", err)
	}
	if outcome != sessionguard.SuppressedBusy || len(messenger.msgs) != 0 {
		t.Fatalf("outcome=%s messages=%v, want busy suppression", outcome, messenger.msgs)
	}

	store = newFakeStore()
	semanticMessenger := &recordingSemanticCoordinationMessenger{}
	m = New(store, semanticMessenger, WithActiveSteering(func(harness domain.AgentHarness) bool { return harness == domain.HarnessCodex }))
	rec := working("mer-1")
	rec.Harness = domain.HarnessCodex
	store.sessions[rec.ID] = rec
	outcome, err = m.NudgeCoordination(context.Background(), rec.ID, "coordinate", "collision-2")
	if err != nil {
		t.Fatalf("steerable nudge: %v", err)
	}
	if outcome != sessionguard.Sent || len(semanticMessenger.semanticCalls) != 1 {
		t.Fatalf("outcome=%s semantic=%+v, want accepted semantic coordination", outcome, semanticMessenger.semanticCalls)
	}
	if call := semanticMessenger.semanticCalls[0]; call.message != "coordinate" || call.deliveryID != "collision-2" {
		t.Fatalf("semantic coordination = %+v, want collision-2", call)
	}
}

func TestNudgeCoordinationQueuesForActiveChatWithoutTerminalSteering(t *testing.T) {
	store := newFakeStore()
	messenger := &recordingSemanticCoordinationMessenger{}
	m := New(store, messenger)
	rec := working("chat-1")
	rec.Mode = domain.SessionModeChat
	store.sessions[rec.ID] = rec

	outcome, err := m.NudgeCoordination(context.Background(), rec.ID, "coordinate", "collision-chat")
	if err != nil {
		t.Fatalf("chat nudge: %v", err)
	}
	if outcome != sessionguard.Sent || messenger.normalCalls != 0 || len(messenger.semanticCalls) != 1 {
		t.Fatalf("outcome=%s normal=%d semantic=%+v, want one semantic Chat delivery", outcome, messenger.normalCalls, messenger.semanticCalls)
	}
	if call := messenger.semanticCalls[0]; call.message != "coordinate" || call.deliveryID != "collision-chat" {
		t.Fatalf("semantic Chat delivery = %+v, want plain message with stable collision id", call)
	}
}

func TestNudgeCoordinationRetriesAmbiguousChatWithSameIdentity(t *testing.T) {
	store := newFakeStore()
	messenger := &recordingSemanticCoordinationMessenger{
		errors:        []error{errors.New("dispatch unconfirmed")},
		confirmErrors: []error{nil},
	}
	m := New(store, messenger)
	rec := working("chat-1")
	rec.Mode = domain.SessionModeChat
	store.sessions[rec.ID] = rec

	outcome, err := m.NudgeCoordination(context.Background(), rec.ID, "coordinate", "collision-chat")
	if outcome != sessionguard.Sent || err == nil {
		t.Fatalf("first Chat nudge = (%s, %v), want admitted unconfirmed delivery", outcome, err)
	}
	outcome, err = m.ConfirmCoordination(context.Background(), rec.ID, "coordinate", "collision-chat")
	if outcome != sessionguard.Sent || err != nil {
		t.Fatalf("Chat confirmation = (%s, %v), want idempotent retry accepted", outcome, err)
	}
	if messenger.normalCalls != 0 || len(messenger.semanticCalls) != 1 || len(messenger.confirmCalls) != 1 {
		t.Fatalf("normal=%d semantic=%+v confirmations=%+v, want one send and one read-only confirmation", messenger.normalCalls, messenger.semanticCalls, messenger.confirmCalls)
	}
	for _, call := range append(messenger.semanticCalls, messenger.confirmCalls...) {
		if call.message != "coordinate" || call.deliveryID != "collision-chat" {
			t.Fatalf("semantic Chat attempt = %+v, want stable message identity", call)
		}
	}
}

func TestNudgeCoordinationDoesNotDropChatDeliveryIdentity(t *testing.T) {
	m, store, messenger := newManager()
	rec := working("chat-1")
	rec.Mode = domain.SessionModeChat
	store.sessions[rec.ID] = rec

	outcome, err := m.NudgeCoordination(context.Background(), rec.ID, "coordinate", "collision-chat")
	if outcome != sessionguard.Sent || !errors.Is(err, errCoordinationSemanticDeliveryUnsupported) {
		t.Fatalf("chat nudge = (%s, %v), want admitted semantic-delivery error", outcome, err)
	}
	if len(messenger.msgs) != 0 {
		t.Fatalf("plain Chat messages = %v, want keyed delivery to fail closed", messenger.msgs)
	}
}

func TestNudgeCoordinationConfirmsAmbiguousAttemptBeforeResending(t *testing.T) {
	store := newFakeStore()
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityIdle
	rec.Metadata.RuntimeLaunchID = "launch-1"
	store.sessions[rec.ID] = rec
	messenger := &appliedErrorMessenger{err: errors.New("delivery unconfirmed")}
	m := New(store, messenger)

	outcome, err := m.NudgeCoordination(context.Background(), rec.ID, "coordinate", "collision-attempt")
	if outcome != sessionguard.Sent || err == nil || messenger.calls != 1 {
		t.Fatalf("first attempt outcome=%s err=%v calls=%d, want sent/unconfirmed/1", outcome, err, messenger.calls)
	}
	outcome, err = m.ConfirmCoordination(context.Background(), rec.ID, "coordinate", "collision-attempt")
	if outcome != sessionguard.Sent || !errors.Is(err, ErrCoordinationDeliveryUnconfirmed) || messenger.calls != 1 {
		t.Fatalf("unconfirmed retry outcome=%s err=%v calls=%d, want sent/unconfirmed without another write", outcome, err, messenger.calls)
	}
	store.accepted[string(rec.ID)+"\x00launch-1\x00collision-attempt"] = true
	messenger.err = nil
	outcome, err = m.ConfirmCoordination(context.Background(), rec.ID, "coordinate", "collision-attempt")
	if err != nil || outcome != sessionguard.Sent || messenger.calls != 1 {
		t.Fatalf("confirmed retry outcome=%s err=%v calls=%d, want sent/nil without another write", outcome, err, messenger.calls)
	}
}

func TestNudgeCoordinationPreservesDefiniteComposerRefusal(t *testing.T) {
	store := newFakeStore()
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityIdle
	store.sessions[rec.ID] = rec
	messenger := &appliedErrorMessenger{err: sessionmanager.ErrComposerBusy}
	m := New(store, messenger)

	outcome, err := m.NudgeCoordination(context.Background(), rec.ID, "coordinate", "collision-attempt")
	if outcome != sessionguard.Sent || !errors.Is(err, sessionmanager.ErrComposerBusy) || messenger.calls != 1 {
		t.Fatalf("composer refusal outcome=%s err=%v calls=%d, want sent/ErrComposerBusy/1", outcome, err, messenger.calls)
	}
}

func TestNudgeCoordinationTUIRequiresSemanticAcceptance(t *testing.T) {
	store := newFakeStore()
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityIdle
	store.sessions[rec.ID] = rec
	messenger := &recordingSemanticCoordinationMessenger{
		errors: []error{sessionmanager.ErrDeliveryUnconfirmed},
	}
	m := New(store, messenger)

	outcome, err := m.NudgeCoordination(context.Background(), rec.ID, "coordinate", "collision-attempt")
	if outcome != sessionguard.Sent || !errors.Is(err, sessionmanager.ErrDeliveryUnconfirmed) {
		t.Fatalf("unconfirmed TUI nudge = (%s, %v), want sent/ErrDeliveryUnconfirmed", outcome, err)
	}
	if messenger.normalCalls != 0 || len(messenger.semanticCalls) != 1 {
		t.Fatalf("normal=%d semantic=%+v, want semantic acceptance instead of ordinary send", messenger.normalCalls, messenger.semanticCalls)
	}
}

type appliedErrorMessenger struct {
	calls int
	err   error
}

type semanticCoordinationCall struct {
	message    string
	deliveryID string
}

type recordingSemanticCoordinationMessenger struct {
	normalCalls   int
	semanticCalls []semanticCoordinationCall
	confirmCalls  []semanticCoordinationCall
	errors        []error
	confirmErrors []error
}

func (m *recordingSemanticCoordinationMessenger) Send(context.Context, domain.SessionID, string) error {
	m.normalCalls++
	return nil
}

func (m *recordingSemanticCoordinationMessenger) SendSemantic(_ context.Context, _ domain.SessionID, message, deliveryID string) error {
	m.semanticCalls = append(m.semanticCalls, semanticCoordinationCall{message: message, deliveryID: deliveryID})
	if len(m.errors) > 0 {
		err := m.errors[0]
		m.errors = m.errors[1:]
		return err
	}
	return nil
}

func (m *recordingSemanticCoordinationMessenger) ConfirmSemantic(_ context.Context, _ domain.SessionID, message, deliveryID string) error {
	m.confirmCalls = append(m.confirmCalls, semanticCoordinationCall{message: message, deliveryID: deliveryID})
	if len(m.confirmErrors) > 0 {
		err := m.confirmErrors[0]
		m.confirmErrors = m.confirmErrors[1:]
		return err
	}
	return nil
}

func (m *appliedErrorMessenger) Send(context.Context, domain.SessionID, string) error {
	m.calls++
	return m.err
}

func (m *appliedErrorMessenger) SendSemantic(context.Context, domain.SessionID, string, string) error {
	m.calls++
	return m.err
}
