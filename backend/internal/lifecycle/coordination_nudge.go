package lifecycle

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

type coordinationDeliveryReceiptStore interface {
	SemanticDeliveryAccepted(context.Context, domain.SessionID, string, string) (bool, error)
}

// NudgeCoordination sends an unsolicited coordination message through the
// lifecycle manager's shared, mode-aware guard. Suppressed outcomes are not
// deliveries; callers may retry them when new state arrives.
func (m *Manager) NudgeCoordination(ctx context.Context, id domain.SessionID, message, deliveryID string) (sessionguard.Outcome, error) {
	if m.guard == nil {
		return sessionguard.SuppressedUnknown, nil
	}
	if deliveryID != "" {
		rec, found, err := m.store.GetSession(ctx, id)
		if err != nil {
			return sessionguard.SuppressedUnknown, fmt.Errorf("read coordination recipient: %w", err)
		}
		if found && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeTUI {
			if receipts, ok := m.store.(coordinationDeliveryReceiptStore); ok && rec.Metadata.RuntimeLaunchID != "" {
				accepted, err := receipts.SemanticDeliveryAccepted(ctx, id, rec.Metadata.RuntimeLaunchID, deliveryID)
				if err != nil {
					return sessionguard.SuppressedUnknown, fmt.Errorf("read coordination receipt: %w", err)
				}
				if accepted {
					return sessionguard.Sent, nil
				}
			}
			message = domain.WrapSessionDelivery(deliveryID, message)
		}
	}
	return m.guard.NudgeCoordination(ctx, id, message, m.steerActive)
}
