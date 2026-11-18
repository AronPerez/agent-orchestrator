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
	SetSessionPlanFirstState(context.Context, domain.SessionID, domain.PlanFirstState) (bool, error)
	GetDecisionCall(context.Context, string) (domain.DecisionCall, bool, error)
	ListDecisionEntries(context.Context, string) ([]domain.DecisionEntry, error)
	ListDecisionCallsForSession(context.Context, string, domain.SessionID, int) ([]domain.DecisionCall, error)
	ListDecisionCallsByState(context.Context, domain.DecisionCallState, int) ([]domain.DecisionCall, error)
	ListDecisionStreams(context.Context) ([]domain.DecisionStream, error)
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
// shared by the service and deliverer so an answer cannot become authoritative
// halfway through dispatching the entry it replaces.
type MutationGate struct{ mu sync.Mutex }

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
)

// Detail combines a call read model with its immutable history.
type Detail struct {
	Call    domain.DecisionCall
	Entries []domain.DecisionEntry
}

// Raise validates and appends a new call owned by the acting session.
func (s *Service) Raise(ctx context.Context, actor Actor, input RaiseInput) (domain.DecisionCall, error) {
	if actor.Session == "" {
		return domain.DecisionCall{}, apierr.Invalid("DECISION_SESSION_REQUIRED", "This needs a session. Run it from inside a session.", nil)
	}
	if s.hostID == "" {
		return domain.DecisionCall{}, apierr.Unavailable("DECISION_HOST_UNKNOWN", "This machine has no identity yet, so it cannot record decisions.")
	}
	session, ok, err := s.store.GetSession(ctx, actor.Session)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	if !ok {
		return domain.DecisionCall{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}

	input = normalizeRaiseInput(input)
	payload := domain.DecisionRaisedPayload{
		Title: input.Title, Context: input.Context, Options: input.Options, Links: input.Links,
		Reference: input.Reference, ProjectID: session.ProjectID,
		MachineLabel: s.machineLabel(), OwnerSessionID: session.ID, OwnerSessionName: sessionName(session),
	}
	if session.ProjectID != "" {
		project, found, err := s.store.GetProject(ctx, string(session.ProjectID))
		if err != nil {
			return domain.DecisionCall{}, err
		}
		payload.ProjectName = string(session.ProjectID)
		if found && project.DisplayName != "" {
			payload.ProjectName = project.DisplayName
		}
		sessions, err := s.store.ListSessions(ctx, session.ProjectID)
		if err != nil {
			return domain.DecisionCall{}, err
		}
		if stream, found := domain.DeriveDecisionStream(session.ID, sessions); found {
			payload.StreamSessionID = stream.ID
			payload.StreamName = sessionName(stream)
		}
	}
	if err := domain.ValidateDecisionRaise(payload); err != nil {
		return domain.DecisionCall{}, invalidDecision(err)
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
	stored, call, err := s.store.AppendDecisionEntry(ctx, entry, now)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	if s.onAppended != nil {
		s.onAppended(stored)
	}
	return call, nil
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
	s.mutationGate.mu.Lock()
	defer s.mutationGate.mu.Unlock()
	call, err := s.call(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	if actor.Session != "" {
		return domain.DecisionCall{}, personOnly()
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
	s.mutationGate.mu.Lock()
	defer s.mutationGate.mu.Unlock()
	call, err := s.call(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	if actor.Session != "" {
		return domain.DecisionCall{}, personOnly()
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
	owner, ok, err := s.store.GetSession(ctx, call.Raised.OwnerSessionID)
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

// RecordOutcome closes an in-progress call with the result of carrying it out.
func (s *Service) RecordOutcome(ctx context.Context, actor Actor, callID string, input OutcomeInput) (domain.DecisionCall, error) {
	s.mutationGate.mu.Lock()
	defer s.mutationGate.mu.Unlock()
	call, err := s.call(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	input.ForEntry = strings.TrimSpace(input.ForEntry)
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
	s.mutationGate.mu.Lock()
	defer s.mutationGate.mu.Unlock()
	call, err := s.call(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
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
	s.mutationGate.mu.Lock()
	defer s.mutationGate.mu.Unlock()
	call, err := s.call(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
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
	default:
		return nil, apierr.Invalid("DECISION_SCOPE_INVALID", "Unknown scope.", nil)
	}
	if err != nil {
		return nil, err
	}
	return s.withStreams(ctx, calls)
}

// Search returns text matches with the actor's project ranked first.
func (s *Service) Search(ctx context.Context, actor Actor, query string) ([]domain.DecisionCall, error) {
	if strings.TrimSpace(query) == "" {
		return nil, apierr.Invalid("DECISION_QUERY_REQUIRED", "Say what to search for.", nil)
	}
	project, hasProject, err := s.actorProject(ctx, actor)
	if err != nil {
		return nil, err
	}
	calls, err := s.store.SearchDecisionCalls(ctx, query, project, hasProject, 20)
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
	return input
}

func invalidDecision(err error) error {
	var rule *domain.DecisionRuleError
	if errors.As(err, &rule) {
		return apierr.Invalid("DECISION_INVALID", rule.Message, map[string]any{"rule": rule.Rule})
	}
	return err
}
