package lifecycle

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

// NudgeCoordination sends an unsolicited coordination message through the
// lifecycle manager's shared, mode-aware guard. Suppressed outcomes are not
// deliveries; callers may retry them when new state arrives.
func (m *Manager) NudgeCoordination(ctx context.Context, id domain.SessionID, message string) (sessionguard.Outcome, error) {
	if m.guard == nil {
		return sessionguard.SuppressedUnknown, nil
	}
	return m.guard.NudgeCoordination(ctx, id, message, m.steerActive)
}
