package decision

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SyncStore is the store method that stores entries received from a linked
// machine under the sync apply rules.
type SyncStore interface {
	ApplyDecisionEntries(ctx context.Context, peerHostID string, entries []domain.DecisionEntry, now time.Time) (domain.DecisionApplyResult, error)
}

// planHold is one plan-first owner whose gate a received page closes.
type planHold struct {
	owner    domain.SessionID
	callIDs  map[string]bool
	entryIDs map[string]bool
	next     domain.PlanFirstState
	previous domain.PlanFirstState
	held     bool
}

// ApplySyncedEntries stores a page received from a linked machine. A new
// answer or dismissal of a plan call this machine owns is applied the way a
// local one is: under the owner's plan gate, which delivery also holds, with
// the session's plan-first state closed before the entry becomes visible. So
// an approval still being delivered cannot reopen a gate that a synced
// "Do not proceed" closed. If the page ends up not changing that call's
// standing answer (a duplicate, a gap or a forked origin), the earlier state
// is restored.
func (s *Service) ApplySyncedEntries(ctx context.Context, peerHostID string, entries []domain.DecisionEntry, now time.Time) (domain.DecisionApplyResult, error) {
	store, ok := s.store.(SyncStore)
	if !ok {
		return domain.DecisionApplyResult{}, errors.New("decision store cannot apply synced entries")
	}
	holds, err := s.planHolds(ctx, entries)
	if err != nil {
		return domain.DecisionApplyResult{}, err
	}
	owners := make([]domain.SessionID, 0, len(holds))
	for owner := range holds {
		owners = append(owners, owner)
	}
	slices.Sort(owners) // a fixed order, so two pages never wait on each other
	unlocks := make([]func(), 0, len(owners))
	defer func() {
		for _, unlock := range unlocks {
			unlock()
		}
	}()
	for _, owner := range owners {
		unlock, err := s.mutationGate.acquire(ctx, planFirstMutationKey(owner))
		if err != nil {
			return domain.DecisionApplyResult{}, err
		}
		unlocks = append(unlocks, unlock)
	}
	for _, owner := range owners {
		if err := s.closePlanGate(ctx, holds[owner]); err != nil {
			s.restorePlanGates(ctx, holds)
			return domain.DecisionApplyResult{}, err
		}
	}
	result, err := store.ApplyDecisionEntries(ctx, peerHostID, entries, now)
	if err != nil {
		s.restorePlanGates(ctx, holds)
		return domain.DecisionApplyResult{}, err
	}
	for _, call := range result.Calls {
		for _, hold := range holds {
			if hold.callIDs[call.ID] && hold.entryIDs[call.StandingEntryID] {
				hold.held = false // the received answer now stands; keep the gate closed
			}
		}
	}
	s.restorePlanGates(ctx, holds)
	return result, nil
}

// planHolds finds, per plan-first owner on this machine, the state the newest
// new answer or dismissal in the page implies.
func (s *Service) planHolds(ctx context.Context, entries []domain.DecisionEntry) (map[domain.SessionID]*planHold, error) {
	ordered := slices.Clone(entries)
	slices.SortFunc(ordered, domain.CompareDecisionEntries)
	holds := map[domain.SessionID]*planHold{}
	for _, e := range ordered {
		if e.Kind != domain.DecisionKindAnswered && e.Kind != domain.DecisionKindDismissed {
			continue
		}
		call, found, err := s.store.GetDecisionCall(ctx, e.CallID)
		if err != nil {
			return nil, err
		}
		// Another machine's plan call names a session on that machine; a local
		// session with the same id is unrelated.
		if !found || call.Origin != s.hostID || !isPlanFirstCall(call) {
			continue
		}
		stored, err := s.store.ListDecisionEntries(ctx, e.CallID)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(stored, func(have domain.DecisionEntry) bool { return have.ID == e.ID }) {
			continue
		}
		next := domain.PlanFirstDeclined
		if e.Kind == domain.DecisionKindAnswered {
			var payload domain.DecisionAnsweredPayload
			if json.Unmarshal(e.Payload, &payload) != nil {
				continue
			}
			next = planFirstStateBeforeDelivery(payload)
		}
		owner := call.Raised.OwnerSessionID
		hold := holds[owner]
		if hold == nil {
			hold = &planHold{owner: owner, callIDs: map[string]bool{}, entryIDs: map[string]bool{}}
			holds[owner] = hold
		}
		hold.callIDs[call.ID] = true
		hold.entryIDs[e.ID] = true
		hold.next = next
	}
	return holds, nil
}

// closePlanGate records the owner's current state and closes its gate.
func (s *Service) closePlanGate(ctx context.Context, hold *planHold) error {
	owner, found, err := s.store.GetSession(ctx, hold.owner)
	if err != nil || !found || !owner.PlanFirstState.Enabled() {
		return err
	}
	hold.previous = owner.PlanFirstState
	if _, err := s.store.SetSessionPlanFirstState(ctx, hold.owner, hold.next); err != nil {
		return err
	}
	hold.held = true
	return nil
}

// restorePlanGates puts back the earlier state for every hold whose received
// answer did not end up standing.
func (s *Service) restorePlanGates(ctx context.Context, holds map[domain.SessionID]*planHold) {
	for _, hold := range holds {
		if hold.held {
			_, _ = s.store.SetSessionPlanFirstState(context.WithoutCancel(ctx), hold.owner, hold.previous)
			hold.held = false
		}
	}
}
