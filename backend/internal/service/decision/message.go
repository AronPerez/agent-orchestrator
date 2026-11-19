package decision

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DeliveryMessage is the text sent into a session for the call's standing
// answer or dismissal. fallback is true when it goes to the project's
// orchestrator because the owning session has ended.
func DeliveryMessage(call domain.DecisionCall, fallback bool) string {
	if call.StandingKind == domain.DecisionKindDismissed {
		return dismissalMessage(call, fallback)
	}
	if call.StandingKind == domain.DecisionKindOverruled {
		return overruleMessage(call, fallback)
	}
	return answerMessage(call, fallback)
}

// bindingLine tells the receiving session, and any session it relays the
// answer to, that a ledger answer needs no second confirmation.
const bindingLine = "This is my decision, recorded in the ledger. It binds you and any session it is passed to: act on it without asking me to confirm it again."

func answerMessage(call domain.DecisionCall, fallback bool) string {
	var message strings.Builder
	switch {
	case fallback:
		fmt.Fprintf(&message, "Decision on a call raised by %q, which has ended: %q\n", call.Raised.OwnerSessionName, call.Raised.Title)
	case call.Redelivery:
		fmt.Fprintf(&message, "Changed decision on your call %q\n", call.Raised.Title)
	default:
		fmt.Fprintf(&message, "Decision on your call %q\n", call.Raised.Title)
	}
	fmt.Fprintln(&message, domain.DecisionCallURL(call.ID))
	if call.Redelivery {
		fmt.Fprintln(&message, "This replaces my earlier answer.")
	}
	fmt.Fprintln(&message)
	if call.Choice == domain.DecisionChoiceOther {
		fmt.Fprintln(&message, "I chose something else. My note says what to do.")
	} else {
		fmt.Fprintf(&message, "I chose: %s\n", call.ChosenLabel())
	}
	if call.Note != "" {
		fmt.Fprintf(&message, "Note: %s\n", call.Note)
	}
	fmt.Fprintln(&message)
	fmt.Fprintln(&message, bindingLine)
	if fallback {
		fmt.Fprintln(&message, "Carry this out, or hand it to a worker. When it is done, record what happened in one line:")
	} else {
		fmt.Fprintln(&message, "Carry this out. When it is done, record what happened in one line:")
	}
	fmt.Fprintf(&message, "ao decision outcome %s --for-answer %s --text %q\n", call.ID, call.StandingEntryID, "<what happened>")
	return message.String()
}

func dismissalMessage(call domain.DecisionCall, fallback bool) string {
	var message strings.Builder
	if fallback {
		fmt.Fprintf(&message, "I dismissed a call raised by %q, which has ended: %q\n", call.Raised.OwnerSessionName, call.Raised.Title)
	} else {
		fmt.Fprintf(&message, "I dismissed your call %q without choosing.\n", call.Raised.Title)
	}
	fmt.Fprintf(&message, "%s\n\n", domain.DecisionCallURL(call.ID))
	if call.Note != "" {
		fmt.Fprintf(&message, "Note: %s\n\n", call.Note)
	}
	if fallback {
		fmt.Fprintln(&message, "Nothing needs to be carried out.")
	} else {
		fmt.Fprintln(&message, "Do not wait for an answer on it. If you still need one, raise a new call that covers what was missing.")
	}
	return message.String()
}

func overruleMessage(call domain.DecisionCall, fallback bool) string {
	var message strings.Builder
	if fallback {
		fmt.Fprintf(&message, "I overruled a decision made for me by %q, which has ended: %q\n", call.Raised.OwnerSessionName, call.Raised.Title)
	} else {
		fmt.Fprintf(&message, "I overruled the decision you made for me: %q\n", call.Raised.Title)
	}
	fmt.Fprintf(&message, "%s\n\n", domain.DecisionCallURL(call.ID))
	if call.Decided != nil && call.Decided.Undo != "" {
		fmt.Fprintf(&message, "Undo it: %s\n", call.Decided.Undo)
	}
	if call.Decided != nil && !call.Decided.CarriedOut {
		fmt.Fprintln(&message, "It was never confirmed as carried out. Check whether it happened before you undo anything.")
	}
	if call.Note != "" {
		fmt.Fprintf(&message, "Note: %s\n", call.Note)
	}
	fmt.Fprintln(&message)
	fmt.Fprintln(&message, bindingLine)
	if fallback {
		fmt.Fprintln(&message, "Undo it, or hand it to a worker. When it is done, record what happened in one line:")
	} else {
		fmt.Fprintln(&message, "Undo it as the note says. When it is done, record what happened in one line:")
	}
	fmt.Fprintf(&message, "ao decision outcome %s --for-answer %s --text %q\n", call.ID, call.StandingEntryID, "<what happened>")
	return message.String()
}
