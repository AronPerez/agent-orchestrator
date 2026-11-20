package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrDecisionEntryInvalid reports a ledger entry missing required identity.
var ErrDecisionEntryInvalid = errors.New("invalid decision entry")

// MaxDecisionTitle and the other limits bound user-authored ledger payloads.
const (
	MaxDecisionTitle       = 120
	MaxDecisionContext     = 1000
	MinDecisionOptions     = 2
	MaxDecisionOptions     = 4
	MaxDecisionOptionLabel = 60
	MaxDecisionConsequence = 200
	MaxDecisionLinks       = 5
	MaxDecisionLinkLabel   = 120
	MaxDecisionLinkURL     = 2048
	MaxDecisionReference   = 64
	MaxDecisionNote        = 1000
	MaxDecisionOutcome     = 300
	MaxDecisionWhy         = 300
	MaxDecisionUndo        = 300
	// PlanFirstDecisionReference identifies the one ledger call that can release
	// a session's plan-first implementation gate.
	PlanFirstDecisionReference = "plan-first"
)

// DecisionUrgencyBlocking and DecisionUrgencyCanWait say how soon a call needs
// the person. A blocking call stops all of the session's remaining work.
const (
	DecisionUrgencyBlocking = "blocking"
	DecisionUrgencyCanWait  = "can_wait"
)

// DecisionPrediction is what the session would have decided on its own: the
// option it would pick, why, and how the person could undo it.
type DecisionPrediction struct {
	Option int    `json:"option"`
	Why    string `json:"why"`
	Undo   string `json:"undo"`
}

// DecisionEntryKind identifies one fact appended to the decision ledger.
type DecisionEntryKind string

// DecisionKindRaised and its peers name facts that can be appended to a call.
const (
	DecisionKindRaised    DecisionEntryKind = "raised"
	DecisionKindAnswered  DecisionEntryKind = "answered"
	DecisionKindDismissed DecisionEntryKind = "dismissed"
	DecisionKindDelivered DecisionEntryKind = "delivered"
	DecisionKindOutcome   DecisionEntryKind = "outcome"
	DecisionKindWithdrawn DecisionEntryKind = "withdrawn"
	DecisionKindRetired   DecisionEntryKind = "retired"
	// DecisionKindDecided records a decision a session made on the person's
	// behalf in autonomy mode on. It starts its own call, in place of raised.
	DecisionKindDecided DecisionEntryKind = "decided"
	// DecisionKindOverruled records the person overruling a decided call. It
	// stands like an answer and is delivered to the owning session.
	DecisionKindOverruled DecisionEntryKind = "overruled"
	// DecisionKindCarriedOut records the owning session confirming it did
	// what a decided entry recorded. Sessions record before acting, so a
	// decision without it is an intent that may not have happened.
	DecisionKindCarriedOut DecisionEntryKind = "carried_out"
)

// DecisionAuthorKind identifies who wrote a ledger entry.
type DecisionAuthorKind string

// DecisionAuthorPerson and its peers name the writer of a ledger fact.
const (
	DecisionAuthorPerson  DecisionAuthorKind = "person"
	DecisionAuthorSession DecisionAuthorKind = "session"
	DecisionAuthorSystem  DecisionAuthorKind = "system"
)

// DecisionStamp orders ledger entries across machines.
type DecisionStamp struct {
	WallMS  int64 `json:"wallMs"`
	Counter int64 `json:"counter"`
}

// Next is the stamp for an entry written at nowMS on a machine whose newest
// stored stamp is s. It is always greater than s.
func (s DecisionStamp) Next(nowMS int64) DecisionStamp {
	if nowMS > s.WallMS {
		return DecisionStamp{WallMS: nowMS}
	}
	return DecisionStamp{WallMS: s.WallMS, Counter: s.Counter + 1}
}

// DecisionEntry is one immutable fact in the decision ledger.
type DecisionEntry struct {
	ID            string
	Origin        string
	OriginSeq     int64
	Stamp         DecisionStamp
	CallID        string
	Kind          DecisionEntryKind
	Payload       json.RawMessage
	WrittenAt     time.Time
	AuthorKind    DecisionAuthorKind
	AuthorSurface RequestSurface
	AuthorSession SessionID
}

// CompareDecisionEntries orders by stamp, origin, then entry id.
func CompareDecisionEntries(a, b DecisionEntry) int {
	if a.Stamp.WallMS != b.Stamp.WallMS {
		if a.Stamp.WallMS < b.Stamp.WallMS {
			return -1
		}
		return 1
	}
	if a.Stamp.Counter != b.Stamp.Counter {
		if a.Stamp.Counter < b.Stamp.Counter {
			return -1
		}
		return 1
	}
	if a.Origin != b.Origin {
		if a.Origin < b.Origin {
			return -1
		}
		return 1
	}
	if a.ID < b.ID {
		return -1
	}
	if a.ID > b.ID {
		return 1
	}
	return 0
}

// DecisionOption is one choice offered by a call and its consequence.
type DecisionOption struct {
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
	Recommended bool   `json:"recommended,omitempty"`
}

// DecisionLink points to supporting material for a call.
type DecisionLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// DecisionPageRef identifies an immutable page snapshot by content fingerprint.
type DecisionPageRef struct {
	Fingerprint string `json:"fingerprint"`
	Size        int64  `json:"size"`
}

// DecisionRaisedPayload is the complete question recorded by a raised entry.
type DecisionRaisedPayload struct {
	Title            string           `json:"title"`
	Context          string           `json:"context"`
	Options          []DecisionOption `json:"options"`
	Links            []DecisionLink   `json:"links,omitempty"`
	Reference        string           `json:"reference,omitempty"`
	Page             *DecisionPageRef `json:"page,omitempty"`
	ProjectID        ProjectID        `json:"projectId,omitempty"`
	ProjectName      string           `json:"projectName,omitempty"`
	StreamSessionID  SessionID        `json:"streamSessionId,omitempty"`
	StreamName       string           `json:"streamName,omitempty"`
	MachineLabel     string           `json:"machineLabel"`
	OwnerSessionID   SessionID        `json:"ownerSessionId"`
	OwnerSessionName string           `json:"ownerSessionName"`

	// Urgency, Kind and Prediction were added by the autonomy-levels design.
	// Entries written before it omit them; older machines ignore them.
	Urgency    string              `json:"urgency,omitempty"`
	Kind       DecisionKindID      `json:"kind,omitempty"`
	Prediction *DecisionPrediction `json:"prediction,omitempty"`
	// Reserved and AutonomyMode are filled in by Orchestrator from the
	// project's policy when the call is raised, so later policy changes do
	// not rewrite what the call was raised under.
	Reserved     bool         `json:"reserved,omitempty"`
	AutonomyMode AutonomyMode `json:"autonomyMode,omitempty"`
}

// EffectiveUrgency returns the call's urgency. Calls raised before urgency
// existed, or without one, can wait.
func (p DecisionRaisedPayload) EffectiveUrgency() string {
	if p.Urgency == "" {
		return DecisionUrgencyCanWait
	}
	return p.Urgency
}

// DecisionChoiceOption and DecisionChoiceOther distinguish answer forms.
const (
	DecisionChoiceOption = "option"
	DecisionChoiceOther  = "other"
)

// DecisionAnsweredPayload records the person's current answer to a call.
type DecisionAnsweredPayload struct {
	Choice string `json:"choice"`
	Option int    `json:"option,omitempty"`
	Note   string `json:"note,omitempty"`
}

// DecisionDismissedPayload records why a person dismissed a call.
type DecisionDismissedPayload struct {
	Note string `json:"note,omitempty"`
}

// DecisionDeliveredSent and its peers describe delivery attempts.
const (
	DecisionDeliveredSent      = "sent"
	DecisionDeliveredNoSession = "no_session"
	// DecisionDeliveredSuperseded closes an undelivered historical answer after
	// a newer standing answer replaces it. It is ledger history, not a public
	// delivery state for the current answer.
	DecisionDeliveredSuperseded = "superseded"
)

// DecisionDeliveredPayload records delivery of an answer to a session.
type DecisionDeliveredPayload struct {
	ForEntry    string    `json:"forEntry"`
	Status      string    `json:"status"`
	SessionID   SessionID `json:"sessionId,omitempty"`
	SessionName string    `json:"sessionName,omitempty"`
	Fallback    bool      `json:"fallback,omitempty"`
}

// DecisionOutcomePayload records what happened after a decision was carried out.
type DecisionOutcomePayload struct {
	ForEntry string `json:"forEntry,omitempty"`
	Text     string `json:"text"`
	Link     string `json:"link,omitempty"`
}

// DecisionRetiredPayload changes whether a call is hidden from active views.
type DecisionRetiredPayload struct {
	Retired bool `json:"retired"`
}

// DecisionRuleError names the first decision rule a payload broke.
type DecisionRuleError struct {
	Rule    string
	Message string
}

func (e *DecisionRuleError) Error() string { return e.Message }

// ValidateDecisionRaise applies the user-facing rules for a new call.
func ValidateDecisionRaise(p DecisionRaisedPayload) error {
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return decisionRule("title_required", "A call needs a title: the question itself, in one line.")
	}
	if strings.ContainsAny(title, "\r\n") {
		return decisionRule("title_one_line", "The title must be one line.")
	}
	if n := utf8.RuneCountInString(title); n > MaxDecisionTitle {
		return decisionRule("title_too_long", fmt.Sprintf("The title is %d characters; the limit is 120.", n))
	}

	context := strings.TrimSpace(p.Context)
	if context == "" {
		return decisionRule("context_required", "A call needs context: what the person needs to know to choose.")
	}
	if n := utf8.RuneCountInString(context); n > MaxDecisionContext {
		return decisionRule("context_too_long", fmt.Sprintf("The context is %d characters; the limit is 1000.", n))
	}
	if len(p.Options) < MinDecisionOptions || len(p.Options) > MaxDecisionOptions {
		return decisionRule("options_count", fmt.Sprintf("A call needs two to four options; this one has %d.", len(p.Options)))
	}

	recommended := 0
	for i, option := range p.Options {
		position := i + 1
		label := strings.TrimSpace(option.Label)
		if label == "" {
			return decisionRule("option_label_required", fmt.Sprintf("Option %d has no label.", position))
		}
		if n := utf8.RuneCountInString(label); n > MaxDecisionOptionLabel {
			return decisionRule("option_label_too_long", fmt.Sprintf("Option %d's label is %d characters; the limit is 60.", position, n))
		}
		consequence := strings.TrimSpace(option.Consequence)
		if consequence == "" {
			return decisionRule("option_consequence_required", fmt.Sprintf("Option %d does not say what it leads to.", position))
		}
		if n := utf8.RuneCountInString(consequence); n > MaxDecisionConsequence {
			return decisionRule("option_consequence_too_long", fmt.Sprintf("Option %d's consequence is %d characters; the limit is 200.", position, n))
		}
		if option.Recommended {
			recommended++
		}
	}
	if recommended != 1 {
		return decisionRule("recommended_count", fmt.Sprintf("Mark exactly one option as recommended; this call marks %d.", recommended))
	}
	if err := validateDecisionLinksAndReference(p.Links, p.Reference); err != nil {
		return err
	}
	if err := validateDecisionAutonomy(p); err != nil {
		return err
	}
	return validateRaiseSecrets(p)
}

func validateDecisionLinksAndReference(links []DecisionLink, reference string) error {
	if len(links) > MaxDecisionLinks {
		return decisionRule("links_count", fmt.Sprintf("A call can carry at most five links; this one has %d.", len(links)))
	}
	for i, link := range links {
		label := strings.TrimSpace(link.Label)
		target := strings.TrimSpace(link.URL)
		if label == "" || !validDecisionURL(target) {
			return decisionRule("link_invalid", fmt.Sprintf("Link %d needs a label and an http or https URL.", i+1))
		}
		if n := utf8.RuneCountInString(label); n > MaxDecisionLinkLabel {
			return decisionRule("link_label_too_long", fmt.Sprintf("Link %d's label is %d characters; the limit is 120.", i+1, n))
		}
		if n := utf8.RuneCountInString(target); n > MaxDecisionLinkURL {
			return decisionRule("link_url_too_long", fmt.Sprintf("Link %d's URL is %d characters; the limit is 2048.", i+1, n))
		}
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(reference)); n > MaxDecisionReference {
		return decisionRule("reference_too_long", fmt.Sprintf("The reference is %d characters; the limit is 64.", n))
	}
	return nil
}

func validateDecisionAutonomy(p DecisionRaisedPayload) error {
	switch p.Urgency {
	case "", DecisionUrgencyBlocking, DecisionUrgencyCanWait:
	default:
		return decisionRule("urgency_invalid", fmt.Sprintf("Urgency must be blocking or can-wait, not %q.", p.Urgency))
	}
	if p.Kind != "" {
		if _, ok := LookupDecisionKind(string(p.Kind)); !ok {
			return decisionRule("kind_unknown", fmt.Sprintf("%q is not a decision kind. Run ao decision kinds to list them.", p.Kind))
		}
	}
	prediction := p.Prediction
	if prediction == nil {
		return nil
	}
	if prediction.Option < 1 || prediction.Option > len(p.Options) {
		return decisionRule("prediction_option_range", fmt.Sprintf("This call has %d options; you would decide %d, which is not one of them.", len(p.Options), prediction.Option))
	}
	why := strings.TrimSpace(prediction.Why)
	if why == "" {
		return decisionRule("prediction_why_required", "Say why you would decide that, with --why.")
	}
	if n := utf8.RuneCountInString(why); n > MaxDecisionWhy {
		return decisionRule("prediction_why_too_long", fmt.Sprintf("The reason is %d characters; the limit is 300.", n))
	}
	undo := strings.TrimSpace(prediction.Undo)
	if undo == "" {
		return decisionRule("prediction_undo_required", "Say how the person could undo it, with --undo.")
	}
	if n := utf8.RuneCountInString(undo); n > MaxDecisionUndo {
		return decisionRule("prediction_undo_too_long", fmt.Sprintf("The undo note is %d characters; the limit is 300.", n))
	}
	return nil
}

// ValidateDecisionAnswer checks an answer against the call's option count.
func ValidateDecisionAnswer(optionCount int, p DecisionAnsweredPayload) error {
	if p.Choice != DecisionChoiceOption && p.Choice != DecisionChoiceOther {
		return decisionRule("choice_invalid", "Choose one of the call's options, or \"something else\".")
	}
	if p.Choice == DecisionChoiceOption && (p.Option < 1 || p.Option > optionCount) {
		return decisionRule("option_range", fmt.Sprintf("This call has %d options; %d is not one of them.", optionCount, p.Option))
	}
	if p.Choice == DecisionChoiceOther && strings.TrimSpace(p.Note) == "" {
		return decisionRule("note_required", "\"Something else\" needs a note that says what to do.")
	}
	return ValidateDecisionNote(p.Note)
}

// ValidateDecisionNote checks an optional answer or dismissal note.
func ValidateDecisionNote(note string) error {
	if n := utf8.RuneCountInString(strings.TrimSpace(note)); n > MaxDecisionNote {
		return decisionRule("note_too_long", fmt.Sprintf("The note is %d characters; the limit is 1000.", n))
	}
	return rejectDecisionSecrets(note)
}

// ValidateDecisionOutcome checks the final result recorded for a call.
func ValidateDecisionOutcome(p DecisionOutcomePayload) error {
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return decisionRule("outcome_required", "Say what happened, in one line.")
	}
	if strings.ContainsAny(text, "\r\n") {
		return decisionRule("outcome_one_line", "The outcome must be one line.")
	}
	if n := utf8.RuneCountInString(text); n > MaxDecisionOutcome {
		return decisionRule("outcome_too_long", fmt.Sprintf("The outcome is %d characters; the limit is 300.", n))
	}
	link := strings.TrimSpace(p.Link)
	if n := utf8.RuneCountInString(link); n > MaxDecisionLinkURL {
		return decisionRule("outcome_link_too_long", fmt.Sprintf("The outcome link is %d characters; the limit is 2048.", n))
	}
	if link != "" && !validDecisionURL(link) {
		return decisionRule("outcome_link_invalid", "The outcome link must be an http or https URL.")
	}
	return rejectDecisionSecrets(p.Text, p.Link)
}

// DecisionCallURL is the in-app link for a decision call.
func DecisionCallURL(callID string) string {
	return "ao://decisions/" + url.PathEscape(callID)
}

func decisionRule(rule, message string) error {
	return &DecisionRuleError{Rule: rule, Message: message}
}

func validDecisionURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
