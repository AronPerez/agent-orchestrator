package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxDecisionVerify bounds the optional "how we'll know it worked" note.
const MaxDecisionVerify = 300

// MaxDecisionKey bounds the session-chosen name of one intended action.
const MaxDecisionKey = 64

// DecisionOutsidePolicyNotOn and DecisionOutsidePolicyReserved say why a
// decided entry falls outside the project's policy when it is recorded. A
// session acts before it records, so a record is never refused for policy:
// the action already happened and the person must be able to see it.
const (
	DecisionOutsidePolicyNotOn    = "autonomy_not_on"
	DecisionOutsidePolicyReserved = "kind_reserved"
)

// DecisionDecidedPayload is a decision a session made on the person's behalf:
// what it decided, why, how to undo it, and optionally how to tell it worked.
// Orchestrator fills in the same origin fields as a raised call.
type DecisionDecidedPayload struct {
	// Key is the session's own name for this one intended action. A retry
	// with the same owner and key returns the existing record.
	Key              string         `json:"key,omitempty"`
	What             string         `json:"what"`
	Why              string         `json:"why"`
	Undo             string         `json:"undo"`
	Verify           string         `json:"verify,omitempty"`
	Kind             DecisionKindID `json:"kind"`
	Links            []DecisionLink `json:"links,omitempty"`
	Reference        string         `json:"reference,omitempty"`
	ProjectID        ProjectID      `json:"projectId,omitempty"`
	ProjectName      string         `json:"projectName,omitempty"`
	StreamSessionID  SessionID      `json:"streamSessionId,omitempty"`
	StreamName       string         `json:"streamName,omitempty"`
	MachineLabel     string         `json:"machineLabel"`
	OwnerSessionID   SessionID      `json:"ownerSessionId"`
	OwnerSessionName string         `json:"ownerSessionName"`
	AutonomyMode     AutonomyMode   `json:"autonomyMode,omitempty"`
	// OutsidePolicy is filled in by Orchestrator when the project's policy
	// did not allow this decision at the time it was recorded.
	OutsidePolicy string `json:"outsidePolicy,omitempty"`
}

// DecisionCarriedOutPayload confirms the decided entry it names was carried out.
type DecisionCarriedOutPayload struct {
	ForEntry string `json:"forEntry"`
}

// DecisionOverruledPayload records why the person overruled a decided call.
type DecisionOverruledPayload struct {
	Note string `json:"note"`
}

// DecisionDecidedDetail is what a decided call carries beyond the fields it
// shares with a raised call.
type DecisionDecidedDetail struct {
	// EntryID is the decided entry itself. An outcome recorded while the
	// decision still stands (for example, "not carried out") names it.
	EntryID       string `json:"entryId"`
	Key           string `json:"key,omitempty"`
	Undo          string `json:"undo"`
	Verify        string `json:"verify,omitempty"`
	OutsidePolicy string `json:"outsidePolicy,omitempty"`
	// CarriedOut is true once the owning session confirmed it acted.
	CarriedOut   bool      `json:"carriedOut,omitempty"`
	CarriedOutAt time.Time `json:"carriedOutAt"`
}

// raisedView presents a decided call through the raised payload, so lists,
// streams, search and storage treat both kinds of call alike.
func (p DecisionDecidedPayload) raisedView() DecisionRaisedPayload {
	return DecisionRaisedPayload{
		Title: p.What, Context: p.Why, Links: p.Links, Reference: p.Reference, Kind: p.Kind,
		ProjectID: p.ProjectID, ProjectName: p.ProjectName,
		StreamSessionID: p.StreamSessionID, StreamName: p.StreamName,
		MachineLabel: p.MachineLabel, OwnerSessionID: p.OwnerSessionID, OwnerSessionName: p.OwnerSessionName,
		AutonomyMode: p.AutonomyMode,
	}
}

// ValidateDecisionDecided applies the user-facing rules for a decided entry.
func ValidateDecisionDecided(p DecisionDecidedPayload) error {
	what := strings.TrimSpace(p.What)
	if what == "" {
		return decisionRule("what_required", "Say what you decided, in one line.")
	}
	if strings.ContainsAny(what, "\r\n") {
		return decisionRule("what_one_line", "What you decided must be one line.")
	}
	if n := utf8.RuneCountInString(what); n > MaxDecisionTitle {
		return decisionRule("what_too_long", fmt.Sprintf("What you decided is %d characters; the limit is 120.", n))
	}
	why := strings.TrimSpace(p.Why)
	if why == "" {
		return decisionRule("why_required", "Say why you decided it.")
	}
	if n := utf8.RuneCountInString(why); n > MaxDecisionContext {
		return decisionRule("why_too_long", fmt.Sprintf("The reason is %d characters; the limit is 1000.", n))
	}
	undo := strings.TrimSpace(p.Undo)
	if undo == "" {
		return decisionRule("undo_required", "Say how the person can undo it. A decision made for the person always needs a way back.")
	}
	if n := utf8.RuneCountInString(undo); n > MaxDecisionUndo {
		return decisionRule("undo_too_long", fmt.Sprintf("The undo note is %d characters; the limit is 300.", n))
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(p.Verify)); n > MaxDecisionVerify {
		return decisionRule("verify_too_long", fmt.Sprintf("The note on how to tell it worked is %d characters; the limit is 300.", n))
	}
	key := strings.TrimSpace(p.Key)
	if strings.ContainsAny(key, "\r\n") {
		return decisionRule("key_one_line", "The key must be one line.")
	}
	if n := utf8.RuneCountInString(key); n > MaxDecisionKey {
		return decisionRule("key_too_long", fmt.Sprintf("The key is %d characters; the limit is 64.", n))
	}
	if p.Kind == "" {
		return decisionRule("kind_required", "Say what kind of decision it was with --kind. Run ao decision kinds to list them.")
	}
	if _, ok := LookupDecisionKind(string(p.Kind)); !ok {
		return decisionRule("kind_unknown", fmt.Sprintf("%q is not a decision kind. Run ao decision kinds to list them.", p.Kind))
	}
	if err := validateDecisionLinksAndReference(p.Links, p.Reference); err != nil {
		return err
	}
	return validateDecidedSecrets(p)
}

// ValidateDecisionOverrule checks the person's note on an overrule. It is
// required, because it is what tells the session what to do instead.
func ValidateDecisionOverrule(note string) error {
	if strings.TrimSpace(note) == "" {
		return decisionRule("overrule_note_required", "Say what to do instead. The note goes to the session that made the decision.")
	}
	return ValidateDecisionNote(note)
}
