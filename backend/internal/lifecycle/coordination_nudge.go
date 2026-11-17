package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

type coordinationDeliveryReceiptStore interface {
	SemanticDeliveryAccepted(context.Context, domain.SessionID, string, string) (bool, error)
}

type semanticCoordinationMessenger interface {
	SendSemantic(context.Context, domain.SessionID, string, string) error
}

// ErrCoordinationDeliveryUnconfirmed means a prior TUI write may already have
// reached the pane, but no durable acceptance receipt exists yet. Retrying the
// write would duplicate the message, so callers should keep checking instead.
var ErrCoordinationDeliveryUnconfirmed = errors.New("coordination delivery remains unconfirmed")

var errCoordinationSemanticDeliveryUnsupported = errors.New("semantic coordination delivery is unsupported")

// NudgeCoordination sends an unsolicited coordination message through the
// lifecycle manager's shared, mode-aware guard. Suppressed outcomes are not
// deliveries; callers may retry them when new state arrives.
func (m *Manager) NudgeCoordination(ctx context.Context, id domain.SessionID, message, deliveryID string) (sessionguard.Outcome, error) {
	return m.nudgeCoordination(ctx, id, message, deliveryID, false)
}

// ConfirmCoordination rechecks a possibly accepted delivery. Chat safely
// retries the same idempotency key; TUI checks its receipt without another
// pane write.
func (m *Manager) ConfirmCoordination(ctx context.Context, id domain.SessionID, message, deliveryID string) (sessionguard.Outcome, error) {
	return m.nudgeCoordination(ctx, id, message, deliveryID, true)
}

func (m *Manager) nudgeCoordination(ctx context.Context, id domain.SessionID, message, deliveryID string, confirmOnly bool) (sessionguard.Outcome, error) {
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
			if confirmOnly {
				return sessionguard.Sent, ErrCoordinationDeliveryUnconfirmed
			}
		}
	}
	writer := func(ctx context.Context, rec domain.SessionRecord, message string) error {
		if deliveryID != "" && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
			if semantic, ok := m.messenger.(semanticCoordinationMessenger); ok {
				return semantic.SendSemantic(ctx, rec.ID, message, deliveryID)
			}
			return errCoordinationSemanticDeliveryUnsupported
		}
		if deliveryID != "" && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeTUI {
			if confirmOnly {
				return ErrCoordinationDeliveryUnconfirmed
			}
			message = domain.WrapSessionDelivery(deliveryID, message)
		}
		return m.messenger.Send(ctx, rec.ID, message)
	}
	return m.guard.NudgeCoordinationWithWriter(ctx, id, message, m.steerActive, writer)
}
