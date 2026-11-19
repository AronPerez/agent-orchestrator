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
