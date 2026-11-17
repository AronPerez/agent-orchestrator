package daemon

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type contractNudgeStore interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
}

type contractChatNudger interface {
	SteerOrSend(context.Context, domain.SessionID, ports.ChatUserMessage, bool) (chatsvc.SteerOrSendResult, error)
	SendIfIdle(context.Context, domain.SessionID, ports.ChatUserMessage) (bool, error)
}

type contractTUINudger interface {
	NudgeSession(context.Context, domain.SessionID, string, string) (bool, error)
	NudgeIdleSession(context.Context, domain.SessionID, string, string) (bool, error)
}

// contractNudger selects the delivery primitive from a fresh session read.
// Chat deadlines use its atomic steer-or-send path, while TUI writes remain
// behind lifecycle's capability-aware input guard.
type contractNudger struct {
	store contractNudgeStore
	chat  contractChatNudger
	tui   contractTUINudger
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
		msg := ports.ChatUserMessage{
			Text: message, ClientMessageID: key, Origin: domain.MessageOriginAutomation,
		}
		_, err := n.chat.SteerOrSend(ctx, id, msg, false)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, chatsvc.ErrSteerUnsupported) {
			return false, err
		}
		msg.ClientMessageID = key + ":idle"
		return n.chat.SendIfIdle(ctx, id, msg)
	}
	if n.tui == nil {
		return false, nil
	}
	return n.tui.NudgeSession(ctx, id, key, message)
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
		if n.chat == nil {
			return false, nil
		}
		return n.chat.SendIfIdle(ctx, id, ports.ChatUserMessage{
			Text: message, ClientMessageID: key, Origin: domain.MessageOriginAutomation,
		})
	}
	if n.tui == nil {
		return false, nil
	}
	return n.tui.NudgeIdleSession(ctx, id, key, message)
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
