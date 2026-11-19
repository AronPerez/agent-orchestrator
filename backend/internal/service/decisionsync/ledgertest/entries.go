package ledgertest

import (
	"encoding/json"
	"strconv"
	"sync/atomic"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var entrySeq atomic.Int64

// NewEntryID returns a process-unique entry id.
func NewEntryID() string {
	return "dent_t" + strconv.FormatInt(entrySeq.Add(1), 10)
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

// Raised is a local raised entry for callID owned by session owner.
func Raised(callID string, owner domain.SessionID, machine string) domain.DecisionEntry {
	return domain.DecisionEntry{
		ID: NewEntryID(), CallID: callID, Kind: domain.DecisionKindRaised,
		Payload: mustJSON(domain.DecisionRaisedPayload{
			Title:   "Which auth scheme should the proxy use?",
			Context: "The connector calls the proxy for each tenant.",
			Options: []domain.DecisionOption{
				{Label: "OAuth client credentials", Consequence: "One app registration per tenant.", Recommended: true},
				{Label: "Per-tenant API keys", Consequence: "Customers rotate keys by hand."},
			},
			ProjectID: "shop", MachineLabel: machine, OwnerSessionID: owner, OwnerSessionName: string(owner),
		}),
		AuthorKind: domain.DecisionAuthorSession, AuthorSession: owner,
	}
}

// PlanRaised is a local raised plan call for owner, which a later plan call
// from the same owner replaces.
func PlanRaised(callID string, owner domain.SessionID, machine string) domain.DecisionEntry {
	e := Raised(callID, owner, machine)
	var p domain.DecisionRaisedPayload
	_ = json.Unmarshal(e.Payload, &p)
	p.Reference = domain.PlanFirstDecisionReference
	e.Payload = mustJSON(p)
	return e
}

// Decided is a local decided entry for callID owned by owner.
func Decided(callID string, owner domain.SessionID, machine string) domain.DecisionEntry {
	return domain.DecisionEntry{
		ID: NewEntryID(), CallID: callID, Kind: domain.DecisionKindDecided,
		Payload: mustJSON(domain.DecisionDecidedPayload{
			What: "Use the queue", Why: "It already exists", Undo: "Revert the commit", Kind: "design",
			ProjectID: "shop", MachineLabel: machine, OwnerSessionID: owner, OwnerSessionName: string(owner),
		}),
		AuthorKind: domain.DecisionAuthorSession, AuthorSession: owner,
	}
}

// Answer is the person picking option (1-based) on callID.
func Answer(callID string, option int, note string) domain.DecisionEntry {
	return person(callID, domain.DecisionKindAnswered, domain.DecisionAnsweredPayload{Choice: domain.DecisionChoiceOption, Option: option, Note: note})
}

// Dismiss is the person dismissing callID.
func Dismiss(callID string) domain.DecisionEntry {
	return person(callID, domain.DecisionKindDismissed, domain.DecisionDismissedPayload{Note: "not now"})
}

// Overrule is the person overruling decided callID.
func Overrule(callID string) domain.DecisionEntry {
	return person(callID, domain.DecisionKindOverruled, domain.DecisionOverruledPayload{Note: "no"})
}

// Retire hides or shows callID.
func Retire(callID string, retired bool) domain.DecisionEntry {
	return person(callID, domain.DecisionKindRetired, domain.DecisionRetiredPayload{Retired: retired})
}

// Outcome records what happened on callID, written by its owning session.
func Outcome(callID string, owner domain.SessionID) domain.DecisionEntry {
	return session(callID, owner, domain.DecisionKindOutcome, domain.DecisionOutcomePayload{Text: "done"})
}

// Withdraw withdraws callID, written by its owning session.
func Withdraw(callID string, owner domain.SessionID) domain.DecisionEntry {
	return session(callID, owner, domain.DecisionKindWithdrawn, map[string]string{"note": "moot"})
}

// CarriedOut confirms the decided entry forEntry was carried out.
func CarriedOut(callID string, owner domain.SessionID, forEntry string) domain.DecisionEntry {
	return session(callID, owner, domain.DecisionKindCarriedOut, domain.DecisionCarriedOutPayload{ForEntry: forEntry})
}

// Delivered records delivery of forEntry to owner, written by the owning
// machine's deliverer.
func Delivered(callID string, owner domain.SessionID, forEntry string) domain.DecisionEntry {
	return domain.DecisionEntry{
		ID: NewEntryID(), CallID: callID, Kind: domain.DecisionKindDelivered,
		Payload:    mustJSON(domain.DecisionDeliveredPayload{ForEntry: forEntry, Status: domain.DecisionDeliveredSent, SessionID: owner}),
		AuthorKind: domain.DecisionAuthorSystem,
	}
}

// Future is an entry of a kind this build does not know, with payload bytes
// chosen to expose any re-encoding on the way.
func Future(callID string) domain.DecisionEntry {
	return domain.DecisionEntry{
		ID: NewEntryID(), CallID: callID, Kind: "future_kind",
		Payload:    json.RawMessage(`{"z":1,  "a":"<&> é \u00e9","nested":{"unknown":[1,2.50]}}`),
		AuthorKind: domain.DecisionAuthorPerson, AuthorSurface: "phone",
	}
}

func person(callID string, kind domain.DecisionEntryKind, payload any) domain.DecisionEntry {
	return domain.DecisionEntry{
		ID: NewEntryID(), CallID: callID, Kind: kind, Payload: mustJSON(payload),
		AuthorKind: domain.DecisionAuthorPerson, AuthorSurface: domain.RequestSurfaceDesktop,
	}
}

func session(callID string, owner domain.SessionID, kind domain.DecisionEntryKind, payload any) domain.DecisionEntry {
	return domain.DecisionEntry{
		ID: NewEntryID(), CallID: callID, Kind: kind, Payload: mustJSON(payload),
		AuthorKind: domain.DecisionAuthorSession, AuthorSession: owner,
	}
}
