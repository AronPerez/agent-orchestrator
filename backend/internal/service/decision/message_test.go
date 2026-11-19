package decision

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDeliveryMessageForAnAnswer(t *testing.T) {
	got := DeliveryMessage(messageCall(), false)
	want := "Decision on your call \"Which auth scheme should the proxy use?\"\n" +
		"ao://decisions/dec_1\n\n" +
		"I chose: OAuth client credentials\n" +
		"Note: Rotate monthly.\n\n" +
		"This is my decision, recorded in the ledger. It binds you and any session it is passed to: act on it without asking me to confirm it again.\n" +
		"Carry this out. When it is done, record what happened in one line:\n" +
		"ao decision outcome dec_1 --for-answer dent_answer --text \"<what happened>\"\n"
	if got != want {
		t.Fatalf("DeliveryMessage() =\n%q\nwant\n%q", got, want)
	}
}

func TestDeliveryMessageVariants(t *testing.T) {
	tests := []struct {
		name     string
		call     domain.DecisionCall
		fallback bool
		contains []string
		excludes []string
	}{
		{
			name: "no note", call: func() domain.DecisionCall { c := messageCall(); c.Note = ""; return c }(),
			contains: []string{"I chose: OAuth client credentials\n\nThis is my decision"}, excludes: []string{"Note:"},
		},
		{
			name: "other", call: func() domain.DecisionCall {
				c := messageCall()
				c.Choice, c.Option, c.Note = domain.DecisionChoiceOther, 0, "Use the vault."
				return c
			}(),
			contains: []string{"I chose something else. My note says what to do.\nNote: Use the vault."},
		},
		{
			name: "redelivery", call: func() domain.DecisionCall { c := messageCall(); c.Redelivery = true; return c }(),
			contains: []string{"Changed decision on your call \"Which auth scheme should the proxy use?\"\nao://decisions/dec_1\nThis replaces my earlier answer.\n\n"},
		},
		{
			name: "fallback", call: messageCall(), fallback: true,
			contains: []string{
				"Decision on a call raised by \"Proxy worker\", which has ended: \"Which auth scheme should the proxy use?\"",
				"Carry this out, or hand it to a worker.",
				"It binds you and any session it is passed to",
			},
			excludes: []string{"your call"},
		},
		{
			name: "fallback redelivery", call: func() domain.DecisionCall { c := messageCall(); c.Redelivery = true; return c }(), fallback: true,
			contains: []string{"Decision on a call raised by", "This replaces my earlier answer."}, excludes: []string{"Changed decision"},
		},
		{
			name: "dismissed", call: func() domain.DecisionCall {
				c := messageCall()
				c.State, c.StandingKind, c.Choice, c.Option, c.Note = domain.DecisionStateDismissed, domain.DecisionKindDismissed, "", 0, "Not enough to go on."
				return c
			}(),
			contains: []string{
				"I dismissed your call \"Which auth scheme should the proxy use?\" without choosing.\nao://decisions/dec_1\n\nNote: Not enough to go on.\n\nDo not wait for an answer on it.",
			},
			excludes: []string{"ao decision outcome", "I chose", "It binds you"},
		},
		{
			name: "dismissed fallback", call: func() domain.DecisionCall {
				c := messageCall()
				c.State, c.StandingKind, c.Choice, c.Option, c.Note = domain.DecisionStateDismissed, domain.DecisionKindDismissed, "", 0, "Not enough to go on."
				return c
			}(), fallback: true,
			contains: []string{"I dismissed a call raised by \"Proxy worker\", which has ended:", "Nothing needs to be carried out."},
			excludes: []string{"Do not wait"},
		},
		{
			name: "dismissed after close", call: func() domain.DecisionCall {
				c := messageCall()
				c.State, c.StandingKind, c.Choice, c.Option, c.Note = domain.DecisionStateWithdrawn, domain.DecisionKindDismissed, "", 0, "No longer needed."
				return c
			}(),
			contains: []string{"I dismissed your call", "Do not wait for an answer"},
			excludes: []string{"ao decision outcome", "I chose"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeliveryMessage(tt.call, tt.fallback)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("message missing %q:\n%s", want, got)
				}
			}
			for _, unwanted := range tt.excludes {
				if strings.Contains(got, unwanted) {
					t.Errorf("message contains %q:\n%s", unwanted, got)
				}
			}
		})
	}
}

func messageCall() domain.DecisionCall {
	return domain.DecisionCall{
		ID: "dec_1", State: domain.DecisionStateInProgress,
		Raised: domain.DecisionRaisedPayload{
			Title: "Which auth scheme should the proxy use?", OwnerSessionName: "Proxy worker",
			Options: []domain.DecisionOption{
				{Label: "OAuth client credentials"},
				{Label: "Per-tenant API keys"},
			},
		},
		StandingEntryID: "dent_answer", StandingKind: domain.DecisionKindAnswered,
		Choice: domain.DecisionChoiceOption, Option: 1, Note: "Rotate monthly.",
	}
}
