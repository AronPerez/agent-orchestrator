package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	decisionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/decision"
)

// startDecisionNotifications runs the decision notification projector for the
// daemon's lifetime. Its first pass repairs any call that entered Needs you
// before a crash without a notification; it never blocks startup.
func startDecisionNotifications(
	ctx context.Context,
	ledger decisionsvc.NotificationLedger,
	writer decisionsvc.NotificationWriter,
	log *slog.Logger,
) (*decisionsvc.NotificationProjector, <-chan struct{}) {
	projector := decisionsvc.NewNotificationProjector(decisionsvc.NotificationProjectorDeps{
		Store: ledger, Writer: writer, Log: log,
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		projector.Run(ctx)
	}()
	return projector, done
}

// decisionAppendHook fans one committed ledger append out to the background
// consumers. Each wake is non-blocking, so the person's or session's request
// never waits on answer delivery or notification I/O.
func decisionAppendHook(wakes ...func()) func(domain.DecisionEntry) {
	return func(domain.DecisionEntry) {
		for _, wake := range wakes {
			wake()
		}
	}
}
