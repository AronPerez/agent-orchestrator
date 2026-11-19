package domain

import "fmt"

const (
	// DefaultAutomatedSessionMaxConcurrent bounds unattended work per project
	// when a caller opts into automated starts without choosing its own cap.
	DefaultAutomatedSessionMaxConcurrent = 3
	// DefaultAutomatedSessionMaxStartsPerPass prevents one polling/scheduling
	// pass from consuming every available slot at once.
	DefaultAutomatedSessionMaxStartsPerPass = 2
	// MaxAutomatedSessionLimit keeps malformed config from turning a bounded
	// admission policy into an impractically large allocation or query.
	MaxAutomatedSessionLimit = 100
)

// AutomatedSessionLimits is the shared admission vocabulary for unattended
// session producers. Tracker intake uses it first; the deterministic backlog
// scheduler can reuse the same limits and RemainingStarts calculation.
//
// Zero fields mean the safe defaults, not unlimited.
type AutomatedSessionLimits struct {
	MaxConcurrent    int `json:"maxConcurrent,omitempty" minimum:"0" maximum:"100" description:"Maximum live sessions counted by this producer. Zero selects the default of 3."`
	MaxStartsPerPass int `json:"maxStartsPerPass,omitempty" minimum:"0" maximum:"100" description:"Maximum session start attempts in one polling or scheduling pass. Zero selects the default of 2."`
}

// WithDefaults fills omitted fields while preserving explicit positive caps.
func (l AutomatedSessionLimits) WithDefaults() AutomatedSessionLimits {
	if l.MaxConcurrent == 0 {
		l.MaxConcurrent = DefaultAutomatedSessionMaxConcurrent
	}
	if l.MaxStartsPerPass == 0 {
		l.MaxStartsPerPass = DefaultAutomatedSessionMaxStartsPerPass
	}
	return l
}

// Validate rejects negative and unreasonably large caps. Zero is valid because
// it selects the safe default.
func (l AutomatedSessionLimits) Validate() error {
	if l.MaxConcurrent < 0 || l.MaxConcurrent > MaxAutomatedSessionLimit {
		return fmt.Errorf("maxConcurrent must be between 0 and %d", MaxAutomatedSessionLimit)
	}
	if l.MaxStartsPerPass < 0 || l.MaxStartsPerPass > MaxAutomatedSessionLimit {
		return fmt.Errorf("maxStartsPerPass must be between 0 and %d", MaxAutomatedSessionLimit)
	}
	return nil
}

// RemainingStarts returns how many start attempts remain in this pass. active
// is the live-session snapshot taken before the pass; attempted includes both
// successful and failed starts so a broken launcher cannot turn a large source
// backlog into an unbounded retry sweep. Negative inputs are treated as zero.
func (l AutomatedSessionLimits) RemainingStarts(active, attempted int) int {
	l = l.WithDefaults()
	if active < 0 {
		active = 0
	}
	if attempted < 0 {
		attempted = 0
	}
	byConcurrent := l.MaxConcurrent - active - attempted
	byPass := l.MaxStartsPerPass - attempted
	if byConcurrent < byPass {
		byPass = byConcurrent
	}
	if byPass < 0 {
		return 0
	}
	return byPass
}
