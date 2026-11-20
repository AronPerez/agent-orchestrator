package domain

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// DecisionCallState is the derived lifecycle state of a decision call.
type DecisionCallState string

// DecisionStateNeedsYou and its peers are the possible derived call states.
const (
	DecisionStateNeedsYou   DecisionCallState = "needs_you"
	DecisionStateInProgress DecisionCallState = "in_progress"
	DecisionStateDone       DecisionCallState = "done"
	DecisionStateDismissed  DecisionCallState = "dismissed"
	DecisionStateWithdrawn  DecisionCallState = "withdrawn"
	// DecisionStateDecided is a decision a session made on the person's
	// behalf that nobody has overruled.
	DecisionStateDecided DecisionCallState = "decided"
)

// Label is the one user-facing name for a decision state.
func (s DecisionCallState) Label() string {
	switch s {
	case DecisionStateNeedsYou:
		return "Needs you"
	case DecisionStateInProgress:
		return "In progress"
	case DecisionStateDone:
		return "Done"
	case DecisionStateDismissed:
		return "Dismissed"
	case DecisionStateWithdrawn:
		return "Withdrawn"
	case DecisionStateDecided:
		return "Decided for you"
	default:
		return ""
	}
}

// DecisionDeliveryState is the derived delivery status of the standing answer.
type DecisionDeliveryState string

// DecisionDeliveryNone and its peers are the possible answer delivery states.
const (
	DecisionDeliveryNone      DecisionDeliveryState = ""
	DecisionDeliveryPending   DecisionDeliveryState = "pending"
	DecisionDeliverySent      DecisionDeliveryState = "sent"
	DecisionDeliveryNoSession DecisionDeliveryState = "no_session"
)

// DecisionCall is the read model reduced from a call's immutable entries.
type DecisionCall struct {
	ID              string                `json:"id"`
	Origin          string                `json:"origin"`
	Raised          DecisionRaisedPayload `json:"raised"`
	RaisedAt        time.Time             `json:"raisedAt"`
	State           DecisionCallState     `json:"state"`
	StandingEntryID string                `json:"standingEntryId,omitempty"`
	StandingKind    DecisionEntryKind     `json:"standingKind,omitempty"`
	Choice          string                `json:"choice,omitempty"`
	Option          int                   `json:"option,omitempty"`
	Note            string                `json:"note,omitempty"`
	AnsweredAt      time.Time             `json:"answeredAt"`
	AnswerSurface   RequestSurface        `json:"answerSurface,omitempty"`
	// AnswerKeyDigest is the idempotency-key digest of the standing answer, if
	// it was sent with one: a client can tell whether its uncertain attempt is
	// the one recorded.
	AnswerKeyDigest   string                `json:"answerKeyDigest,omitempty"`
	Outcome           string                `json:"outcome,omitempty"`
	OutcomeLink       string                `json:"outcomeLink,omitempty"`
	OutcomeAt         time.Time             `json:"outcomeAt"`
	Retired           bool                  `json:"retired,omitempty"`
	Delivery          DecisionDeliveryState `json:"delivery,omitempty"`
	DeliveredTo       SessionID             `json:"deliveredTo,omitempty"`
	DeliveredToName   string                `json:"deliveredToName,omitempty"`
	DeliveredFallback bool                  `json:"deliveredFallback,omitempty"`
	Redelivery        bool                  `json:"redelivery,omitempty"`
	ChangedAfterClose bool                  `json:"changedAfterClose,omitempty"`
	LastStamp         DecisionStamp         `json:"lastStamp"`
	// Decided is set for a call that began as a decision made on the
	// person's behalf rather than as a question.
	Decided *DecisionDecidedDetail `json:"decided,omitempty"`
}

type reducedDelivery struct {
	entry   DecisionEntry
	payload DecisionDeliveredPayload
}

// ReduceDecisionCall computes a call from all of its entries, in any order.
// It returns false when there is no valid raised entry.
func ReduceDecisionCall(entries []DecisionEntry) (DecisionCall, bool) {
	ordered := append([]DecisionEntry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool {
		return CompareDecisionEntries(ordered[i], ordered[j]) < 0
	})

	var call DecisionCall
	var raised bool
	var standing DecisionEntry
	var standingKind DecisionEntryKind
	var closing DecisionEntry
	var hasClosing bool
	var hasOutcome bool
	var withdrawn bool
	var closedUnconfirmed, reopened bool
	answered := make(map[string]DecisionEntry)
	deliveries := make([]reducedDelivery, 0)
	// After an identity repair a copy stands in for its original: an entry
	// that names the original (a delivery, an outcome, a confirmation) counts
	// for the copy, so nothing already delivered is delivered again.
	copyOf := map[string]string{}
	for _, entry := range ordered {
		if from, err := DecisionReissuedFrom(entry.Payload); err == nil && from != "" {
			copyOf[from] = entry.ID
		}
	}
	current := func(id string) string {
		if c, ok := copyOf[id]; ok {
			return c
		}
		return id
	}

	for _, entry := range ordered {
		call.LastStamp = entry.Stamp
		switch entry.Kind {
		case DecisionKindRaised:
			if raised {
				continue
			}
			var payload DecisionRaisedPayload
			if json.Unmarshal(entry.Payload, &payload) != nil {
				continue
			}
			raised = true
			call.ID = entry.CallID
			call.Origin = entry.Origin
			call.Raised = payload
			call.RaisedAt = entry.WrittenAt
		case DecisionKindDecided:
			if raised {
				continue
			}
			var payload DecisionDecidedPayload
			if json.Unmarshal(entry.Payload, &payload) != nil {
				continue
			}
			raised = true
			call.ID = entry.CallID
			call.Origin = entry.Origin
			call.Raised = payload.raisedView()
			call.RaisedAt = entry.WrittenAt
			call.Decided = &DecisionDecidedDetail{EntryID: entry.ID, Key: payload.Key, Undo: payload.Undo, Verify: payload.Verify, OutsidePolicy: payload.OutsidePolicy}
		case DecisionKindOverruled:
			// Entries are ordered, so a decided call's own entry precedes any
			// overrule of it; an overrule on any other call is ignored.
			if call.Decided == nil {
				continue
			}
			var payload DecisionOverruledPayload
			if json.Unmarshal(entry.Payload, &payload) != nil {
				continue
			}
			standing = entry
			standingKind = entry.Kind
			call.StandingEntryID = entry.ID
			call.StandingKind = entry.Kind
			call.Choice = ""
			call.Option = 0
			call.Note = payload.Note
			call.AnsweredAt = entry.WrittenAt
			call.AnswerSurface = entry.AuthorSurface
			call.AnswerKeyDigest = ""
		case DecisionKindCarriedOut:
			var payload DecisionCarriedOutPayload
			if call.Decided == nil || json.Unmarshal(entry.Payload, &payload) != nil || current(payload.ForEntry) != call.Decided.EntryID {
				continue
			}
			if !call.Decided.CarriedOut {
				call.Decided.CarriedOut = true
				call.Decided.CarriedOutAt = entry.WrittenAt
			}
			// A confirmation that arrives after the decision was closed while
			// unconfirmed contradicts that close: the action did happen. Reopen
			// it so it stays visible and overrulable; the close stays in history.
			if closedUnconfirmed {
				closedUnconfirmed, reopened = false, true
				hasOutcome, hasClosing, closing = false, false, DecisionEntry{}
				call.Outcome, call.OutcomeLink, call.OutcomeAt = "", "", time.Time{}
			}
		case DecisionKindAnswered:
			var payload DecisionAnsweredPayload
			if json.Unmarshal(entry.Payload, &payload) != nil {
				continue
			}
			standing = entry
			standingKind = entry.Kind
			answered[entry.ID] = entry
			call.StandingEntryID = entry.ID
			call.StandingKind = entry.Kind
			call.Choice = payload.Choice
			call.Option = payload.Option
			call.Note = payload.Note
			call.AnsweredAt = entry.WrittenAt
			call.AnswerSurface = entry.AuthorSurface
			call.AnswerKeyDigest = payload.KeyDigest()
		case DecisionKindDismissed:
			var payload DecisionDismissedPayload
			if json.Unmarshal(entry.Payload, &payload) != nil {
				continue
			}
			standing = entry
			standingKind = entry.Kind
			call.StandingEntryID = entry.ID
			call.StandingKind = entry.Kind
			call.Choice = ""
			call.Option = 0
			call.Note = payload.Note
			call.AnsweredAt = time.Time{}
			call.AnswerSurface = ""
			call.AnswerKeyDigest = ""
		case DecisionKindDelivered:
			var payload DecisionDeliveredPayload
			if json.Unmarshal(entry.Payload, &payload) != nil {
				continue
			}
			payload.ForEntry = current(payload.ForEntry)
			deliveries = append(deliveries, reducedDelivery{entry: entry, payload: payload})
		case DecisionKindOutcome:
			var payload DecisionOutcomePayload
			if json.Unmarshal(entry.Payload, &payload) != nil {
				continue
			}
			if payload.ForEntry != "" {
				payload.ForEntry = current(payload.ForEntry)
			}
			// New outcomes name the immutable answer they carried out. If a
			// newer answer already stands, keep the stale outcome in history but
			// do not let it close that newer work. Empty preserves legacy entries.
			// An outcome on a decision that still stands names the decided
			// entry itself, since there is no answer to name.
			closesStandingDecision := standing.ID == "" && call.Decided != nil && payload.ForEntry == call.Decided.EntryID
			if payload.ForEntry != "" && payload.ForEntry != standing.ID && !closesStandingDecision {
				continue
			}
			if !hasClosing {
				closing, hasClosing = entry, true
			}
			hasOutcome = true
			closedUnconfirmed = closesStandingDecision && !call.Decided.CarriedOut
			call.Outcome = payload.Text
			call.OutcomeLink = payload.Link
			call.OutcomeAt = entry.WrittenAt
		case DecisionKindWithdrawn:
			var payload struct{}
			if json.Unmarshal(entry.Payload, &payload) != nil {
				continue
			}
			withdrawn = true
			if !hasClosing {
				closing, hasClosing = entry, true
			}
		case DecisionKindRetired:
			var payload DecisionRetiredPayload
			if json.Unmarshal(entry.Payload, &payload) != nil {
				continue
			}
			call.Retired = payload.Retired
		}
	}

	if !raised {
		return DecisionCall{}, false
	}
	if hasOutcome {
		call.State = DecisionStateDone
	} else if withdrawn {
		call.State = DecisionStateWithdrawn
	} else if standingKind == DecisionKindAnswered || standingKind == DecisionKindOverruled {
		call.State = DecisionStateInProgress
	} else if standingKind == DecisionKindDismissed {
		call.State = DecisionStateDismissed
	} else if call.Decided != nil {
		call.State = DecisionStateDecided
	} else {
		call.State = DecisionStateNeedsYou
	}
	if reopened {
		call.ChangedAfterClose = true
	}
	if hasClosing && standing.ID != "" && CompareDecisionEntries(closing, standing) < 0 {
		call.ChangedAfterClose = true
	}

	if standing.ID == "" {
		call.Delivery = DecisionDeliveryNone
		return call, true
	}
	call.Delivery = DecisionDeliveryPending
	var lastSent *reducedDelivery
	hasNoSession := false
	for i := range deliveries {
		delivery := &deliveries[i]
		if delivery.payload.ForEntry != standing.ID {
			continue
		}
		switch delivery.payload.Status {
		case DecisionDeliveredSent:
			lastSent = delivery
		case DecisionDeliveredNoSession:
			hasNoSession = true
		}
	}
	if lastSent != nil {
		call.Delivery = DecisionDeliverySent
		call.DeliveredTo = lastSent.payload.SessionID
		call.DeliveredToName = lastSent.payload.SessionName
		call.DeliveredFallback = lastSent.payload.Fallback
	} else if hasNoSession {
		call.Delivery = DecisionDeliveryNoSession
	}

	if call.Delivery == DecisionDeliveryPending {
		for _, delivery := range deliveries {
			answeredEntry, ok := answered[delivery.payload.ForEntry]
			if ok && delivery.payload.Status == DecisionDeliveredSent && CompareDecisionEntries(answeredEntry, standing) < 0 {
				call.Redelivery = true
				break
			}
		}
	}
	return call, true
}

// PredictionAgreed compares the session's prediction with the person's
// standing answer. known is false until there is both a prediction and an
// answer; "something else" counts as a different decision.
func (c DecisionCall) PredictionAgreed() (agreed, known bool) {
	if c.Raised.Prediction == nil || c.StandingKind != DecisionKindAnswered {
		return false, false
	}
	return c.Choice == DecisionChoiceOption && c.Option == c.Raised.Prediction.Option, true
}

// ChosenLabel returns the label represented by the current answer.
func (c DecisionCall) ChosenLabel() string {
	if c.Choice == DecisionChoiceOther {
		return "Something else"
	}
	if c.Choice != DecisionChoiceOption || c.Option < 1 || c.Option > len(c.Raised.Options) {
		return ""
	}
	return c.Raised.Options[c.Option-1].Label
}

// SearchText is the call text indexed by the local full-text search table.
func (c DecisionCall) SearchText() string {
	parts := make([]string, 0, 5+len(c.Raised.Options))
	appendText := func(value string) {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	appendText(c.Raised.Title)
	appendText(c.Raised.Context)
	for _, option := range c.Raised.Options {
		appendText(option.Label)
	}
	appendText(c.Raised.Reference)
	appendText(c.Note)
	appendText(c.Outcome)
	return strings.Join(parts, "\n")
}
