package agy

import (
	"os"
	"strings"
)

// StandingRuleHolds reports whether workspacePath holds AO's always-on rule
// with exactly these standing instructions, as installStandingRule writes
// them. A Chat on a server that loads workspace rules then already has them in
// its system prompt. A missing, foreign, unreadable or different rule reports
// false, so the instructions are sent rather than lost.
func StandingRuleHolds(workspacePath, instructions string) bool {
	instructions = strings.TrimSpace(instructions)
	if strings.TrimSpace(workspacePath) == "" || instructions == "" {
		return false
	}
	data, err := os.ReadFile(agyStandingRulePath(workspacePath)) //nolint:gosec // path built from caller-owned workspace dir
	if err != nil {
		return false
	}
	return string(data) == agyStandingRuleFront+"<!-- "+agyStandingRuleMark+" -->\n\n"+instructions+"\n"
}
