package decision

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrSessionEnded marks a session that can no longer take a message.
var ErrSessionEnded = errors.New("decision: session can no longer take a message")

// Messenger delivers a decision as the person's own turn.
type Messenger interface {
	// DeliverAsPerson returns an error wrapping ErrSessionEnded when the session
	// can no longer take a message.
	DeliverAsPerson(context.Context, domain.SessionID, string, time.Time) error
}

// FallbackOutcomeAuthorizer lets a selected fallback session record an outcome
// while its accepted delivery is still waiting to become durable in the ledger.
type FallbackOutcomeAuthorizer interface {
	AuthorizeFallbackOutcome(string, string, domain.SessionID) bool
	RevokeFallbackOutcome(string, string, domain.SessionID)
}

// DeliveryStore is the durable decision-delivery boundary.
type DeliveryStore interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	SetSessionPlanFirstState(context.Context, domain.SessionID, domain.PlanFirstState) (bool, error)
	AppendDecisionEntry(context.Context, domain.DecisionEntry, time.Time) (domain.DecisionEntry, domain.DecisionCall, error)
	ListDecisionCallsAwaitingDelivery(context.Context, string) ([]domain.DecisionCall, error)
}

// DelivererDeps configures decision answer delivery.
type DelivererDeps struct {
	Store      DeliveryStore
	Messenger  Messenger
	HostID     string
	Now        func() time.Time
	NewID      func(prefix string) string
	RetryDelay time.Duration
	Log        *slog.Logger
	// FallbackAuthorizer is shared with the decision service in daemon wiring.
	FallbackAuthorizer FallbackOutcomeAuthorizer
}

// Deliverer sends pending standing answers and records their delivery state.
type Deliverer struct {
	store      DeliveryStore
	messenger  Messenger
	hostID     string
	now        func() time.Time
	newID      func(string) string
	retryDelay time.Duration
	log        *slog.Logger
	fallback   FallbackOutcomeAuthorizer
	wake       chan struct{}
	mu         sync.Mutex
}

// NewDeliverer constructs an idle decision answer deliverer.
func NewDeliverer(d DelivererDeps) *Deliverer {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewID == nil {
		d.NewID = func(prefix string) string { return prefix + uuid.NewString() }
	}
	if d.RetryDelay <= 0 {
		d.RetryDelay = 30 * time.Second
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Deliverer{
		store: d.Store, messenger: d.Messenger, hostID: d.HostID,
		now: d.Now, newID: d.NewID, retryDelay: d.RetryDelay, log: d.Log,
		fallback: d.FallbackAuthorizer, wake: make(chan struct{}, 1),
	}
}

// Wake asks the deliverer to inspect durable pending calls.
func (d *Deliverer) Wake() {
	if d == nil {
		return
	}
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Start runs delivery until ctx is cancelled and returns its completion signal.
func (d *Deliverer) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.run(ctx)
	}()
	return done
}

func (d *Deliverer) run(ctx context.Context) {
	for {
		pending, err := d.RunDue(ctx)
		if err != nil {
			d.log.Warn("decision delivery pass failed", "err", err)
			pending = 1
		}
		if pending == 0 {
			select {
			case <-ctx.Done():
				return
			case <-d.wake:
			}
			continue
		}
		timer := time.NewTimer(d.retryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-d.wake:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

// RunDue tries every pending delivery once and returns how many are still pending.
func (d *Deliverer) RunDue(ctx context.Context) (int, error) {
	if d == nil || d.store == nil || d.messenger == nil {
		return 0, errors.New("decision deliverer dependencies are required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	calls, err := d.store.ListDecisionCallsAwaitingDelivery(ctx, d.hostID)
	if err != nil {
		return 0, err
	}
	pending := 0
	blocked := make(map[string]bool)
	sentAnswers := make(map[string]bool)
	for _, call := range calls {
		if blocked[call.ID] {
			pending++
			continue
		}
		if call.StandingKind == domain.DecisionKindAnswered && sentAnswers[call.ID] {
			call.Redelivery = true
		}
		retry, sent := d.deliver(ctx, call)
		if sent && call.StandingKind == domain.DecisionKindAnswered {
			sentAnswers[call.ID] = true
		}
		if retry {
			pending++
			blocked[call.ID] = true
		}
	}
	return pending, nil
}

// deliver reports whether the entry needs another attempt and whether its
// message reached a session during this attempt.
func (d *Deliverer) deliver(ctx context.Context, call domain.DecisionCall) (bool, bool) {
	owner, found, err := d.store.GetSession(ctx, call.Raised.OwnerSessionID)
	if err != nil {
		d.logFailure(call.ID, "load owner", err)
		return true, false
	}
	ended := !found || owner.IsTerminated
	if !ended {
		err = d.messenger.DeliverAsPerson(ctx, owner.ID, DeliveryMessage(call, false), d.now().UTC())
		switch {
		case err == nil:
			if !d.applyPlanFirstAnswer(ctx, owner, call) {
				return true, true
			}
			return !d.record(ctx, call, domain.DecisionDeliveredPayload{
				ForEntry: call.StandingEntryID, Status: domain.DecisionDeliveredSent,
				SessionID: owner.ID, SessionName: sessionName(owner),
			}), true
		case errors.Is(err, ErrSessionEnded):
			// A stopped plan-first worker is resumable. Keep the answer pending
			// instead of delivering it elsewhere (or recording no_session), so
			// the worker receives the approval after it resumes.
			if owner.PlanFirstState.BlocksAutomation() && isPlanFirstCall(call) {
				return true, false
			}
			ended = true
		default:
			d.logFailure(call.ID, "send to owner", err)
			return true, false
		}
	}
	if !ended {
		return true, false
	}
	return d.deliverFallback(ctx, call)
}

func (d *Deliverer) applyPlanFirstAnswer(ctx context.Context, owner domain.SessionRecord, call domain.DecisionCall) bool {
	if !owner.PlanFirstState.Enabled() || !isPlanFirstCall(call) {
		return true
	}
	next := domain.PlanFirstDeclined
	if call.StandingKind == domain.DecisionKindAnswered && call.Choice == domain.DecisionChoiceOption &&
		(call.Option == 1 || (call.Option == 2 && strings.TrimSpace(call.Note) != "")) {
		next = domain.PlanFirstApproved
	} else if call.StandingKind == domain.DecisionKindAnswered && call.Choice == domain.DecisionChoiceOption && call.Option == 2 {
		next = domain.PlanFirstPending
	}
	updated, err := d.store.SetSessionPlanFirstState(ctx, owner.ID, next)
	if err != nil {
		d.logFailure(call.ID, "update plan-first gate", err)
		return false
	}
	if !updated {
		d.logFailure(call.ID, "update plan-first gate", errors.New("owner session no longer exists"))
		return false
	}
	return true
}

func isPlanFirstCall(call domain.DecisionCall) bool {
	if call.Raised.Reference != domain.PlanFirstDecisionReference || call.Raised.Title != "Approve this plan?" || len(call.Raised.Options) != 3 {
		return false
	}
	return call.Raised.Options[0].Label == "Approve" &&
		call.Raised.Options[1].Label == "Approve with changes" &&
		call.Raised.Options[2].Label == "Do not proceed"
}

func (d *Deliverer) deliverFallback(ctx context.Context, call domain.DecisionCall) (bool, bool) {
	var orchestrator domain.SessionRecord
	found := false
	if call.Raised.ProjectID != "" {
		sessions, err := d.store.ListSessions(ctx, call.Raised.ProjectID)
		if err != nil {
			d.logFailure(call.ID, "list fallback sessions", err)
			return true, false
		}
		orchestrator, found = domain.ActiveOrchestrator(sessions)
		if found && orchestrator.ID == call.Raised.OwnerSessionID {
			found = false
		}
	}
	if !found {
		return !d.record(ctx, call, domain.DecisionDeliveredPayload{
			ForEntry: call.StandingEntryID, Status: domain.DecisionDeliveredNoSession,
		}), false
	}
	newGrant := false
	if d.fallback != nil {
		newGrant = d.fallback.AuthorizeFallbackOutcome(call.ID, call.StandingEntryID, orchestrator.ID)
	}
	revoke := func() {
		if d.fallback != nil {
			d.fallback.RevokeFallbackOutcome(call.ID, call.StandingEntryID, orchestrator.ID)
		}
	}
	revokeAttempt := func() {
		if newGrant {
			revoke()
		}
	}
	err := d.messenger.DeliverAsPerson(ctx, orchestrator.ID, DeliveryMessage(call, true), d.now().UTC())
	switch {
	case err == nil:
		recorded := d.record(ctx, call, domain.DecisionDeliveredPayload{
			ForEntry: call.StandingEntryID, Status: domain.DecisionDeliveredSent,
			SessionID: orchestrator.ID, SessionName: sessionName(orchestrator), Fallback: true,
		})
		if recorded {
			revoke()
		}
		return !recorded, true
	case errors.Is(err, ports.ErrAgentDeliveryUnconfirmed):
		// The message reached the agent surface, so the fallback may already be
		// acting on it even though AO missed the acceptance signal. Retain its
		// answer-scoped authority while the delivery remains pending for retry.
		d.logFailure(call.ID, "confirm fallback orchestrator delivery", err)
		return true, false
	case errors.Is(err, ErrSessionEnded):
		revokeAttempt()
		return !d.record(ctx, call, domain.DecisionDeliveredPayload{
			ForEntry: call.StandingEntryID, Status: domain.DecisionDeliveredNoSession,
		}), false
	default:
		revokeAttempt()
		d.logFailure(call.ID, "send to fallback orchestrator", err)
		return true, false
	}
}

func (d *Deliverer) record(ctx context.Context, call domain.DecisionCall, payload domain.DecisionDeliveredPayload) bool {
	raw, err := json.Marshal(payload)
	if err != nil {
		d.logFailure(call.ID, "encode delivery", err)
		return false
	}
	now := d.now().UTC()
	_, _, err = d.store.AppendDecisionEntry(ctx, domain.DecisionEntry{
		ID: d.newID("dent_"), Origin: d.hostID, CallID: call.ID,
		Kind: domain.DecisionKindDelivered, Payload: raw, WrittenAt: now,
		AuthorKind: domain.DecisionAuthorSystem,
	}, now)
	if err != nil {
		d.logFailure(call.ID, "record delivery", err)
		return false
	}
	return true
}

func (d *Deliverer) logFailure(callID, operation string, err error) {
	d.log.Warn("decision delivery remains pending", "call", callID, "operation", operation, "err", err)
}
