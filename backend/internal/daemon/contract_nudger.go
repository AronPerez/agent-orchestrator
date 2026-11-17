package daemon

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type contractNudgeStore interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
}

type contractChatNudger interface {
	SteerOrSend(context.Context, domain.SessionID, ports.ChatUserMessage, bool) (chatsvc.SteerOrSendResult, error)
}

type contractSemanticNudger interface {
	SendSemantic(context.Context, domain.SessionID, string, string) error
}

type contractTUINudger interface {
	NudgeSession(context.Context, domain.SessionID, string) (bool, error)
	NudgeIdleSession(context.Context, domain.SessionID, string) (bool, error)
}

// contractNudger selects the delivery primitive from a fresh session read.
// Chat deadlines use its atomic steer-or-send path, while TUI writes remain
// behind lifecycle's capability-aware input guard.
type contractNudger struct {
	store    contractNudgeStore
	chat     contractChatNudger
	semantic contractSemanticNudger
	tui      contractTUINudger
}

func (n contractNudger) NudgeSession(ctx context.Context, id domain.SessionID, key, message string) (bool, error) {
	rec, ok, err := n.session(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if n.chat == nil {
			return false, nil
		}
		_, err := n.chat.SteerOrSend(ctx, id, ports.ChatUserMessage{
			Text: message, ClientMessageID: key, Origin: domain.MessageOriginAutomation,
		}, false)
		return err == nil, err
	}
	if n.tui == nil {
		return false, nil
	}
	return n.tui.NudgeSession(ctx, id, message)
}

func (n contractNudger) NudgeIdleSession(ctx context.Context, id domain.SessionID, key, message string) (bool, error) {
	rec, ok, err := n.session(ctx, id)
	if err != nil || !ok {
		return false, err
	}
	if rec.Activity.State != domain.ActivityIdle {
		return false, nil
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		if n.semantic == nil {
			return false, nil
		}
		err := n.semantic.SendSemantic(ctx, id, message, key)
		return err == nil, err
	}
	if n.tui == nil {
		return false, nil
	}
	return n.tui.NudgeIdleSession(ctx, id, message)
}

func (n contractNudger) session(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if n.store == nil {
		return domain.SessionRecord{}, false, nil
	}
	rec, ok, err := n.store.GetSession(ctx, id)
	if err != nil || !ok || rec.IsTerminated || rec.Activity.State == domain.ActivityExited || rec.Activity.State.NeedsInput() {
		return rec, false, err
	}
	return rec, true, nil
}
