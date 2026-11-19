package domain

import "fmt"

// DecisionKindID names one kind of decision in the fixed vocabulary that
// calls, decided entries and a project's reserved list all share.
type DecisionKindID string

// DecisionKindOther is the catch-all kind. It is reserved by default so a
// decision that fits no other kind is never made without the person.
const DecisionKindOther DecisionKindID = "other"

// DecisionKindInfo describes one decision kind.
type DecisionKindInfo struct {
	ID                DecisionKindID `json:"id"`
	Label             string         `json:"label"`
	ReservedByDefault bool           `json:"reservedByDefault"`
}

// decisionKinds is the vocabulary from the autonomy-levels design
// (docs/superpowers/specs/2026-11-18-autonomy-levels-design.md). Ids are
// stored in ledger entries and project config, so they never change meaning.
var decisionKinds = []DecisionKindInfo{
	{"production", "Production changes and deploys", true},
	{"merge_publish", "Merges, releases or publishing beyond what was authorized", true},
	{"destructive", "Destructive or hard-to-undo operations", true},
	{"credentials", "Secrets, credentials, passwords and 2FA", true},
	{"spending", "Spending, and new paid or public infrastructure", true},
	{"public_api", "Public API or schema changes", true},
	{"dependency", "New dependencies", true},
	{"scope", "Scope changes", true},
	{"design", "Design and product behaviour", true},
	{"people", "Actions that reach other people", true},
	{"project_default", "Departing from a project default (agent, model, vendor)", true},
	{"ticket_hygiene", "Ticket hygiene", false},
	{"pr_hygiene", "Pull request hygiene", false},
	{"tests_hooks", "Test and hook handling, short of deleting tests", false},
	{"session_lifecycle", "Session lifecycle", false},
	{"test_fixtures", "Test fixtures and leftovers outside production", false},
	{"observability", "Observability and alert config, after a dry run", false},
	{"interpretation", "Interpreting an instruction", false},
	{DecisionKindOther, "Anything else", true},
}

// DecisionKinds returns the decision kind vocabulary in display order.
func DecisionKinds() []DecisionKindInfo {
	return append([]DecisionKindInfo(nil), decisionKinds...)
}

// LookupDecisionKind returns the kind with the given id.
func LookupDecisionKind(id string) (DecisionKindInfo, bool) {
	for _, kind := range decisionKinds {
		if string(kind.ID) == id {
			return kind, true
		}
	}
	return DecisionKindInfo{}, false
}

// AutonomyMode says how much a project's sessions decide on their own.
type AutonomyMode string

// AutonomyOff and AutonomyObserve are the supported autonomy modes.
const (
	// AutonomyOff keeps today's behaviour. It is the default.
	AutonomyOff AutonomyMode = "off"
	// AutonomyObserve keeps every call going to the person, and records the
	// kind of each call and, for kinds that are not reserved, what the session
	// would have decided on its own.
	AutonomyObserve AutonomyMode = "observe"
	// AutonomyOn lets sessions decide kinds the person has not reserved, act,
	// and record a decided entry the person can overrule. The person chooses
	// it in project settings or through project config.
	AutonomyOn AutonomyMode = "on"
)

// AutonomyConfig is a project's policy for which decisions belong to the
// person. Read when a call is raised and when standing instructions are built.
type AutonomyConfig struct {
	Mode AutonomyMode `json:"mode,omitempty" enum:"off,observe,on"`
	// Reserved lists the kinds the person keeps. Empty means the kinds that
	// are reserved by default.
	Reserved []DecisionKindID `json:"reserved,omitempty"`
}

// EffectiveMode returns the configured mode, or off when none is set.
func (c AutonomyConfig) EffectiveMode() AutonomyMode {
	if c.Mode == "" {
		return AutonomyOff
	}
	return c.Mode
}

// IsReserved reports whether the person keeps decisions of this kind. The
// catch-all kind is always reserved, so a decision that fits no named kind is
// never made without the person, whatever list the project sets.
func (c AutonomyConfig) IsReserved(id DecisionKindID) bool {
	if id == DecisionKindOther {
		return true
	}
	if len(c.Reserved) == 0 {
		kind, ok := LookupDecisionKind(string(id))
		return ok && kind.ReservedByDefault
	}
	for _, reserved := range c.Reserved {
		if reserved == id {
			return true
		}
	}
	return false
}

// Validate refuses unknown modes, unknown kinds and repeated kinds.
func (c AutonomyConfig) Validate() error {
	switch c.Mode {
	case "", AutonomyOff, AutonomyObserve, AutonomyOn:
	default:
		return fmt.Errorf("autonomy.mode: unknown mode %q", c.Mode)
	}
	seen := make(map[DecisionKindID]bool, len(c.Reserved))
	for _, id := range c.Reserved {
		if _, ok := LookupDecisionKind(string(id)); !ok {
			return fmt.Errorf("autonomy.reserved: unknown decision kind %q", id)
		}
		if seen[id] {
			return fmt.Errorf("autonomy.reserved: %q is listed twice", id)
		}
		seen[id] = true
	}
	return nil
}
