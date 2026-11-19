package decision

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/notify"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// NotificationLedger is the decision read model the notification projector scans.
type NotificationLedger interface {
	ListDecisionCallsByState(context.Context, domain.DecisionCallState, int) ([]domain.DecisionCall, error)
}

// NotificationWriter persists and publishes derived decision notifications.
// notify.Manager implements it: Notify is idempotent per call source key, and
// Reconcile resolves every open notification whose call left Needs you.
type NotificationWriter interface {
	Notify(context.Context, ports.NotificationIntent) error
	Reconcile(context.Context) error
}

// NotificationProjectorDeps configures a NotificationProjector.
type NotificationProjectorDeps struct {
	Store      NotificationLedger
	Writer     NotificationWriter
	RetryDelay time.Duration
	Log        *slog.Logger
}

// NotificationProjector derives decision_needed notifications from the ledger.
// It never runs inside a ledger write: an append only wakes it, and each pass
// rescans the whole Needs you set, so a crash or a failed publication between
// the ledger commit and the notification is repaired by the next pass.
type NotificationProjector struct {
	store      NotificationLedger
	writer     NotificationWriter
	retryDelay time.Duration
	log        *slog.Logger
	wake       chan struct{}
}

// NewNotificationProjector constructs an idle projector; call Run to start it.
func NewNotificationProjector(d NotificationProjectorDeps) *NotificationProjector {
	if d.RetryDelay <= 0 {
		d.RetryDelay = 30 * time.Second
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &NotificationProjector{
		store: d.Store, writer: d.Writer, retryDelay: d.RetryDelay, log: d.Log,
		wake: make(chan struct{}, 1),
	}
}

// Wake asks for another pass. It never blocks: bursts of appends coalesce into
// one pending pass, so a slow notification store cannot stall a ledger write.
func (p *NotificationProjector) Wake() {
	if p == nil {
		return
	}
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Run passes once immediately (startup repair), then after every wake, and
// retries a failed pass after RetryDelay until ctx is cancelled.
func (p *NotificationProjector) Run(ctx context.Context) {
	for {
		var (
			retry <-chan time.Time
			timer *time.Timer
		)
		if err := p.Reconcile(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			// Errors name ids only; call text never reaches logs.
			p.log.Warn("decision notification pass failed", "err", err)
			timer = time.NewTimer(p.retryDelay)
			retry = timer.C
		}
		select {
		case <-ctx.Done():
		case <-p.wake:
		case <-retry:
		}
		if timer != nil {
			timer.Stop()
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// Reconcile ensures every call in Needs you has its notification, then
// resolves notifications whose call has left Needs you. Creating before
// resolving means a call answered mid-pass is closed in the same pass. One
// call's failure does not stop the others; the first error is returned so the
// run loop retries.
func (p *NotificationProjector) Reconcile(ctx context.Context) error {
	var errs []error
	calls, err := p.store.ListDecisionCallsByState(ctx, domain.DecisionStateNeedsYou, -1)
	if err != nil {
		errs = append(errs, err)
	}
	for _, call := range calls {
		err := p.writer.Notify(ctx, decisionNotificationIntent(call))
		if errors.Is(err, domain.ErrInvalidNotificationRecord) {
			// A record that cannot be announced (no title, no origin) will not
			// become valid on retry; the call still shows on the Decisions page.
			p.log.Warn("decision call has no announceable notification", "call", call.ID)
			continue
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if err := p.writer.Reconcile(ctx); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// decisionNotificationIntent carries only the call's identity and recorded
// labels. Context, options, and notes are deliberately left out.
func decisionNotificationIntent(call domain.DecisionCall) ports.NotificationIntent {
	return ports.NotificationIntent{
		Type:                 domain.NotificationDecisionNeeded,
		ProjectID:            call.Raised.ProjectID,
		SourceKey:            notify.DecisionSourceKey(call.ID),
		DecisionCallID:       call.ID,
		DecisionOriginHostID: call.Origin,
		DecisionProjectName:  call.Raised.ProjectName,
		DecisionStreamName:   call.Raised.StreamName,
		DecisionTitle:        call.Raised.Title,
	}
}
