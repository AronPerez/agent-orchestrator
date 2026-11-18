package domain

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

func validDecisionRaise() DecisionRaisedPayload {
	return DecisionRaisedPayload{
		Title:   "Which auth scheme should the proxy use?",
		Context: "The connector calls the proxy for each tenant.",
		Options: []DecisionOption{
			{Label: "OAuth client credentials", Consequence: "One app registration per tenant.", Recommended: true},
			{Label: "Per-tenant API keys", Consequence: "Customers rotate keys by hand."},
		},
		MachineLabel:     "mac-mini",
		OwnerSessionID:   "w-1",
		OwnerSessionName: "w-1",
	}
}

func TestDecisionStampNextNeverGoesBackwards(t *testing.T) {
	s := DecisionStamp{WallMS: 1000, Counter: 2}
	for _, tc := range []struct {
		now  int64
		want DecisionStamp
	}{
		{now: 1500, want: DecisionStamp{WallMS: 1500}},
		{now: 1000, want: DecisionStamp{WallMS: 1000, Counter: 3}},
		{now: 400, want: DecisionStamp{WallMS: 1000, Counter: 3}},
	} {
		if got := s.Next(tc.now); got != tc.want {
			t.Errorf("Next(%d) = %+v, want %+v", tc.now, got, tc.want)
		}
	}
}

func TestCompareDecisionEntriesBreaksTiesByOriginThenID(t *testing.T) {
	stamp := DecisionStamp{WallMS: 5}
	a := DecisionEntry{ID: "dent_b", Origin: "h_a", Stamp: stamp}
	b := DecisionEntry{ID: "dent_a", Origin: "h_b", Stamp: stamp}
	c := DecisionEntry{ID: "dent_c", Origin: "h_a", Stamp: stamp}
	later := DecisionEntry{ID: "dent_0", Origin: "h_0", Stamp: DecisionStamp{WallMS: 5, Counter: 1}}
	entries := []DecisionEntry{later, c, b, a}
	sort.Slice(entries, func(i, j int) bool { return CompareDecisionEntries(entries[i], entries[j]) < 0 })

	want := []string{"dent_b", "dent_c", "dent_a", "dent_0"}
	for i, entry := range entries {
		if entry.ID != want[i] {
			t.Fatalf("entry %d = %q, want %q", i, entry.ID, want[i])
		}
	}
	if got := CompareDecisionEntries(a, a); got != 0 {
		t.Fatalf("CompareDecisionEntries(a, a) = %d, want 0", got)
	}
}

func TestValidateDecisionRaiseNamesTheBrokenRule(t *testing.T) {
	tests := []struct {
		name string
		rule string
		edit func(*DecisionRaisedPayload)
	}{
		{name: "title required", rule: "title_required", edit: func(p *DecisionRaisedPayload) { p.Title = "  " }},
		{name: "title one line", rule: "title_one_line", edit: func(p *DecisionRaisedPayload) { p.Title = "a\nb" }},
		{name: "title too long", rule: "title_too_long", edit: func(p *DecisionRaisedPayload) { p.Title = strings.Repeat("界", 121) }},
		{name: "context required", rule: "context_required", edit: func(p *DecisionRaisedPayload) { p.Context = "" }},
		{name: "context too long", rule: "context_too_long", edit: func(p *DecisionRaisedPayload) { p.Context = strings.Repeat("x", 1001) }},
		{name: "too few options", rule: "options_count", edit: func(p *DecisionRaisedPayload) { p.Options = p.Options[:1] }},
		{name: "too many options", rule: "options_count", edit: func(p *DecisionRaisedPayload) {
			p.Options = append(p.Options, p.Options[1], p.Options[1], p.Options[1])
		}},
		{name: "option label required", rule: "option_label_required", edit: func(p *DecisionRaisedPayload) { p.Options[1].Label = " " }},
		{name: "option label too long", rule: "option_label_too_long", edit: func(p *DecisionRaisedPayload) { p.Options[1].Label = strings.Repeat("x", 61) }},
		{name: "option consequence required", rule: "option_consequence_required", edit: func(p *DecisionRaisedPayload) { p.Options[1].Consequence = "" }},
		{name: "option consequence too long", rule: "option_consequence_too_long", edit: func(p *DecisionRaisedPayload) { p.Options[1].Consequence = strings.Repeat("x", 201) }},
		{name: "no recommendation", rule: "recommended_count", edit: func(p *DecisionRaisedPayload) { p.Options[0].Recommended = false }},
		{name: "two recommendations", rule: "recommended_count", edit: func(p *DecisionRaisedPayload) { p.Options[1].Recommended = true }},
		{name: "too many links", rule: "links_count", edit: func(p *DecisionRaisedPayload) { p.Links = make([]DecisionLink, 6) }},
		{name: "link label required", rule: "link_invalid", edit: func(p *DecisionRaisedPayload) { p.Links = []DecisionLink{{URL: "https://example.com"}} }},
		{name: "link label too long", rule: "link_label_too_long", edit: func(p *DecisionRaisedPayload) {
			p.Links = []DecisionLink{{Label: strings.Repeat("x", 121), URL: "https://example.com"}}
		}},
		{name: "link URL too long", rule: "link_url_too_long", edit: func(p *DecisionRaisedPayload) {
			p.Links = []DecisionLink{{Label: "Design", URL: "https://example.com/" + strings.Repeat("x", 2030)}}
		}},
		{name: "link scheme", rule: "link_invalid", edit: func(p *DecisionRaisedPayload) { p.Links = []DecisionLink{{Label: "x", URL: "file:///etc/passwd"}} }},
		{name: "reference too long", rule: "reference_too_long", edit: func(p *DecisionRaisedPayload) { p.Reference = strings.Repeat("x", 65) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := validDecisionRaise()
			tc.edit(&p)
			assertDecisionRule(t, ValidateDecisionRaise(p), tc.rule)
		})
	}
}

func TestValidateDecisionRaiseAcceptsTheLimits(t *testing.T) {
	p := validDecisionRaise()
	p.Title = strings.Repeat("界", 120)
	p.Context = strings.Repeat("x", 1000)
	p.Options = append(p.Options,
		DecisionOption{Label: "Third", Consequence: "Third consequence."},
		DecisionOption{Label: "Fourth", Consequence: "Fourth consequence."},
	)
	p.Links = []DecisionLink{{Label: strings.Repeat("x", 120), URL: "https://example.com/" + strings.Repeat("x", 2028)}}
	p.Reference = strings.Repeat("x", 64)
	if err := ValidateDecisionRaise(p); err != nil {
		t.Fatal(err)
	}
}

func TestValidateDecisionAnswerNoteAndOutcome(t *testing.T) {
	tests := []struct {
		name string
		rule string
		err  func() error
	}{
		{name: "choice invalid", rule: "choice_invalid", err: func() error {
			return ValidateDecisionAnswer(2, DecisionAnsweredPayload{Choice: "maybe"})
		}},
		{name: "option high", rule: "option_range", err: func() error {
			return ValidateDecisionAnswer(2, DecisionAnsweredPayload{Choice: DecisionChoiceOption, Option: 3})
		}},
		{name: "option zero", rule: "option_range", err: func() error {
			return ValidateDecisionAnswer(2, DecisionAnsweredPayload{Choice: DecisionChoiceOption})
		}},
		{name: "other needs note", rule: "note_required", err: func() error {
			return ValidateDecisionAnswer(2, DecisionAnsweredPayload{Choice: DecisionChoiceOther})
		}},
		{name: "answer note too long", rule: "note_too_long", err: func() error {
			return ValidateDecisionAnswer(2, DecisionAnsweredPayload{Choice: DecisionChoiceOption, Option: 2, Note: strings.Repeat("x", 1001)})
		}},
		{name: "note too long", rule: "note_too_long", err: func() error {
			return ValidateDecisionNote(strings.Repeat("x", 1001))
		}},
		{name: "outcome required", rule: "outcome_required", err: func() error {
			return ValidateDecisionOutcome(DecisionOutcomePayload{Text: " "})
		}},
		{name: "outcome one line", rule: "outcome_one_line", err: func() error {
			return ValidateDecisionOutcome(DecisionOutcomePayload{Text: "a\nb"})
		}},
		{name: "outcome too long", rule: "outcome_too_long", err: func() error {
			return ValidateDecisionOutcome(DecisionOutcomePayload{Text: strings.Repeat("x", 301)})
		}},
		{name: "outcome link invalid", rule: "outcome_link_invalid", err: func() error {
			return ValidateDecisionOutcome(DecisionOutcomePayload{Text: "Shipped", Link: "ftp://x"})
		}},
		{name: "outcome link too long", rule: "outcome_link_too_long", err: func() error {
			return ValidateDecisionOutcome(DecisionOutcomePayload{Text: "Shipped", Link: "https://example.com/" + strings.Repeat("x", 2030)})
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { assertDecisionRule(t, tc.err(), tc.rule) })
	}

	if err := ValidateDecisionAnswer(2, DecisionAnsweredPayload{Choice: DecisionChoiceOption, Option: 2}); err != nil {
		t.Fatalf("valid answer: %v", err)
	}
	if err := ValidateDecisionOutcome(DecisionOutcomePayload{Text: "Shipped in PR 412", Link: "https://example.com/pr/412"}); err != nil {
		t.Fatalf("valid outcome: %v", err)
	}
}

func TestDecisionCallURL(t *testing.T) {
	if got, want := DecisionCallURL("dec_1 2"), "ao://decisions/dec_1%202"; got != want {
		t.Fatalf("DecisionCallURL() = %q, want %q", got, want)
	}
}

func assertDecisionRule(t *testing.T, err error, want string) {
	t.Helper()
	var rule *DecisionRuleError
	if !errors.As(err, &rule) {
		t.Fatalf("error = %v, want *DecisionRuleError", err)
	}
	if rule.Rule != want {
		t.Fatalf("rule = %q, want %q (error: %v)", rule.Rule, want, err)
	}
}
