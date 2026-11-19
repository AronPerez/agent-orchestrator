package domain

import (
	"fmt"
	"strings"
)

// AutonomyInstructions renders a project's autonomy policy as a standing
// instructions section. It is empty in mode off, so projects that never opt
// in see exactly the instructions they saw before the policy existed.
func AutonomyInstructions(c AutonomyConfig) string {
	switch c.EffectiveMode() {
	case AutonomyObserve:
		return observeInstructions(c)
	case AutonomyOn:
		return onInstructions(c)
	default:
		return ""
	}
}

func observeInstructions(c AutonomyConfig) string {
	return "## Reserved Decisions (observe mode)\n\n" +
		"This project is trying out autonomy levels in observe mode. Nothing is decided on the human's behalf yet. " +
		"You still raise a call for every decision you would ask the human about today, and you also raise one before any action of a reserved kind. " +
		"Every call waits for the human's answer, and you follow that answer even when it differs from what you would have chosen.\n\n" +
		kindLists(c) +
		"- Pass `--kind <id>` on every call. Use `other` when no kind fits. `ao decision kinds` lists them again.\n" +
		"- Before any action of a reserved kind, raise a call, even if you used to take that kind of action on your own.\n" +
		"- For a kind that is not reserved, also say what you would have decided on your own: " +
		"`--would-decide <n> --why <reason> --undo <how the human could undo it>`. " +
		"Orchestrator keeps it beside the human's answer, so the human can see where your judgment matches theirs before trusting you to decide alone."
}

func onInstructions(c AutonomyConfig) string {
	return "## Reserved Decisions (on)\n\n" +
		"In this project you decide the kinds of decision the human has not reserved, and record each one so the human can review it and overrule it.\n\n" +
		kindLists(c) +
		"- Before any action of a reserved kind, raise a call with `--kind <id>` and wait for the answer.\n" +
		"- For a kind that is not reserved, decide, act, and record it straight away: " +
		"`ao decision decide --kind <id> --what <what you decided> --why <why> --undo <how to undo it>`, adding `--verify <how to tell it worked>` when there is a way to check. " +
		"Write the undo note so the human could follow it without you. It appears on the Decisions page under Decided for you.\n" +
		"- If the human overrules a decision, a message arrives that begins \"I overruled the decision you made for me\". Undo it as its note says, then record what happened with the `ao decision outcome` command it gives.\n" +
		"- When you are unsure which kind applies, or whether it can be undone, raise a call instead. `ao decision kinds` lists the kinds again."
}

func kindLists(c AutonomyConfig) string {
	var reserved, open strings.Builder
	for _, kind := range decisionKinds {
		line := fmt.Sprintf("- %s (`%s`)\n", kind.Label, kind.ID)
		if c.IsReserved(kind.ID) {
			reserved.WriteString(line)
		} else {
			open.WriteString(line)
		}
	}
	return "Reserved for the human:\n" + reserved.String() + "\n" +
		"Not reserved:\n" + open.String() + "\n"
}
