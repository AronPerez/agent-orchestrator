// Package decision owns the decision ledger's actor and state rules.
package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Store is the persistence boundary used by the decision service.
type Store interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	AppendDecisionEntry(context.Context, domain.DecisionEntry, time.Time) (domain.DecisionEntry, domain.DecisionCall, error)
	AppendDecisionEntries(context.Context, []domain.DecisionEntry, time.Time) ([]domain.DecisionEntry, []domain.DecisionCall, error)
	SetSessionPlanFirstState(context.Context, domain.SessionID, domain.PlanFirstState) (bool, error)
	GetDecisionCall(context.Context, string) (domain.DecisionCall, bool, error)
	ListDecisionEntries(context.Context, string) ([]domain.DecisionEntry, error)
	ListDecisionCallsForSession(context.Context, string, domain.SessionID, int) ([]domain.DecisionCall, error)
	ListDecisionCallsByState(context.Context, domain.DecisionCallState, int) ([]domain.DecisionCall, error)
	ListDecisionCallsForBoard(context.Context, time.Time) ([]domain.DecisionCall, error)
	ListDecisionStreams(context.Context) ([]domain.DecisionStream, error)
	ListDecisionStreamsFor(context.Context, []domain.SessionID) ([]domain.DecisionStream, error)
	SearchDecisionCalls(context.Context, string, domain.ProjectID, bool, int) ([]domain.DecisionCall, error)
}

// Deps supplies the decision service's persistence, identity, clock and hooks.
type Deps struct {
	Store        Store
	HostID       string
	MutationGate *MutationGate
	MachineLabel func() string
	Now          func() time.Time
	NewID        func(prefix string) string
	OnAppended   func(domain.DecisionEntry)
}

// Service applies actor, validation and work-stream rules to ledger operations.
type Service struct {
	store        Store
	hostID       string
	machineLabel func() string
	now          func() time.Time
	newID        func(string) string
	onAppended   func(domain.DecisionEntry)
	mutationGate *MutationGate
	fallbackMu   sync.RWMutex
	fallback     map[fallbackOutcomeGrant]struct{}
}

// MutationGate serializes standing-answer changes with delivery. The gate is
// shared by the service and deliverer, but scoped per call (or per plan-first
// owner) so one slow provider cannot block unrelated decisions.
type MutationGate struct {
	mu    sync.Mutex
	calls map[string]*callMutation
}

type callMutation struct {
	token chan struct{}
	refs  int
}

func (g *MutationGate) acquire(ctx context.Context, callID string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.calls == nil {
		g.calls = make(map[string]*callMutation)
	}
	lock := g.calls[callID]
	if lock == nil {
		lock = &callMutation{token: make(chan struct{}, 1)}
		lock.token <- struct{}{}
		g.calls[callID] = lock
	}
	lock.refs++
	g.mu.Unlock()

	select {
	case <-ctx.Done():
		g.drop(callID, lock)
		return nil, ctx.Err()
	case <-lock.token:
		if err := ctx.Err(); err != nil {
			lock.token <- struct{}{}
			g.drop(callID, lock)
			return nil, err
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			lock.token <- struct{}{}
			g.drop(callID, lock)
		})
	}, nil
}

func (g *MutationGate) drop(callID string, lock *callMutation) {
	g.mu.Lock()
	defer g.mu.Unlock()
	lock.refs--
	if lock.refs == 0 && g.calls[callID] == lock {
		delete(g.calls, callID)
	}
}

type fallbackOutcomeGrant struct {
	callID   string
	forEntry string
	session  domain.SessionID
}

// New constructs a decision service from its daemon-owned dependencies.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewID == nil {
		d.NewID = func(prefix string) string { return prefix + uuid.NewString() }
	}
	if d.MachineLabel == nil {
		d.MachineLabel = func() string {
			label, err := os.Hostname()
			if err != nil || strings.TrimSpace(label) == "" {
				return d.HostID
			}
			return label
		}
	}
	if d.MutationGate == nil {
		d.MutationGate = &MutationGate{}
	}
	return &Service{
		store: d.Store, hostID: d.HostID, machineLabel: d.MachineLabel,
		now: d.Now, newID: d.NewID, onAppended: d.OnAppended,
		mutationGate: d.MutationGate,
		fallback:     make(map[fallbackOutcomeGrant]struct{}),
	}
}

// Actor is either a calling session or the person using one of AO's surfaces.
type Actor struct {
	Session domain.SessionID
	Surface domain.RequestSurface
}

// RaiseInput contains the session-authored parts of a new decision call.
type RaiseInput struct {
	Title     string
	Context   string
	Options   []domain.DecisionOption
	Links     []domain.DecisionLink
	Reference string
	// Urgency is blocking or can_wait; empty means can_wait.
	Urgency string
	// Kind is one of domain.DecisionKinds; required in observe mode.
	Kind string
	// Prediction is what the session would decide on its own. Required in
	// observe mode for kinds the project has not reserved; refused for
	// reserved kinds.
	Prediction *domain.DecisionPrediction
}

// AnswerInput identifies an offered option or a person-authored alternative.
type AnswerInput struct {
	Choice string
	Option int
	Note   string
}

// OutcomeInput records the result of carrying out a decision.
type OutcomeInput struct {
	ForEntry string
	Text     string
	Link     string
}

// Scope selects a decision list view.
type Scope string

// ScopeMine and ScopeNeedsYou are the supported decision list views.
const (
	ScopeMine     Scope = "mine"
	ScopeNeedsYou Scope = "needs_you"
	// ScopeDecided lists decisions made for the person that nobody overruled.
	ScopeDecided Scope = "decided"
)

// DecisionBoardDoneWindow is how long a non-retired Done call stays on the
// board after its outcome. Older Done calls remain searchable history.
const DecisionBoardDoneWindow = 7 * 24 * time.Hour

// DefaultDecisionSearchLimit and MaxDecisionSearchLimit bound one search page.
const (
	DefaultDecisionSearchLimit = 20
	MaxDecisionSearchLimit     = 100
)

// Board is the bounded live snapshot behind the Decisions page and its
// Needs-you count.
type Board struct {
	Calls         []domain.DecisionCall
	NeedsYouCount int
	ServingHostID string
	DoneSince     time.Time
}

// Detail combines a call read model with its immutable history.
type Detail struct {
	Call    domain.DecisionCall
	Entries []domain.DecisionEntry
}

// Raise validates and appends a new call owned by the acting session.
func (s *Service) Raise(ctx context.Context, actor Actor, input RaiseInput) (domain.DecisionCall, error) {
	session, err := s.actingSession(ctx, actor)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	origin, err := s.callOrigin(ctx, session)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	autonomy := origin.autonomy

	input = normalizeRaiseInput(input)
	payload := domain.DecisionRaisedPayload{
		Title: input.Title, Context: input.Context, Options: input.Options, Links: input.Links,
		Reference: input.Reference, ProjectID: session.ProjectID, ProjectName: origin.projectName,
		StreamSessionID: origin.streamSessionID, StreamName: origin.streamName,
		MachineLabel: origin.machineLabel, OwnerSessionID: session.ID, OwnerSessionName: sessionName(session),
		Urgency: input.Urgency, Kind: domain.DecisionKindID(input.Kind), Prediction: input.Prediction,
	}
	if payload.Urgency == "" {
		payload.Urgency = domain.DecisionUrgencyCanWait
	}
	if err := domain.ValidateDecisionRaise(payload); err != nil {
		return domain.DecisionCall{}, invalidDecision(err)
	}
	payload.AutonomyMode = autonomy.EffectiveMode()
	payload.Reserved = payload.Kind != "" && autonomy.IsReserved(payload.Kind)
	if err := checkRaisePolicy(payload); err != nil {
		return domain.DecisionCall{}, err
	}
	var pendingPlanCall domain.DecisionCall
	var hasPendingPlanCall bool
	if isPlanFirstCall(domain.DecisionCall{Raised: payload}) {
		unlock, err := s.mutationGate.acquire(ctx, planFirstMutationKey(session.ID))
		if err != nil {
			return domain.DecisionCall{}, err
		}
		defer unlock()
		if err := s.holdPlanFirstOwnerGate(ctx, session.ID, domain.PlanFirstPending); err != nil {
			return domain.DecisionCall{}, err
		}
		pendingPlanCall, hasPendingPlanCall, err = s.pendingPlanFirstCall(ctx, session.ID)
		if err != nil {
			return domain.DecisionCall{}, err
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	now := s.now().UTC()
	callID := s.newID("dec_")
	entry := domain.DecisionEntry{
		ID: s.newID("dent_"), Origin: s.hostID, CallID: callID,
		Kind: domain.DecisionKindRaised, Payload: raw, WrittenAt: now,
		AuthorKind: domain.DecisionAuthorSession, AuthorSession: session.ID,
	}
	if hasPendingPlanCall {
		withdrawn, err := json.Marshal(struct{}{})
		if err != nil {
			return domain.DecisionCall{}, err
		}
		withdrawal := domain.DecisionEntry{
			ID: s.newID("dent_"), Origin: s.hostID, CallID: pendingPlanCall.ID,
			Kind: domain.DecisionKindWithdrawn, Payload: withdrawn, WrittenAt: now,
			AuthorKind: domain.DecisionAuthorSession, AuthorSession: session.ID,
		}
		stored, calls, err := s.store.AppendDecisionEntries(ctx, []domain.DecisionEntry{entry, withdrawal}, now)
		if err != nil {
			return domain.DecisionCall{}, err
		}
		if s.onAppended != nil {
			for _, appended := range stored {
				s.onAppended(appended)
			}
		}
		return calls[0], nil
	}
	stored, call, err := s.store.AppendDecisionEntry(ctx, entry, now)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	if s.onAppended != nil {
		s.onAppended(stored)
	}
	return call, nil
}

// actingSession returns the calling session for an operation only a session
// may perform on this machine's ledger.
func (s *Service) actingSession(ctx context.Context, actor Actor) (domain.SessionRecord, error) {
	if actor.Session == "" {
		return domain.SessionRecord{}, apierr.Invalid("DECISION_SESSION_REQUIRED", "This needs a session. Run it from inside a session.", nil)
	}
	if s.hostID == "" {
		return domain.SessionRecord{}, apierr.Unavailable("DECISION_HOST_UNKNOWN", "This machine has no identity yet, so it cannot record decisions.")
	}
	session, ok, err := s.store.GetSession(ctx, actor.Session)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	if !ok {
		return domain.SessionRecord{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return session, nil
}

// origin is what Orchestrator, never the session, records about where a new
// call came from, plus the project's autonomy policy at that moment.
type origin struct {
	projectName     string
	streamSessionID domain.SessionID
	streamName      string
	machineLabel    string
	autonomy        domain.AutonomyConfig
}

func (s *Service) callOrigin(ctx context.Context, session domain.SessionRecord) (origin, error) {
	result := origin{machineLabel: s.machineLabel()}
	if session.ProjectID == "" {
		return result, nil
	}
	project, found, err := s.store.GetProject(ctx, string(session.ProjectID))
	if err != nil {
		return origin{}, err
	}
	result.projectName = string(session.ProjectID)
	if found {
		if project.DisplayName != "" {
			result.projectName = project.DisplayName
		}
		result.autonomy = project.Config.Autonomy
	}
	sessions, err := s.store.ListSessions(ctx, session.ProjectID)
	if err != nil {
		return origin{}, err
	}
	if stream, found := domain.DeriveDecisionStream(session.ID, sessions); found {
		result.streamSessionID = stream.ID
		result.streamName = sessionName(stream)
	}
	return result, nil
}

// checkRaisePolicy applies the project's autonomy policy, already snapshotted
// into the payload, to a call that passed the shape rules.
//
// A call without a kind is accepted in every mode. Standing instructions are
// built when a session starts, so a session that started before the project
// switched to observe mode has never been told to pass one; refusing its
// calls would block it on a policy it cannot see. Sessions that know the
// policy always pass a kind, and the observe readout skips calls without one.
func checkRaisePolicy(p domain.DecisionRaisedPayload) error {
	observe := p.AutonomyMode == domain.AutonomyObserve
	if p.Reserved && p.Prediction != nil {
		kind, _ := domain.LookupDecisionKind(string(p.Kind))
		return policyRule("prediction_on_reserved", fmt.Sprintf("%q is reserved for the person in this project. Drop --would-decide.", kind.Label))
	}
	if observe && p.Kind != "" && !p.Reserved && p.Prediction == nil {
		return policyRule("prediction_required", "This project is in observe mode. Say what you would have decided with --would-decide, --why and --undo.")
	}
	return nil
}

func policyRule(rule, message string) error {
	return invalidDecision(&domain.DecisionRuleError{Rule: rule, Message: message})
}

// KindPolicy is one decision kind and whether a project reserves it.
type KindPolicy struct {
	ID                domain.DecisionKindID
	Label             string
	Reserved          bool
	ReservedByDefault bool
}

// Policy is a project's autonomy mode and its view of every decision kind.
type Policy struct {
	ProjectID domain.ProjectID
	Mode      domain.AutonomyMode
	Kinds     []KindPolicy
}

// Kinds returns the autonomy policy for the calling session's project, or
// for the named project when the person asks.
func (s *Service) Kinds(ctx context.Context, actor Actor, projectID domain.ProjectID) (Policy, error) {
	projectID, autonomy, err := s.projectAutonomy(ctx, actor, projectID)
	if err != nil {
		return Policy{}, err
	}
	kinds := domain.DecisionKinds()
	policy := Policy{ProjectID: projectID, Mode: autonomy.EffectiveMode(), Kinds: make([]KindPolicy, len(kinds))}
	for i, kind := range kinds {
		policy.Kinds[i] = KindPolicy{ID: kind.ID, Label: kind.Label, Reserved: autonomy.IsReserved(kind.ID), ReservedByDefault: kind.ReservedByDefault}
	}
	return policy, nil
}

// projectAutonomy resolves which project a policy read is about: a calling
// session always gets its own project; the person must name one.
func (s *Service) projectAutonomy(ctx context.Context, actor Actor, projectID domain.ProjectID) (domain.ProjectID, domain.AutonomyConfig, error) {
	if actor.Session != "" {
		var err error
		if projectID, _, err = s.actorProject(ctx, actor); err != nil {
			return "", domain.AutonomyConfig{}, err
		}
	} else if projectID == "" {
		return "", domain.AutonomyConfig{}, apierr.Invalid("DECISION_PROJECT_REQUIRED", "Name the project with --project.", nil)
	}
	if projectID == "" {
		return "", domain.AutonomyConfig{}, nil
	}
	project, found, err := s.store.GetProject(ctx, string(projectID))
	if err != nil {
		return "", domain.AutonomyConfig{}, err
	}
	if !found {
		return "", domain.AutonomyConfig{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	return projectID, project.Config.Autonomy, nil
}

// Get returns one call and its ordered ledger history.
func (s *Service) Get(ctx context.Context, callID string) (Detail, error) {
	call, ok, err := s.store.GetDecisionCall(ctx, callID)
	if err != nil {
		return Detail{}, err
	}
	if !ok {
		return Detail{}, apierr.NotFound("DECISION_NOT_FOUND", "Unknown call")
	}
	entries, err := s.store.ListDecisionEntries(ctx, callID)
	if err != nil {
		return Detail{}, err
	}
	calls, err := s.withStreams(ctx, []domain.DecisionCall{call})
	if err != nil {
		return Detail{}, err
	}
	return Detail{Call: calls[0], Entries: entries}, nil
}

// Answer records the person's standing answer to an open call.
func (s *Service) Answer(ctx context.Context, actor Actor, callID string, input AnswerInput) (domain.DecisionCall, error) {
	call, unlock, err := s.lockedCall(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	defer unlock()
	if actor.Session != "" {
		return domain.DecisionCall{}, personOnly()
	}
	if call.Decided != nil {
		return domain.DecisionCall{}, apierr.Conflict("DECISION_NOT_A_QUESTION", "This is a decision made for you, not a question. Overrule it instead.", nil)
	}
	if err := s.rejectSupersededPlanCall(ctx, call); err != nil {
		return domain.DecisionCall{}, err
	}
	if call.State != domain.DecisionStateNeedsYou && call.State != domain.DecisionStateInProgress && call.State != domain.DecisionStateDismissed {
		return domain.DecisionCall{}, wrongState(call, `This call is closed. Its state is "%s".`)
	}
	payload := domain.DecisionAnsweredPayload{Choice: input.Choice, Option: input.Option, Note: strings.TrimSpace(input.Note)}
	if err := domain.ValidateDecisionAnswer(len(call.Raised.Options), payload); err != nil {
		return domain.DecisionCall{}, invalidDecision(err)
	}
	if err := s.holdPlanFirstGate(ctx, call, planFirstStateBeforeDelivery(payload)); err != nil {
		return domain.DecisionCall{}, err
	}
	return s.append(ctx, actor, call.ID, domain.DecisionKindAnswered, payload)
}

// Dismiss records that the person declined to choose from a pending call.
func (s *Service) Dismiss(ctx context.Context, actor Actor, callID, note string) (domain.DecisionCall, error) {
	call, unlock, err := s.lockedCall(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	defer unlock()
	if actor.Session != "" {
		return domain.DecisionCall{}, personOnly()
	}
	if err := s.rejectSupersededPlanCall(ctx, call); err != nil {
		return domain.DecisionCall{}, err
	}
	if call.State != domain.DecisionStateNeedsYou {
		return domain.DecisionCall{}, wrongState(call, `Only a call in "Needs you" can be dismissed. This one is "%s".`)
	}
	note = strings.TrimSpace(note)
	if err := domain.ValidateDecisionNote(note); err != nil {
		return domain.DecisionCall{}, invalidDecision(err)
	}
	if err := s.holdPlanFirstGate(ctx, call, domain.PlanFirstDeclined); err != nil {
		return domain.DecisionCall{}, err
	}
	return s.append(ctx, actor, call.ID, domain.DecisionKindDismissed, domain.DecisionDismissedPayload{Note: note})
}

// holdPlanFirstGate closes a previously approved gate before the changed
// ledger entry is appended. If the append then fails, the session stays safely
// blocked until the person retries instead of implementing a superseded plan.
func (s *Service) holdPlanFirstGate(ctx context.Context, call domain.DecisionCall, next domain.PlanFirstState) error {
	if !isPlanFirstCall(call) {
		return nil
	}
	return s.holdPlanFirstOwnerGate(ctx, call.Raised.OwnerSessionID, next)
}

func (s *Service) holdPlanFirstOwnerGate(ctx context.Context, ownerID domain.SessionID, next domain.PlanFirstState) error {
	owner, ok, err := s.store.GetSession(ctx, ownerID)
	if err != nil {
		return err
	}
	if !ok || !owner.PlanFirstState.Enabled() {
		return nil
	}
	updated, err := s.store.SetSessionPlanFirstState(ctx, owner.ID, next)
	if err != nil {
		return err
	}
	if !updated {
		return errors.New("plan-first owner session no longer exists")
	}
	return nil
}

func planFirstStateBeforeDelivery(answer domain.DecisionAnsweredPayload) domain.PlanFirstState {
	if answer.Choice == domain.DecisionChoiceOption && answer.Option == 3 {
		return domain.PlanFirstDeclined
	}
	return domain.PlanFirstPending
}

func (s *Service) rejectSupersededPlanCall(ctx context.Context, call domain.DecisionCall) error {
	if !isPlanFirstCall(call) {
		return nil
	}
	current, found, err := newestPlanFirstCall(ctx, s.store, call.Origin, call.Raised.OwnerSessionID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("plan-first owner has no current call")
	}
	if current.ID != call.ID {
		return apierr.Conflict(
			"DECISION_SUPERSEDED",
			"A newer plan decision replaced this call. Answer the latest plan decision instead.",
			map[string]any{"currentCallId": current.ID},
		)
	}
	return nil
}

func (s *Service) pendingPlanFirstCall(ctx context.Context, ownerID domain.SessionID) (domain.DecisionCall, bool, error) {
	current, found, err := newestPlanFirstCall(ctx, s.store, s.hostID, ownerID)
	if err != nil || !found || current.State != domain.DecisionStateNeedsYou {
		return domain.DecisionCall{}, false, err
	}
	return current, true, nil
}

// RecordOutcome closes an in-progress call with the result of carrying it out.
func (s *Service) RecordOutcome(ctx context.Context, actor Actor, callID string, input OutcomeInput) (domain.DecisionCall, error) {
	call, unlock, err := s.lockedCall(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	defer unlock()
	input.ForEntry = strings.TrimSpace(input.ForEntry)
	if call.State == domain.DecisionStateDecided && call.Decided != nil {
		return s.closeStandingDecision(ctx, actor, call, input)
	}
	if actor.Session != "" && !s.mayClose(call, input.ForEntry, actor.Session) {
		return domain.DecisionCall{}, notOwner()
	}
	if call.State != domain.DecisionStateInProgress {
		return domain.DecisionCall{}, wrongState(call, `An outcome can be recorded once a call is "In progress". This one is "%s".`)
	}
	if input.ForEntry != "" && input.ForEntry != call.StandingEntryID {
		return domain.DecisionCall{}, answerChanged(call)
	}
	if actor.Session != "" && input.ForEntry == "" {
		return domain.DecisionCall{}, answerChanged(call)
	}
	if input.ForEntry == "" {
		input.ForEntry = call.StandingEntryID
	}
	payload := domain.DecisionOutcomePayload{ForEntry: input.ForEntry, Text: strings.TrimSpace(input.Text), Link: strings.TrimSpace(input.Link)}
	if err := domain.ValidateDecisionOutcome(payload); err != nil {
		return domain.DecisionCall{}, invalidDecision(err)
	}
	completed, err := s.append(ctx, actor, call.ID, domain.DecisionKindOutcome, payload)
	if err == nil && actor.Session != "" {
		s.RevokeFallbackOutcome(call.ID, payload.ForEntry, actor.Session)
	}
	return completed, err
}

// Withdraw closes a pending call at the owning session's request.
func (s *Service) Withdraw(ctx context.Context, actor Actor, callID string) (domain.DecisionCall, error) {
	call, unlock, err := s.lockedCall(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	defer unlock()
	if !s.owns(call, actor.Session) {
		return domain.DecisionCall{}, notOwner()
	}
	if call.State != domain.DecisionStateNeedsYou {
		return domain.DecisionCall{}, wrongState(call, `Only a call in "Needs you" can be withdrawn. This one is "%s".`)
	}
	return s.append(ctx, actor, call.ID, domain.DecisionKindWithdrawn, struct{}{})
}

// Retire changes whether a completed call still applies.
func (s *Service) Retire(ctx context.Context, actor Actor, callID string, retired bool) (domain.DecisionCall, error) {
	call, unlock, err := s.lockedCall(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	defer unlock()
	if actor.Session != "" {
		return domain.DecisionCall{}, personOnly()
	}
	if call.State != domain.DecisionStateDone {
		return domain.DecisionCall{}, wrongState(call, `Only a finished call can be marked "no longer applies". This one is "%s".`)
	}
	return s.append(ctx, actor, call.ID, domain.DecisionKindRetired, domain.DecisionRetiredPayload{Retired: retired})
}

// List returns calls in the requested actor-relative scope.
func (s *Service) List(ctx context.Context, actor Actor, scope Scope) ([]domain.DecisionCall, error) {
	var calls []domain.DecisionCall
	var err error
	switch scope {
	case ScopeMine:
		if actor.Session == "" {
			return nil, apierr.Invalid("DECISION_SESSION_REQUIRED", "This needs a session. Run it from inside a session.", nil)
		}
		calls, err = s.store.ListDecisionCallsForSession(ctx, s.hostID, actor.Session, -1)
	case ScopeNeedsYou:
		calls, err = s.store.ListDecisionCallsByState(ctx, domain.DecisionStateNeedsYou, -1)
	case ScopeDecided:
		calls, err = s.store.ListDecisionCallsByState(ctx, domain.DecisionStateDecided, -1)
	default:
		return nil, apierr.Invalid("DECISION_SCOPE_INVALID", "Unknown scope.", nil)
	}
	if err != nil {
		return nil, err
	}
	return s.withStreams(ctx, calls)
}

// Board returns every active call plus non-retired Done calls from the last
// seven days, labelled only from what each call recorded.
func (s *Service) Board(ctx context.Context) (Board, error) {
	since := s.now().UTC().Add(-DecisionBoardDoneWindow)
	calls, err := s.store.ListDecisionCallsForBoard(ctx, since)
	if err != nil {
		return Board{}, err
	}
	calls, err = s.withOwnStreams(ctx, calls)
	if err != nil {
		return Board{}, err
	}
	if calls == nil {
		calls = []domain.DecisionCall{}
	}
	board := Board{Calls: calls, ServingHostID: s.hostID, DoneSince: since}
	for _, call := range calls {
		if call.State == domain.DecisionStateNeedsYou {
			board.NeedsYouCount++
		}
	}
	return board, nil
}

// Search returns up to limit text matches across every state and age, with
// the actor's project ranked first. A non-positive limit uses the default.
func (s *Service) Search(ctx context.Context, actor Actor, query string, limit int) ([]domain.DecisionCall, error) {
	if strings.TrimSpace(query) == "" {
		return nil, apierr.Invalid("DECISION_QUERY_REQUIRED", "Say what to search for.", nil)
	}
	project, hasProject, err := s.actorProject(ctx, actor)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = DefaultDecisionSearchLimit
	}
	limit = min(limit, MaxDecisionSearchLimit)
	calls, err := s.store.SearchDecisionCalls(ctx, query, project, hasProject, limit)
	if err != nil {
		return nil, err
	}
	calls, err = s.withStreams(ctx, calls)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(calls, func(i, j int) bool {
		iSame := hasProject && calls[i].Raised.ProjectID == project
		jSame := hasProject && calls[j].Raised.ProjectID == project
		if iSame != jSame {
			return iSame
		}
		if calls[i].Retired != calls[j].Retired {
			return !calls[i].Retired
		}
		return false
	})
	return calls, nil
}

func (s *Service) withStreams(ctx context.Context, calls []domain.DecisionCall) ([]domain.DecisionCall, error) {
	streams, err := s.store.ListDecisionStreams(ctx)
	if err != nil {
		return nil, err
	}
	return domain.AssignDecisionStreams(calls, streams), nil
}

// withOwnStreams is withStreams for a bounded list: it looks up only the
// streams these calls can belong to (their recorded stream, or the owner for a
// manager's early call), never the whole history.
func (s *Service) withOwnStreams(ctx context.Context, calls []domain.DecisionCall) ([]domain.DecisionCall, error) {
	if len(calls) == 0 {
		return calls, nil
	}
	seen := make(map[domain.SessionID]bool, len(calls))
	sessions := make([]domain.SessionID, 0, len(calls))
	for _, call := range calls {
		session := call.Raised.StreamSessionID
		if session == "" {
			session = call.Raised.OwnerSessionID
		}
		if session != "" && !seen[session] {
			seen[session] = true
			sessions = append(sessions, session)
		}
	}
	streams, err := s.store.ListDecisionStreamsFor(ctx, sessions)
	if err != nil {
		return nil, err
	}
	return domain.AssignDecisionStreams(calls, streams), nil
}

func (s *Service) actorProject(ctx context.Context, actor Actor) (domain.ProjectID, bool, error) {
	if actor.Session == "" {
		return "", false, nil
	}
	session, ok, err := s.store.GetSession(ctx, actor.Session)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return session.ProjectID, true, nil
}

func (s *Service) call(ctx context.Context, callID string) (domain.DecisionCall, error) {
	call, ok, err := s.store.GetDecisionCall(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	if !ok {
		return domain.DecisionCall{}, apierr.NotFound("DECISION_NOT_FOUND", "Unknown call")
	}
	return call, nil
}

// lockedCall first reads under the ordinary call gate. Plan calls then switch
// to their owner's shared gate and reload, so replacement calls cannot race an
// earlier delivery. Ordinary calls keep one read and their independent gate.
func (s *Service) lockedCall(ctx context.Context, callID string) (domain.DecisionCall, func(), error) {
	unlock, err := s.mutationGate.acquire(ctx, "call:"+callID)
	if err != nil {
		return domain.DecisionCall{}, nil, err
	}
	initial, err := s.call(ctx, callID)
	if err != nil {
		unlock()
		return domain.DecisionCall{}, nil, err
	}
	if !isPlanFirstCall(initial) {
		return initial, unlock, nil
	}
	unlock()
	unlock, err = s.mutationGate.acquire(ctx, decisionMutationKey(initial))
	if err != nil {
		return domain.DecisionCall{}, nil, err
	}
	current, err := s.call(ctx, callID)
	if err != nil {
		unlock()
		return domain.DecisionCall{}, nil, err
	}
	return current, unlock, nil
}

func decisionMutationKey(call domain.DecisionCall) string {
	if isPlanFirstCall(call) {
		return planFirstMutationKey(call.Raised.OwnerSessionID)
	}
	return "call:" + call.ID
}

func planFirstMutationKey(owner domain.SessionID) string {
	return "plan:" + string(owner)
}

type planFirstCallStore interface {
	ListDecisionCallsForSession(context.Context, string, domain.SessionID, int) ([]domain.DecisionCall, error)
}

// newestPlanFirstCall returns the owner's plan protocol call with the newest
// raised-entry ledger stamp. The store supplies calls in that order.
func newestPlanFirstCall(ctx context.Context, store planFirstCallStore, origin string, owner domain.SessionID) (domain.DecisionCall, bool, error) {
	calls, err := store.ListDecisionCallsForSession(ctx, origin, owner, -1)
	if err != nil {
		return domain.DecisionCall{}, false, err
	}
	for _, call := range calls {
		if call.Raised.OwnerSessionID == owner && isPlanFirstCall(call) {
			return call, true, nil
		}
	}
	return domain.DecisionCall{}, false, nil
}

func (s *Service) append(ctx context.Context, actor Actor, callID string, kind domain.DecisionEntryKind, payload any) (domain.DecisionCall, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	now := s.now().UTC()
	entry := domain.DecisionEntry{
		ID: s.newID("dent_"), Origin: s.hostID, CallID: callID,
		Kind: kind, Payload: raw, WrittenAt: now,
	}
	if actor.Session != "" {
		entry.AuthorKind = domain.DecisionAuthorSession
		entry.AuthorSession = actor.Session
	} else {
		entry.AuthorKind = domain.DecisionAuthorPerson
		entry.AuthorSurface = actor.Surface
	}
	stored, call, err := s.store.AppendDecisionEntry(ctx, entry, now)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	if s.onAppended != nil {
		s.onAppended(stored)
	}
	return call, nil
}

func (s *Service) owns(call domain.DecisionCall, session domain.SessionID) bool {
	return session != "" && call.Origin == s.hostID && call.Raised.OwnerSessionID == session
}

func (s *Service) mayClose(call domain.DecisionCall, forEntry string, session domain.SessionID) bool {
	return call.Origin == s.hostID && (call.Raised.OwnerSessionID == session ||
		(forEntry == call.StandingEntryID && call.DeliveredTo == session) ||
		s.fallbackOutcomeAllowed(call.ID, forEntry, session))
}

// AuthorizeFallbackOutcome grants the selected fallback session temporary
// authority while delivery acceptance and the durable delivery entry are in flight.
// It reports whether this attempt created the grant, so a failed retry cannot
// revoke authority retained from an earlier accepted send.
func (s *Service) AuthorizeFallbackOutcome(callID, forEntry string, session domain.SessionID) bool {
	s.fallbackMu.Lock()
	defer s.fallbackMu.Unlock()
	key := fallbackOutcomeGrant{callID: callID, forEntry: forEntry, session: session}
	if _, exists := s.fallback[key]; exists {
		return false
	}
	s.fallback[key] = struct{}{}
	return true
}

// RevokeFallbackOutcome removes one answer-scoped temporary fallback grant.
func (s *Service) RevokeFallbackOutcome(callID, forEntry string, session domain.SessionID) {
	s.fallbackMu.Lock()
	defer s.fallbackMu.Unlock()
	delete(s.fallback, fallbackOutcomeGrant{callID: callID, forEntry: forEntry, session: session})
}

func (s *Service) fallbackOutcomeAllowed(callID, forEntry string, session domain.SessionID) bool {
	s.fallbackMu.RLock()
	defer s.fallbackMu.RUnlock()
	_, ok := s.fallback[fallbackOutcomeGrant{callID: callID, forEntry: forEntry, session: session}]
	return session != "" && forEntry != "" && ok
}

func personOnly() error {
	return apierr.Forbidden("DECISION_PERSON_ONLY", "Only the person can answer, dismiss or retire a call. A session cannot.")
}

func notOwner() error {
	return apierr.Forbidden("DECISION_NOT_OWNER", "Only the session that raised this call can do that.")
}

func wrongState(call domain.DecisionCall, message string) error {
	return apierr.Conflict("DECISION_WRONG_STATE", fmt.Sprintf(message, call.State.Label()), map[string]any{"state": call.State})
}

func answerChanged(call domain.DecisionCall) error {
	return apierr.Conflict(
		"DECISION_ANSWER_CHANGED",
		"The decision changed before this outcome was recorded. Review the latest answer and carry that out.",
		map[string]any{"answerEntryId": call.StandingEntryID},
	)
}

func sessionName(session domain.SessionRecord) string {
	if session.DisplayName != "" {
		return session.DisplayName
	}
	return string(session.ID)
}

func normalizeRaiseInput(input RaiseInput) RaiseInput {
	input.Title = strings.TrimSpace(input.Title)
	input.Context = strings.TrimSpace(input.Context)
	input.Reference = strings.TrimSpace(input.Reference)
	options := make([]domain.DecisionOption, len(input.Options))
	for i, option := range input.Options {
		option.Label = strings.TrimSpace(option.Label)
		option.Consequence = strings.TrimSpace(option.Consequence)
		options[i] = option
	}
	input.Options = options
	links := make([]domain.DecisionLink, len(input.Links))
	for i, link := range input.Links {
		link.Label = strings.TrimSpace(link.Label)
		link.URL = strings.TrimSpace(link.URL)
		links[i] = link
	}
	input.Links = links
	input.Urgency = strings.TrimSpace(input.Urgency)
	input.Kind = strings.TrimSpace(input.Kind)
	if input.Prediction != nil {
		prediction := *input.Prediction
		prediction.Why = strings.TrimSpace(prediction.Why)
		prediction.Undo = strings.TrimSpace(prediction.Undo)
		input.Prediction = &prediction
	}
	return input
}

func invalidDecision(err error) error {
	var rule *domain.DecisionRuleError
	if errors.As(err, &rule) {
		return apierr.Invalid("DECISION_INVALID", rule.Message, map[string]any{"rule": rule.Rule})
	}
	return err
}
