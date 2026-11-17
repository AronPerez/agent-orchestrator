package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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

	m, store, messenger = newManager(WithActiveSteering(func(harness domain.AgentHarness) bool { return harness == domain.HarnessCodex }))
	rec := working("mer-1")
	rec.Harness = domain.HarnessCodex
	store.sessions[rec.ID] = rec
	outcome, err = m.NudgeCoordination(context.Background(), rec.ID, "coordinate", "collision-2")
	if err != nil {
		t.Fatalf("steerable nudge: %v", err)
	}
	if outcome != sessionguard.Sent || len(messenger.msgs) != 1 {
		t.Fatalf("outcome=%s messages=%v, want sent coordination", outcome, messenger.msgs)
	}
	deliveryID, wrapped := domain.SessionDeliveryID(messenger.msgs[0])
	if !strings.Contains(messenger.msgs[0], "coordinate") || !wrapped || deliveryID != "collision-2" {
		t.Fatalf("message=%q deliveryID=%q, want wrapped collision-2 coordination", messenger.msgs[0], deliveryID)
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
}

func (m *recordingSemanticCoordinationMessenger) Send(context.Context, domain.SessionID, string) error {
	m.normalCalls++
	return nil
}

func (m *recordingSemanticCoordinationMessenger) SendSemantic(_ context.Context, _ domain.SessionID, message, deliveryID string) error {
	m.semanticCalls = append(m.semanticCalls, semanticCoordinationCall{message: message, deliveryID: deliveryID})
	return nil
}

func (m *appliedErrorMessenger) Send(context.Context, domain.SessionID, string) error {
	m.calls++
	return m.err
}
