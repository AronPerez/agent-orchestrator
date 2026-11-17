// Package sessioncontract supervises workers that were spawned with a durable
// goal and timebox. It owns policy only; session writes, guarded pane delivery,
// reports, and notifications stay behind their existing boundaries.
package sessioncontract

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// DefaultSweepInterval is the production cadence for contract evaluation.
const DefaultSweepInterval = time.Minute

const (
	stallNudgeMessage = "You stopped without reporting. Continue working, or report done, stuck, or needs input."
	timeboxMessage    = "Your timebox has ended. Wrap up the current safe step and report done, stuck, or needs input now."
)

// Store supplies the durable facts and narrowly fenced writes used by the
// coordinator.
type Store interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListAllSessions(context.Context) ([]domain.SessionRecord, error)
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error)
	ListPRComments(context.Context, string) ([]domain.PullRequestComment, error)
	RecordSessionContractStallNudge(context.Context, domain.SessionID, time.Time) (bool, error)
	RecordSessionContractStallEscalation(context.Context, domain.SessionID, time.Time, time.Time) (bool, error)
	ExpireSessionContract(context.Context, domain.SessionID, time.Time, time.Time) (bool, error)
}

// Nudger performs the existing just-in-time sessionguard check before writing
// to a worker. sent is false when the live surface is unsafe.
type Nudger interface {
	NudgeSession(context.Context, domain.SessionID, string) (sent bool, err error)
	NudgeIdleSession(context.Context, domain.SessionID, string) (sent bool, err error)
}

// StallReporter delivers a daemon-observed stall through the existing worker
// report channel to the project's orchestrator.
type StallReporter interface {
	ReportSessionContractStall(context.Context, domain.SessionID, string) error
}

// Notifier is the user-facing side of a stall escalation.
type Notifier interface {
	Notify(context.Context, ports.NotificationIntent) error
}

// StallReporterFunc adapts a report-service closure at daemon wiring time.
type StallReporterFunc func(context.Context, domain.SessionID, string) error

// ReportSessionContractStall calls f with the observed stalled worker.
func (f StallReporterFunc) ReportSessionContractStall(ctx context.Context, id domain.SessionID, message string) error {
	return f(ctx, id, message)
}

// Action names the one state transition an evaluation performed.
type Action string

// Action values identify the single durable transition an evaluation made.
const (
	ActionNone      Action = ""
	ActionNudged    Action = "nudged"
	ActionEscalated Action = "escalated"
	ActionLimitHit  Action = "limit_hit"
)

// Result explains an evaluation without making callers inspect store writes.
type Result struct {
	Action Action
	Reason string
}

// Config customizes deterministic time, cadence, and logging.
type Config struct {
	Clock         func() time.Time
	SweepInterval time.Duration
	// StartAfter holds the first sweep until startup reconciliation has made
	// persisted activity and runtime facts safe to act on. Nil starts at once.
	StartAfter <-chan struct{}
	Logger     *slog.Logger
}

// Coordinator evaluates contracted workers and owns the periodic sweep.
type Coordinator struct {
	store         Store
	nudger        Nudger
	reporter      StallReporter
	notifier      Notifier
	clock         func() time.Time
	sweepInterval time.Duration
	startAfter    <-chan struct{}
	logger        *slog.Logger
}

// New constructs a contract coordinator.
func New(store Store, nudger Nudger, reporter StallReporter, notifier Notifier, cfg Config) *Coordinator {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = DefaultSweepInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Coordinator{
		store: store, nudger: nudger, reporter: reporter, notifier: notifier,
		clock: cfg.Clock, sweepInterval: cfg.SweepInterval, startAfter: cfg.StartAfter, logger: cfg.Logger,
	}
}

// EvaluateSession applies deadline and stall policy to one worker.
func (c *Coordinator) EvaluateSession(ctx context.Context, id domain.SessionID) (Result, error) {
	if c == nil || c.store == nil {
		return Result{}, fmt.Errorf("session contract: store is required")
	}
	rec, ok, err := c.store.GetSession(ctx, id)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{Reason: "session_not_found"}, nil
	}
	if reason := contractGate(rec); reason != "" {
		return Result{Reason: reason}, nil
	}

	now := c.clock().UTC()
	deadline := rec.TimeboxDeadlineAt.UTC()
	if !now.Before(deadline) {
		return c.expire(ctx, rec, deadline, now)
	}
	if rec.Activity.State != domain.ActivityIdle {
		return Result{Reason: "not_idle"}, nil
	}
	if rec.Activity.LastActivityAt.IsZero() {
		return Result{Reason: "no_activity"}, nil
	}
	project, ok, err := c.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{Reason: "project_not_found"}, nil
	}
	stallWindow := time.Duration(project.Config.WithDefaults().WorkerContracts.StallMinutes) * time.Minute
	if now.Sub(rec.Activity.LastActivityAt) < stallWindow {
		return Result{Reason: "stall_threshold_not_met"}, nil
	}
	prBlocker, err := c.prStallBlocker(ctx, rec.ID)
	if err != nil {
		return Result{}, err
	}
	if prBlocker != "" {
		return Result{Reason: prBlocker}, nil
	}

	if rec.ContractStallNudgedAt == nil || rec.Activity.LastActivityAt.After(*rec.ContractStallNudgedAt) {
		return c.nudge(ctx, rec, now)
	}
	if rec.ContractStallEscalatedAt == nil && !now.Before(rec.ContractStallNudgedAt.Add(stallWindow)) {
		return c.escalate(ctx, rec, *rec.ContractStallNudgedAt, now)
	}
	return Result{Reason: "nudge_already_sent"}, nil
}

func contractGate(rec domain.SessionRecord) string {
	switch {
	case rec.Kind != domain.KindWorker:
		return "not_worker"
	case rec.IsTerminated || rec.Activity.State == domain.ActivityExited:
		return "terminated"
	case rec.Goal == "" || rec.TimeboxDeadlineAt == nil:
		return "no_contract"
	case rec.ContractOutcome != domain.SessionContractOutcomeNone:
		return "contract_finished"
	default:
		return ""
	}
}

func (c *Coordinator) nudge(ctx context.Context, rec domain.SessionRecord, now time.Time) (Result, error) {
	if c.nudger == nil {
		return Result{Reason: "nudge_unavailable"}, nil
	}
	sent, sendErr := c.nudger.NudgeIdleSession(ctx, rec.ID, stallNudgeMessage)
	if !sent {
		if sendErr != nil {
			return Result{}, sendErr
		}
		return Result{Reason: "nudge_suppressed"}, nil
	}
	recorded, err := c.store.RecordSessionContractStallNudge(ctx, rec.ID, now)
	if err != nil {
		return Result{}, err
	}
	if !recorded {
		return Result{Reason: "contract_finished"}, sendErr
	}
	return Result{Action: ActionNudged}, sendErr
}

func (c *Coordinator) expire(ctx context.Context, rec domain.SessionRecord, deadline, now time.Time) (Result, error) {
	var sendErr error
	if c.nudger != nil {
		_, sendErr = c.nudger.NudgeSession(ctx, rec.ID, timeboxMessage)
	}
	expired, err := c.store.ExpireSessionContract(ctx, rec.ID, deadline, now)
	if err != nil {
		return Result{}, err
	}
	if !expired {
		return Result{Reason: "contract_finished"}, sendErr
	}
	return Result{Action: ActionLimitHit}, sendErr
}

func (c *Coordinator) escalate(ctx context.Context, rec domain.SessionRecord, nudgedAt, now time.Time) (Result, error) {
	message := fmt.Sprintf("Worker %s stopped without reporting and remained idle after one automatic nudge.", rec.ID)
	if c.notifier != nil {
		if err := c.notifier.Notify(ctx, ports.NotificationIntent{
			Type: domain.NotificationSessionStalled, SessionID: rec.ID, ProjectID: rec.ProjectID,
			CreatedAt: now, SourceKey: fmt.Sprintf("session-contract-stall:%s:%d", rec.ID, nudgedAt.UnixNano()),
			SessionDisplayName: rec.DisplayName,
		}); err != nil {
			return Result{}, err
		}
	}
	if c.reporter != nil {
		if err := c.reporter.ReportSessionContractStall(ctx, rec.ID, message); err != nil {
			return Result{}, err
		}
	}
	recorded, err := c.store.RecordSessionContractStallEscalation(ctx, rec.ID, nudgedAt, now)
	if err != nil {
		return Result{}, err
	}
	if !recorded {
		return Result{Reason: "escalation_already_recorded"}, nil
	}
	return Result{Action: ActionEscalated}, nil
}

func (c *Coordinator) prStallBlocker(ctx context.Context, id domain.SessionID) (string, error) {
	prs, err := c.store.ListPRsBySession(ctx, id)
	if err != nil {
		return "", err
	}
	if len(prs) == 0 {
		return "", nil
	}
	waiting := false
	needsWorker := false
	for _, pr := range prs {
		if pr.Merged || pr.Closed {
			continue
		}
		comments, err := c.store.ListPRComments(ctx, pr.URL)
		if err != nil {
			return "", err
		}
		unresolved := false
		for _, comment := range comments {
			if domain.IsActionableReviewComment(comment.Resolved, comment.IsBot, comment.File, comment.Line) {
				unresolved = true
				break
			}
		}
		if domain.MergeReadinessOf(pr, unresolved).ReadyToMerge() {
			continue
		}
		actionable := pr.Draft || pr.CI == domain.CIFailing || pr.Review == domain.ReviewChangesRequest ||
			unresolved || pr.Mergeability == domain.MergeConflicting
		switch {
		case actionable:
			needsWorker = true
		case pr.CI == domain.CIPending || pr.Review == domain.ReviewRequired:
			waiting = true
		default:
			needsWorker = true
		}
	}
	if needsWorker {
		return "", nil
	}
	if waiting {
		return "waiting_on_ci_or_review", nil
	}
	return "work_ready", nil
}

// Sweep evaluates every known session and isolates per-session failures.
func (c *Coordinator) Sweep(ctx context.Context) error {
	sessions, err := c.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	for _, rec := range sessions {
		if _, err := c.EvaluateSession(ctx, rec.ID); err != nil {
			c.logger.Error("session contract: evaluate session failed", "session_id", rec.ID, "err", err)
		}
	}
	return nil
}

// Start waits for the optional startup fence, then evaluates persisted
// contracts immediately and on every sweep interval.
func (c *Coordinator) Start(ctx context.Context) <-chan struct{} {
	if c.startAfter != nil {
		done := make(chan struct{})
		go func() {
			defer close(done)
			select {
			case <-ctx.Done():
				return
			case <-c.startAfter:
			}
			<-observe.StartPollLoop(ctx, c.sweepInterval, c.Sweep, c.logger, "session contract")
		}()
		return done
	}
	return observe.StartPollLoop(ctx, c.sweepInterval, c.Sweep, c.logger, "session contract")
}
