package domain

import (
	"fmt"
	"strings"
)

// AutonomyInstructions renders a project's autonomy policy as a standing
// instructions section. It is empty in mode off, so projects that never opt
// in see exactly the instructions they saw before the policy existed.
func AutonomyInstructions(c AutonomyConfig) string {
	if c.EffectiveMode() != AutonomyObserve {
		return ""
	}
	var reserved, open strings.Builder
	for _, kind := range decisionKinds {
		line := fmt.Sprintf("- %s (`%s`)\n", kind.Label, kind.ID)
		if c.IsReserved(kind.ID) {
			reserved.WriteString(line)
		} else {
			open.WriteString(line)
		}
	}
	return "## Reserved Decisions (observe mode)\n\n" +
		"This project is trying out autonomy levels in observe mode. Nothing is decided on the human's behalf yet. " +
		"You still raise a call for every decision you would ask the human about today, and you also raise one before any action of a reserved kind. " +
		"Every call waits for the human's answer, and you follow that answer even when it differs from what you would have chosen.\n\n" +
		"Reserved for the human:\n" + reserved.String() + "\n" +
		"Not reserved:\n" + open.String() + "\n" +
		"- Pass `--kind <id>` on every call. Use `other` when no kind fits. `ao decision kinds` lists them again.\n" +
		"- Before any action of a reserved kind, raise a call, even if you used to take that kind of action on your own.\n" +
		"- For a kind that is not reserved, also say what you would have decided on your own: " +
		"`--would-decide <n> --why <reason> --undo <how the human could undo it>`. " +
		"Orchestrator keeps it beside the human's answer, so the human can see where your judgment matches theirs before trusting you to decide alone."
}
