package daemon

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type contractNudgeStoreStub struct {
	rec domain.SessionRecord
}

func (s *contractNudgeStoreStub) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return s.rec, s.rec.ID != "", nil
}

type contractChatNudgerStub struct {
	calls []ports.ChatUserMessage
}

func (s *contractChatNudgerStub) SteerOrSend(_ context.Context, _ domain.SessionID, msg ports.ChatUserMessage, _ bool) (chatsvc.SteerOrSendResult, error) {
	s.calls = append(s.calls, msg)
	return chatsvc.SteerOrSendResult{Steered: true}, nil
}

type contractSemanticNudgerStub struct {
	keys []string
}

func (s *contractSemanticNudgerStub) SendSemantic(_ context.Context, _ domain.SessionID, _ string, key string) error {
	s.keys = append(s.keys, key)
	return nil
}

type contractTUINudgerStub struct {
	deadline int
	idle     int
}

func (s *contractTUINudgerStub) NudgeSession(context.Context, domain.SessionID, string) (bool, error) {
	s.deadline++
	return true, nil
}

func (s *contractTUINudgerStub) NudgeIdleSession(context.Context, domain.SessionID, string) (bool, error) {
	s.idle++
	return true, nil
}

func TestContractNudgerSteersActiveChatDeadlineWithStableKey(t *testing.T) {
	store := &contractNudgeStoreStub{rec: domain.SessionRecord{ID: "worker", Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityActive}}}
	chat := &contractChatNudgerStub{}
	n := contractNudger{store: store, chat: chat}

	sent, err := n.NudgeSession(context.Background(), "worker", "deadline-key", "wrap up")
	if err != nil || !sent || len(chat.calls) != 1 {
		t.Fatalf("sent=%v calls=%+v err=%v", sent, chat.calls, err)
	}
	if got := chat.calls[0]; got.Text != "wrap up" || got.ClientMessageID != "deadline-key" || got.Origin != domain.MessageOriginAutomation {
		t.Fatalf("message=%+v", got)
	}
}

func TestContractNudgerUsesSemanticSendOnlyForIdleChatStall(t *testing.T) {
	store := &contractNudgeStoreStub{rec: domain.SessionRecord{ID: "worker", Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}}
	semantic := &contractSemanticNudgerStub{}
	n := contractNudger{store: store, semantic: semantic}

	sent, err := n.NudgeIdleSession(context.Background(), "worker", "stall-key", "continue")
	if err != nil || !sent || len(semantic.keys) != 1 || semantic.keys[0] != "stall-key" {
		t.Fatalf("sent=%v keys=%v err=%v", sent, semantic.keys, err)
	}
	store.rec.Activity.State = domain.ActivityActive
	sent, err = n.NudgeIdleSession(context.Background(), "worker", "stale-key", "continue")
	if err != nil || sent || len(semantic.keys) != 1 {
		t.Fatalf("active sent=%v keys=%v err=%v", sent, semantic.keys, err)
	}
}

func TestContractNudgerSuppressesNeedsInputAndDelegatesTUI(t *testing.T) {
	store := &contractNudgeStoreStub{rec: domain.SessionRecord{ID: "worker", Activity: domain.Activity{State: domain.ActivityWaitingInput}}}
	tui := &contractTUINudgerStub{}
	n := contractNudger{store: store, tui: tui}

	if sent, err := n.NudgeSession(context.Background(), "worker", "deadline-key", "wrap up"); err != nil || sent {
		t.Fatalf("waiting sent=%v err=%v", sent, err)
	}
	store.rec.Activity.State = domain.ActivityIdle
	if sent, err := n.NudgeIdleSession(context.Background(), "worker", "stall-key", "continue"); err != nil || !sent {
		t.Fatalf("idle sent=%v err=%v", sent, err)
	}
	if tui.deadline != 0 || tui.idle != 1 {
		t.Fatalf("tui deadline=%d idle=%d", tui.deadline, tui.idle)
	}
}
