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
	calls     []ports.ChatUserMessage
	err       error
	idleSent  bool
	idleCalls []ports.ChatUserMessage
}

func (s *contractChatNudgerStub) SteerOrSend(_ context.Context, _ domain.SessionID, msg ports.ChatUserMessage, _ bool) (chatsvc.SteerOrSendResult, error) {
	s.calls = append(s.calls, msg)
	return chatsvc.SteerOrSendResult{Steered: s.err == nil}, s.err
}

func (s *contractChatNudgerStub) SendIfIdle(_ context.Context, _ domain.SessionID, msg ports.ChatUserMessage) (bool, error) {
	s.idleCalls = append(s.idleCalls, msg)
	return s.idleSent, nil
}

type contractTUINudgerStub struct {
	deadline int
	idle     int
	keys     []string
}

func (s *contractTUINudgerStub) NudgeSession(_ context.Context, _ domain.SessionID, key, _ string) (bool, error) {
	s.deadline++
	s.keys = append(s.keys, key)
	return true, nil
}

func (s *contractTUINudgerStub) NudgeIdleSession(_ context.Context, _ domain.SessionID, key, _ string) (bool, error) {
	s.idle++
	s.keys = append(s.keys, key)
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

func TestContractNudgerRetriesUnsupportedChatDeadlineAsIdleSend(t *testing.T) {
	store := &contractNudgeStoreStub{rec: domain.SessionRecord{ID: "worker", Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityActive}}}
	chat := &contractChatNudgerStub{err: chatsvc.ErrSteerUnsupported}
	n := contractNudger{store: store, chat: chat}

	sent, err := n.NudgeSession(context.Background(), "worker", "deadline-key", "wrap up")
	if err != nil || sent || len(chat.idleCalls) != 1 {
		t.Fatalf("active sent=%v idle calls=%+v err=%v", sent, chat.idleCalls, err)
	}
	if got := chat.idleCalls[0]; got.ClientMessageID != "deadline-key:idle" || got.Text != "wrap up" {
		t.Fatalf("fallback message=%+v", got)
	}
	chat.idleSent = true
	sent, err = n.NudgeSession(context.Background(), "worker", "deadline-key", "wrap up")
	if err != nil || !sent || len(chat.calls) != 2 || len(chat.idleCalls) != 2 {
		t.Fatalf("idle recovery sent=%v steer calls=%d idle calls=%d err=%v", sent, len(chat.calls), len(chat.idleCalls), err)
	}
}

func TestContractNudgerUsesAtomicIdleSendForChatStall(t *testing.T) {
	store := &contractNudgeStoreStub{rec: domain.SessionRecord{ID: "worker", Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}}
	chat := &contractChatNudgerStub{idleSent: true}
	n := contractNudger{store: store, chat: chat}

	sent, err := n.NudgeIdleSession(context.Background(), "worker", "stall-key", "continue")
	if err != nil || !sent || len(chat.idleCalls) != 1 || chat.idleCalls[0].ClientMessageID != "stall-key" {
		t.Fatalf("sent=%v calls=%+v err=%v", sent, chat.idleCalls, err)
	}
	store.rec.Activity.State = domain.ActivityActive
	sent, err = n.NudgeIdleSession(context.Background(), "worker", "stale-key", "continue")
	if err != nil || sent || len(chat.idleCalls) != 1 {
		t.Fatalf("active sent=%v calls=%+v err=%v", sent, chat.idleCalls, err)
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
	if len(tui.keys) != 1 || tui.keys[0] != "stall-key" {
		t.Fatalf("tui keys=%v", tui.keys)
	}
}
