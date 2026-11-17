package lifecycle

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

func TestNudgeCoordinationUsesSharedGuardActiveSteeringPolicy(t *testing.T) {
	m, store, messenger := newManager()
	store.sessions["mer-1"] = working("mer-1")

	outcome, err := m.NudgeCoordination(context.Background(), "mer-1", "coordinate")
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
	outcome, err = m.NudgeCoordination(context.Background(), rec.ID, "coordinate")
	if err != nil {
		t.Fatalf("steerable nudge: %v", err)
	}
	if outcome != sessionguard.Sent || len(messenger.msgs) != 1 || messenger.msgs[0] != "coordinate" {
		t.Fatalf("outcome=%s messages=%v, want sent coordination", outcome, messenger.msgs)
	}
}

func TestNudgeCoordinationQueuesForActiveChatWithoutTerminalSteering(t *testing.T) {
	m, store, messenger := newManager()
	rec := working("chat-1")
	rec.Mode = domain.SessionModeChat
	store.sessions[rec.ID] = rec

	outcome, err := m.NudgeCoordination(context.Background(), rec.ID, "coordinate")
	if err != nil {
		t.Fatalf("chat nudge: %v", err)
	}
	if outcome != sessionguard.Sent || len(messenger.msgs) != 1 || messenger.msgs[0] != "coordinate" {
		t.Fatalf("outcome=%s messages=%v, want active Chat message queued", outcome, messenger.msgs)
	}
}
