package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func seedDecisionSearch(t *testing.T) *sqlite.Store {
	t.Helper()
	store := newTestStore(t)
	at := time.UnixMilli(1_000_000).UTC()
	raised := func(id, title, reference string, at time.Time) domain.DecisionEntry {
		entry := decisionRaised("dent_"+id, "h_a", id, "w-1", "", "", at)
		var payload domain.DecisionRaisedPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		payload.Title = title
		payload.Context = "Tenants rotate their secrets."
		payload.Options = []domain.DecisionOption{
			{Label: "Vault", Consequence: "Keep the secret in managed storage.", Recommended: true},
			{Label: "Environment files", Consequence: "Keep one file for each tenant."},
		}
		payload.Reference = reference
		entry.Payload = mustDecisionJSON(payload)
		return entry
	}
	appendDecision(t, store, raised("dec_auth", "Which auth scheme should the proxy use?", "SKY-12", at), at)
	appendDecision(t, store, raised("dec_cadence", "How often should the report refresh?", "", at.Add(time.Second)), at.Add(time.Second))
	appendDecision(t, store, domain.DecisionEntry{
		ID: "dent_answer", Origin: "h_a", CallID: "dec_cadence", Kind: domain.DecisionKindAnswered,
		Payload:   mustDecisionJSON(domain.DecisionAnsweredPayload{Choice: domain.DecisionChoiceOther, Note: "Hourly, except weekends"}),
		WrittenAt: at.Add(2 * time.Second), AuthorKind: domain.DecisionAuthorPerson, AuthorSurface: domain.RequestSurfaceCLI,
	}, at.Add(2*time.Second))
	appendDecision(t, store, domain.DecisionEntry{
		ID: "dent_outcome", Origin: "h_a", CallID: "dec_cadence", Kind: domain.DecisionKindOutcome,
		Payload:   mustDecisionJSON(domain.DecisionOutcomePayload{Text: "Shipped the scheduler change"}),
		WrittenAt: at.Add(3 * time.Second), AuthorKind: domain.DecisionAuthorSession, AuthorSession: "w-1",
	}, at.Add(3*time.Second))
	return store
}

func TestSearchDecisionCalls(t *testing.T) {
	store := seedDecisionSearch(t)
	tests := []struct {
		query string
		want  string
	}{
		{query: "auth scheme", want: "dec_auth"},
		{query: "SKY-12", want: "dec_auth"},
		{query: "vault", want: "dec_auth,dec_cadence"},
		{query: "weekends", want: "dec_cadence"},
		{query: "scheduler", want: "dec_cadence"},
		{query: "refreshing reports", want: "dec_cadence"},
		{query: "sched", want: "dec_cadence"},
		{query: "auth zebra", want: "dec_auth"},
		{query: "zebra", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			calls, err := store.SearchDecisionCalls(context.Background(), tc.query, 20)
			if err != nil || decisionIDs(calls) != tc.want {
				t.Fatalf("SearchDecisionCalls(%q) = %q, %v; want %q", tc.query, decisionIDs(calls), err, tc.want)
			}
		})
	}
}

func TestSearchDecisionCallsTreatsPunctuationAsText(t *testing.T) {
	store := seedDecisionSearch(t)
	for _, query := range []string{`"auth" OR -proxy* (scheme)`, `NEAR(auth, 2)`, `auth:scheme^`, "🙂 auth", `"`} {
		if _, err := store.SearchDecisionCalls(context.Background(), query, 20); err != nil {
			t.Errorf("SearchDecisionCalls(%q): %v", query, err)
		}
	}
	for _, query := range []string{"", "   ", `"-*()`} {
		calls, err := store.SearchDecisionCalls(context.Background(), query, 20)
		if err != nil || calls != nil {
			t.Errorf("SearchDecisionCalls(%q) = %+v, %v; want nil, nil", query, calls, err)
		}
	}
}
