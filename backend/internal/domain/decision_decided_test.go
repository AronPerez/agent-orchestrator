package domain

import (
	"strings"
	"testing"
)

func validDecided() DecisionDecidedPayload {
	return DecisionDecidedPayload{
		What: "Closed the duplicate pull request #12", Why: "It repeats #11, which is further along.",
		Undo: "Reopen #12; its branch is kept.", Kind: "pr_hygiene",
		MachineLabel: "mac-mini", OwnerSessionID: "w-1", OwnerSessionName: "w-1",
	}
}

func TestValidateDecisionDecidedNamesTheBrokenRule(t *testing.T) {
	tests := []struct {
		rule string
		edit func(*DecisionDecidedPayload)
	}{
		{"what_required", func(p *DecisionDecidedPayload) { p.What = " " }},
		{"what_one_line", func(p *DecisionDecidedPayload) { p.What = "a\nb" }},
		{"what_too_long", func(p *DecisionDecidedPayload) { p.What = strings.Repeat("x", 121) }},
		{"why_required", func(p *DecisionDecidedPayload) { p.Why = "" }},
		{"why_too_long", func(p *DecisionDecidedPayload) { p.Why = strings.Repeat("x", 1001) }},
		{"undo_required", func(p *DecisionDecidedPayload) { p.Undo = "" }},
		{"undo_too_long", func(p *DecisionDecidedPayload) { p.Undo = strings.Repeat("x", 301) }},
		{"verify_too_long", func(p *DecisionDecidedPayload) { p.Verify = strings.Repeat("x", 301) }},
		{"kind_required", func(p *DecisionDecidedPayload) { p.Kind = "" }},
		{"kind_unknown", func(p *DecisionDecidedPayload) { p.Kind = "lunch" }},
		{"links_count", func(p *DecisionDecidedPayload) { p.Links = make([]DecisionLink, 6) }},
		{"link_invalid", func(p *DecisionDecidedPayload) { p.Links = []DecisionLink{{Label: "x", URL: "file:///etc"}} }},
		{"reference_too_long", func(p *DecisionDecidedPayload) { p.Reference = strings.Repeat("x", 65) }},
	}
	for _, tc := range tests {
		t.Run(tc.rule, func(t *testing.T) {
			p := validDecided()
			tc.edit(&p)
			assertDecisionRule(t, ValidateDecisionDecided(p), tc.rule)
		})
	}
	if err := ValidateDecisionDecided(validDecided()); err != nil {
		t.Fatalf("valid payload refused: %v", err)
	}
}

func TestValidateDecisionOverrule(t *testing.T) {
	assertDecisionRule(t, ValidateDecisionOverrule(" "), "overrule_note_required")
	assertDecisionRule(t, ValidateDecisionOverrule(strings.Repeat("x", 1001)), "note_too_long")
	if err := ValidateDecisionOverrule("Keep it open; I want both reviewed."); err != nil {
		t.Fatal(err)
	}
}

func TestReduceDecidedCall(t *testing.T) {
	decided := validDecided()
	decided.Verify = "The PR list shows one open PR for the ticket."
	decided.OutsidePolicy = DecisionOutsidePolicyReserved
	entries := []DecisionEntry{decisionEntry(t, "dent_d", 1, DecisionKindDecided, decided)}
	call, ok := ReduceDecisionCall(entries)
	if !ok {
		t.Fatal("a decided entry alone must form a call")
	}
	if call.State != DecisionStateDecided || call.State.Label() != "Decided for you" {
		t.Fatalf("state = %q (%q)", call.State, call.State.Label())
	}
	if call.Raised.Title != decided.What || call.Raised.Context != decided.Why || call.Raised.Kind != "pr_hygiene" ||
		call.Raised.OwnerSessionID != "w-1" || len(call.Raised.Options) != 0 {
		t.Fatalf("raised view = %+v", call.Raised)
	}
	if call.Decided == nil || call.Decided.Undo != decided.Undo || call.Decided.Verify != decided.Verify || call.Decided.OutsidePolicy != DecisionOutsidePolicyReserved {
		t.Fatalf("decided = %+v", call.Decided)
	}
	if call.Delivery != DecisionDeliveryNone || call.StandingEntryID != "" {
		t.Fatalf("an unchallenged decision has nothing to deliver: %+v", call)
	}
}

func TestReduceOverruledDecidedCall(t *testing.T) {
	decided := decisionEntry(t, "dent_d", 1, DecisionKindDecided, validDecided())
	overruled := decisionEntry(t, "dent_o", 2, DecisionKindOverruled, DecisionOverruledPayload{Note: "Keep both open."})
	delivered := decisionEntry(t, "dent_v", 3, DecisionKindDelivered, DecisionDeliveredPayload{ForEntry: "dent_o", Status: DecisionDeliveredSent, SessionID: "w-1"})
	outcome := decisionEntry(t, "dent_x", 4, DecisionKindOutcome, DecisionOutcomePayload{ForEntry: "dent_o", Text: "Reopened #12."})

	// Any arrival order reduces the same way.
	for _, order := range [][]DecisionEntry{
		{decided, overruled},
		{overruled, decided},
	} {
		call, _ := ReduceDecisionCall(order)
		if call.State != DecisionStateInProgress || call.StandingKind != DecisionKindOverruled ||
			call.StandingEntryID != "dent_o" || call.Note != "Keep both open." || call.Delivery != DecisionDeliveryPending {
			t.Fatalf("overruled call = %+v", call)
		}
	}
	call, _ := ReduceDecisionCall([]DecisionEntry{outcome, delivered, overruled, decided})
	if call.State != DecisionStateDone || call.Outcome != "Reopened #12." || call.Delivery != DecisionDeliverySent {
		t.Fatalf("done call = %+v", call)
	}
}

func TestReduceIgnoresOverruleOnAnOrdinaryCall(t *testing.T) {
	raised := decisionEntry(t, "dent_r", 1, DecisionKindRaised, validDecisionRaise())
	overruled := decisionEntry(t, "dent_o", 2, DecisionKindOverruled, DecisionOverruledPayload{Note: "n"})
	call, _ := ReduceDecisionCall([]DecisionEntry{raised, overruled})
	if call.State != DecisionStateNeedsYou || call.StandingEntryID != "" {
		t.Fatalf("an overrule must not affect an ordinary call: %+v", call)
	}
}

func TestAutonomyOnModeAndCatchAll(t *testing.T) {
	if err := (AutonomyConfig{Mode: AutonomyOn}).Validate(); err != nil {
		t.Fatalf("on mode refused: %v", err)
	}
	custom := AutonomyConfig{Mode: AutonomyObserve, Reserved: []DecisionKindID{"production", "design"}}
	if !custom.IsReserved(DecisionKindOther) {
		t.Fatal("other must stay reserved under a custom list")
	}
}

func TestReduceOutcomeClosesAStandingDecidedCall(t *testing.T) {
	decided := decisionEntry(t, "dent_d", 1, DecisionKindDecided, validDecided())
	outcome := decisionEntry(t, "dent_x", 2, DecisionKindOutcome, DecisionOutcomePayload{ForEntry: "dent_d", Text: "Not carried out."})
	call, _ := ReduceDecisionCall([]DecisionEntry{decided, outcome})
	if call.State != DecisionStateDone || call.Outcome != "Not carried out." {
		t.Fatalf("call = %+v", call)
	}
}

func TestReduceCarriedOutConfirmsADecidedCall(t *testing.T) {
	decided := decisionEntry(t, "dent_d", 1, DecisionKindDecided, validDecided())
	call, _ := ReduceDecisionCall([]DecisionEntry{decided})
	if call.Decided.CarriedOut {
		t.Fatal("a recorded decision is not carried out until its session says so")
	}
	carried := decisionEntry(t, "dent_c", 2, DecisionKindCarriedOut, DecisionCarriedOutPayload{ForEntry: "dent_d"})
	call, _ = ReduceDecisionCall([]DecisionEntry{carried, decided})
	if !call.Decided.CarriedOut || call.State != DecisionStateDecided || call.Decided.CarriedOutAt.IsZero() {
		t.Fatalf("carried out call = %+v decided = %+v", call, call.Decided)
	}
	stray := decisionEntry(t, "dent_s", 3, DecisionKindCarriedOut, DecisionCarriedOutPayload{ForEntry: "dent_other"})
	call, _ = ReduceDecisionCall([]DecisionEntry{decided, stray})
	if call.Decided.CarriedOut {
		t.Fatal("a confirmation naming another entry must be ignored")
	}
	raised := decisionEntry(t, "dent_r", 1, DecisionKindRaised, validDecisionRaise())
	call, _ = ReduceDecisionCall([]DecisionEntry{raised, decisionEntry(t, "dent_c", 2, DecisionKindCarriedOut, DecisionCarriedOutPayload{ForEntry: "dent_r"})})
	if call.Decided != nil || call.State != DecisionStateNeedsYou {
		t.Fatalf("a confirmation on an ordinary call must be ignored: %+v", call)
	}
}

func TestReduceLateConfirmationReopensADecisionClosedUnconfirmed(t *testing.T) {
	decided := decisionEntry(t, "dent_d", 1, DecisionKindDecided, validDecided())
	closed := decisionEntry(t, "dent_x", 2, DecisionKindOutcome, DecisionOutcomePayload{ForEntry: "dent_d", Text: "Not carried out."})
	late := decisionEntry(t, "dent_c", 3, DecisionKindCarriedOut, DecisionCarriedOutPayload{ForEntry: "dent_d"})
	call, _ := ReduceDecisionCall([]DecisionEntry{late, closed, decided})
	if call.State != DecisionStateDecided || !call.Decided.CarriedOut || !call.ChangedAfterClose || call.Outcome != "" {
		t.Fatalf("reopened call = %+v decided = %+v", call, call.Decided)
	}
	// Confirmed first, then closed by the person: an ordinary close.
	early := decisionEntry(t, "dent_c", 2, DecisionKindCarriedOut, DecisionCarriedOutPayload{ForEntry: "dent_d"})
	personClose := decisionEntry(t, "dent_x", 3, DecisionKindOutcome, DecisionOutcomePayload{ForEntry: "dent_d", Text: "Reviewed."})
	call, _ = ReduceDecisionCall([]DecisionEntry{decided, early, personClose})
	if call.State != DecisionStateDone || call.ChangedAfterClose {
		t.Fatalf("confirmed-then-closed call = %+v", call)
	}
}

func TestValidateDecisionDecidedKey(t *testing.T) {
	p := validDecided()
	p.Key = strings.Repeat("k", 65)
	assertDecisionRule(t, ValidateDecisionDecided(p), "key_too_long")
	p.Key = "close pr\n12"
	assertDecisionRule(t, ValidateDecisionDecided(p), "key_one_line")
	p.Key = "close-pr-12"
	if err := ValidateDecisionDecided(p); err != nil {
		t.Fatal(err)
	}
	call, _ := ReduceDecisionCall([]DecisionEntry{decisionEntry(t, "dent_d", 1, DecisionKindDecided, p)})
	if call.Decided.Key != "close-pr-12" {
		t.Fatalf("key lost: %+v", call.Decided)
	}
}
