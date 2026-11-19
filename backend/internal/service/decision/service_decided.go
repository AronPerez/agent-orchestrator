package decision

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// DecideInput is the session-authored part of a decision made on the
// person's behalf.
type DecideInput struct {
	Kind      string
	What      string
	Why       string
	Undo      string
	Verify    string
	Links     []domain.DecisionLink
	Reference string
}

// Decide records a decision the calling session made on the person's
// behalf. The project must be in mode on and the kind unreserved for the
// decision to be within policy. A record outside policy is still kept, with
// the reason, because a session started under an older policy may already
// have acted on it and the person must be able to see and overrule it.
func (s *Service) Decide(ctx context.Context, actor Actor, input DecideInput) (domain.DecisionCall, error) {
	session, err := s.actingSession(ctx, actor)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	origin, err := s.callOrigin(ctx, session)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	links := make([]domain.DecisionLink, len(input.Links))
	for i, link := range input.Links {
		links[i] = domain.DecisionLink{Label: strings.TrimSpace(link.Label), URL: strings.TrimSpace(link.URL)}
	}
	payload := domain.DecisionDecidedPayload{
		What: strings.TrimSpace(input.What), Why: strings.TrimSpace(input.Why),
		Undo: strings.TrimSpace(input.Undo), Verify: strings.TrimSpace(input.Verify),
		Kind: domain.DecisionKindID(strings.TrimSpace(input.Kind)), Links: links, Reference: strings.TrimSpace(input.Reference),
		ProjectID: session.ProjectID, ProjectName: origin.projectName,
		StreamSessionID: origin.streamSessionID, StreamName: origin.streamName,
		MachineLabel: origin.machineLabel, OwnerSessionID: session.ID, OwnerSessionName: sessionName(session),
		AutonomyMode: origin.autonomy.EffectiveMode(),
	}
	if err := domain.ValidateDecisionDecided(payload); err != nil {
		return domain.DecisionCall{}, invalidDecision(err)
	}
	// The session has usually acted already, so a policy mismatch never
	// loses the record: it is kept, flagged, and shown to the person.
	switch {
	case payload.AutonomyMode != domain.AutonomyOn:
		payload.OutsidePolicy = domain.DecisionOutsidePolicyNotOn
	case origin.autonomy.IsReserved(payload.Kind):
		payload.OutsidePolicy = domain.DecisionOutsidePolicyReserved
	}
	// A session that retries after losing the response must not leave two
	// records of one action, so an identical open record is returned. The
	// current policy still applies to the reply: if it no longer allows the
	// decision, the reply carries the flag so the session is told not to
	// act. The stored record is left as written.
	unlock, err := s.mutationGate.acquire(ctx, "decide:"+string(session.ID))
	if err != nil {
		return domain.DecisionCall{}, err
	}
	defer unlock()
	if existing, found, err := s.openIdenticalDecision(ctx, session.ID, payload); err != nil || found {
		if found && existing.Decided.OutsidePolicy == "" && payload.OutsidePolicy != "" {
			decided := *existing.Decided
			decided.OutsidePolicy = payload.OutsidePolicy
			existing.Decided = &decided
		}
		return existing, err
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	now := s.now().UTC()
	entry := domain.DecisionEntry{
		ID: s.newID("dent_"), Origin: s.hostID, CallID: s.newID("dec_"),
		Kind: domain.DecisionKindDecided, Payload: raw, WrittenAt: now,
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

// Overrule records the person reversing a decision made on their behalf.
// The overrule is delivered to the owning session like an answer, and the
// session undoes the decision and records the outcome.
func (s *Service) Overrule(ctx context.Context, actor Actor, callID, note string) (domain.DecisionCall, error) {
	call, unlock, err := s.lockedCall(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	defer unlock()
	if actor.Session != "" {
		return domain.DecisionCall{}, personOnly()
	}
	if call.Decided == nil {
		return domain.DecisionCall{}, apierr.Conflict("DECISION_NOT_DECIDED", "Only a decision made for you can be overruled. Answer or dismiss a call instead.", nil)
	}
	if call.State != domain.DecisionStateDecided {
		return domain.DecisionCall{}, wrongState(call, `Only a decision that stands can be overruled. This one is "%s".`)
	}
	note = strings.TrimSpace(note)
	if err := domain.ValidateDecisionOverrule(note); err != nil {
		return domain.DecisionCall{}, invalidDecision(err)
	}
	return s.append(ctx, actor, call.ID, domain.DecisionKindOverruled, domain.DecisionOverruledPayload{Note: note})
}

// closeStandingDecision records an outcome on a decision nobody overruled.
// Sessions record a decision before acting on it, so this is how the owning
// session says it did not, in the end, carry it out; the person may close it
// too. The outcome names the decided entry, since there is no answer.
func (s *Service) closeStandingDecision(ctx context.Context, actor Actor, call domain.DecisionCall, input OutcomeInput) (domain.DecisionCall, error) {
	if actor.Session != "" && !s.owns(call, actor.Session) {
		return domain.DecisionCall{}, notOwner()
	}
	// Once confirmed as carried out, the action happened: only the person
	// may close the record, so a session cannot hide it from review.
	if actor.Session != "" && call.Decided.CarriedOut {
		return domain.DecisionCall{}, apierr.Conflict("DECISION_CARRIED_OUT",
			"This decision was confirmed as carried out, so it stays for the person to review or overrule. Do not close it.", nil)
	}
	if input.ForEntry != "" && input.ForEntry != call.Decided.EntryID {
		return domain.DecisionCall{}, answerChanged(call)
	}
	payload := domain.DecisionOutcomePayload{ForEntry: call.Decided.EntryID, Text: strings.TrimSpace(input.Text), Link: strings.TrimSpace(input.Link)}
	if err := domain.ValidateDecisionOutcome(payload); err != nil {
		return domain.DecisionCall{}, invalidDecision(err)
	}
	return s.append(ctx, actor, call.ID, domain.DecisionKindOutcome, payload)
}

// CarriedOut records the owning session confirming it did what it decided.
// Sessions record a decision before acting, so until this is recorded the
// decision is an intent that may not have happened. Confirming again is a
// no-op. It is accepted while the decision stands or after an overrule, so
// an undo always knows whether there is anything to undo.
func (s *Service) CarriedOut(ctx context.Context, actor Actor, callID string) (domain.DecisionCall, error) {
	call, unlock, err := s.lockedCall(ctx, callID)
	if err != nil {
		return domain.DecisionCall{}, err
	}
	defer unlock()
	if call.Decided == nil {
		return domain.DecisionCall{}, apierr.Conflict("DECISION_NOT_DECIDED", "Only a decision made for the person can be confirmed as carried out.", nil)
	}
	if !s.owns(call, actor.Session) {
		return domain.DecisionCall{}, notOwner()
	}
	if call.State != domain.DecisionStateDecided && call.State != domain.DecisionStateInProgress {
		return domain.DecisionCall{}, wrongState(call, `This decision is closed. Its state is "%s".`)
	}
	if call.Decided.CarriedOut {
		return call, nil
	}
	return s.append(ctx, actor, call.ID, domain.DecisionKindCarriedOut, domain.DecisionCarriedOutPayload{ForEntry: call.Decided.EntryID})
}

func (s *Service) openIdenticalDecision(ctx context.Context, session domain.SessionID, payload domain.DecisionDecidedPayload) (domain.DecisionCall, bool, error) {
	calls, err := s.store.ListDecisionCallsForSession(ctx, s.hostID, session, -1)
	if err != nil {
		return domain.DecisionCall{}, false, err
	}
	for _, call := range calls {
		if call.Decided != nil && call.State == domain.DecisionStateDecided &&
			call.Raised.Title == payload.What && call.Raised.Kind == payload.Kind && call.Decided.Undo == payload.Undo {
			return call, true, nil
		}
	}
	return domain.DecisionCall{}, false, nil
}
