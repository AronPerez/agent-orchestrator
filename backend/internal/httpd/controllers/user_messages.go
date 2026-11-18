package controllers

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// UserMessageRecorder counts a human send that the Chat message insert does not
// count itself: a steer into a running turn, or a `/send` (`ao send` outside an
// AO session, or a TUI session's composer). It receives no message content.
// Controllers call it only after the send was accepted; recordUserMessage
// excludes both local and cross-machine automation before recording usage.
type UserMessageRecorder interface {
	RecordUserMessage(ctx context.Context, id domain.SessionID, isSteer bool)
}

func recordUserMessage(ctx context.Context, recorder UserMessageRecorder, id domain.SessionID, senderSessionID string, automationAuthored, isSteer bool) {
	if recorder == nil || automationAuthored || senderSessionID != "" {
		return
	}
	recorder.RecordUserMessage(ctx, id, isSteer)
}
