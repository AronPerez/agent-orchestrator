package domain

import (
	"encoding/json"
	"math/rand"
	"testing"
	"time"
)

func decisionEntry(t *testing.T, id string, wallMS int64, kind DecisionEntryKind, payload any) DecisionEntry {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return DecisionEntry{
		ID:            id,
		Origin:        "h_a",
		CallID:        "dec_1",
		Kind:          kind,
		Payload:       raw,
		Stamp:         DecisionStamp{WallMS: wallMS},
		WrittenAt:     timeFromMillis(wallMS),
		AuthorKind:    DecisionAuthorPerson,
		AuthorSurface: RequestSurfaceDesktop,
	}
}

func timeFromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func TestReduceDecisionCall(t *testing.T) {
	raised := decisionEntry(t, "r", 1, DecisionKindRaised, validDecisionRaise())
	pick := func(id string, wall int64, option int) DecisionEntry {
		return decisionEntry(t, id, wall, DecisionKindAnswered, DecisionAnsweredPayload{
			Choice: DecisionChoiceOption, Option: option, Note: "n" + id,
		})
	}
	sent := func(id string, wall int64, forEntry string) DecisionEntry {
		return decisionEntry(t, id, wall, DecisionKindDelivered, DecisionDeliveredPayload{
			ForEntry: forEntry, Status: DecisionDeliveredSent, SessionID: "w-1", SessionName: "w-1",
		})
	}
	dismissed := decisionEntry(t, "d", 3, DecisionKindDismissed, DecisionDismissedPayload{Note: "not now"})
	outcome := decisionEntry(t, "o", 6, DecisionKindOutcome, DecisionOutcomePayload{Text: "Shipped in PR 412"})
	withdrawn := decisionEntry(t, "w", 2, DecisionKindWithdrawn, struct{}{})
	noSession := decisionEntry(t, "ns", 3, DecisionKindDelivered, DecisionDeliveredPayload{
		ForEntry: "a1", Status: DecisionDeliveredNoSession,
	})
	retired := decisionEntry(t, "rt", 8, DecisionKindRetired, DecisionRetiredPayload{Retired: true})
	unretired := decisionEntry(t, "urt", 9, DecisionKindRetired, DecisionRetiredPayload{Retired: false})
	unknown := DecisionEntry{
		ID: "u", Origin: "h_a", CallID: "dec_1", Kind: "asked",
		Payload: json.RawMessage(`{"question":"why"}`), Stamp: DecisionStamp{WallMS: 4}, WrittenAt: timeFromMillis(4),
	}

	tests := []struct {
		name    string
		entries []DecisionEntry
		check   func(*testing.T, DecisionCall)
	}{
		{
			name:    "raised",
			entries: []DecisionEntry{raised},
			check: func(t *testing.T, call DecisionCall) {
				if call.State != DecisionStateNeedsYou || call.Delivery != DecisionDeliveryNone || call.ID != "dec_1" || call.Origin != "h_a" {
					t.Fatalf("call = %+v", call)
				}
			},
		},
		{
			name:    "answered",
			entries: []DecisionEntry{raised, pick("a1", 2, 2)},
			check: func(t *testing.T, call DecisionCall) {
				if call.State != DecisionStateInProgress || call.Option != 2 || call.Note != "na1" || call.StandingEntryID != "a1" || call.Delivery != DecisionDeliveryPending || call.Redelivery || call.AnswerSurface != RequestSurfaceDesktop || call.ChosenLabel() != "Per-tenant API keys" {
					t.Fatalf("call = %+v", call)
				}
			},
		},
		{
			name:    "delivered",
			entries: []DecisionEntry{raised, pick("a1", 2, 2), sent("s1", 3, "a1")},
			check: func(t *testing.T, call DecisionCall) {
				if call.Delivery != DecisionDeliverySent || call.DeliveredTo != "w-1" {
					t.Fatalf("call = %+v", call)
				}
			},
		},
		{
			name:    "changed pick needs redelivery",
			entries: []DecisionEntry{raised, pick("a1", 2, 2), sent("s1", 3, "a1"), pick("a2", 4, 1)},
			check: func(t *testing.T, call DecisionCall) {
				if call.State != DecisionStateInProgress || call.Option != 1 || call.StandingEntryID != "a2" || call.Delivery != DecisionDeliveryPending || !call.Redelivery {
					t.Fatalf("call = %+v", call)
				}
			},
		},
		{
			name:    "dismissed",
			entries: []DecisionEntry{raised, dismissed},
			check: func(t *testing.T, call DecisionCall) {
				if call.State != DecisionStateDismissed || call.Note != "not now" || call.Choice != "" || call.Delivery != DecisionDeliveryPending {
					t.Fatalf("call = %+v", call)
				}
			},
		},
		{
			name:    "answer reopens dismissal",
			entries: []DecisionEntry{raised, dismissed, pick("a1", 4, 1)},
			check: func(t *testing.T, call DecisionCall) {
				if call.State != DecisionStateInProgress {
					t.Fatalf("state = %q", call.State)
				}
			},
		},
		{
			name:    "outcome closes",
			entries: []DecisionEntry{raised, pick("a1", 2, 1), outcome},
			check: func(t *testing.T, call DecisionCall) {
				if call.State != DecisionStateDone || call.Outcome != "Shipped in PR 412" || call.ChangedAfterClose {
					t.Fatalf("call = %+v", call)
				}
			},
		},
		{
			name:    "answer after outcome is kept",
			entries: []DecisionEntry{raised, pick("a1", 2, 1), outcome, pick("a2", 7, 2)},
			check: func(t *testing.T, call DecisionCall) {
				if call.State != DecisionStateDone || !call.ChangedAfterClose || call.StandingEntryID != "a2" || call.Delivery != DecisionDeliveryPending {
					t.Fatalf("call = %+v", call)
				}
			},
		},
		{
			name:    "withdrawn",
			entries: []DecisionEntry{raised, withdrawn},
			check: func(t *testing.T, call DecisionCall) {
				if call.State != DecisionStateWithdrawn || call.ChangedAfterClose {
					t.Fatalf("call = %+v", call)
				}
			},
		},
		{
			name:    "answer after withdrawal is kept",
			entries: []DecisionEntry{raised, withdrawn, pick("a1", 5, 1)},
			check: func(t *testing.T, call DecisionCall) {
				if call.State != DecisionStateWithdrawn || !call.ChangedAfterClose || call.Delivery != DecisionDeliveryPending {
					t.Fatalf("call = %+v", call)
				}
			},
		},
		{
			name:    "no session delivery",
			entries: []DecisionEntry{raised, pick("a1", 2, 1), noSession},
			check: func(t *testing.T, call DecisionCall) {
				if call.Delivery != DecisionDeliveryNoSession {
					t.Fatalf("delivery = %q", call.Delivery)
				}
			},
		},
		{
			name:    "retire then undo",
			entries: []DecisionEntry{raised, pick("a1", 2, 1), outcome, retired, unretired},
			check: func(t *testing.T, call DecisionCall) {
				if call.Retired || call.LastStamp.WallMS != 9 {
					t.Fatalf("call = %+v", call)
				}
			},
		},
		{
			name:    "unknown kind advances stamp",
			entries: []DecisionEntry{raised, unknown},
			check: func(t *testing.T, call DecisionCall) {
				if call.State != DecisionStateNeedsYou || call.LastStamp.WallMS != 4 {
					t.Fatalf("call = %+v", call)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for seed := int64(0); seed < 6; seed++ {
				entries := append([]DecisionEntry(nil), tc.entries...)
				rand.New(rand.NewSource(seed)).Shuffle(len(entries), func(i, j int) {
					entries[i], entries[j] = entries[j], entries[i]
				})
				call, ok := ReduceDecisionCall(entries)
				if !ok {
					t.Fatalf("seed %d: no call", seed)
				}
				tc.check(t, call)
			}
		})
	}
}

func TestReduceDecisionCallNeedsARaisedEntry(t *testing.T) {
	entry := decisionEntry(t, "a1", 2, DecisionKindAnswered, DecisionAnsweredPayload{
		Choice: DecisionChoiceOption, Option: 1,
	})
	if _, ok := ReduceDecisionCall([]DecisionEntry{entry}); ok {
		t.Fatal("ReduceDecisionCall returned a call without a raised entry")
	}
}

func TestDecisionStateLabelsAndSearchText(t *testing.T) {
	labels := map[DecisionCallState]string{
		DecisionStateNeedsYou:   "Needs you",
		DecisionStateInProgress: "In progress",
		DecisionStateDone:       "Done",
		DecisionStateDismissed:  "Dismissed",
		DecisionStateWithdrawn:  "Withdrawn",
	}
	for state, want := range labels {
		if got := state.Label(); got != want {
			t.Errorf("%q.Label() = %q, want %q", state, got, want)
		}
	}

	raised := validDecisionRaise()
	raised.Reference = "SKY-12"
	call := DecisionCall{Raised: raised, Note: "Rotate monthly", Outcome: "Shipped in PR 412"}
	want := "Which auth scheme should the proxy use?\n" +
		"The connector calls the proxy for each tenant.\n" +
		"OAuth client credentials\n" +
		"Per-tenant API keys\n" +
		"SKY-12\n" +
		"Rotate monthly\n" +
		"Shipped in PR 412"
	if got := call.SearchText(); got != want {
		t.Fatalf("SearchText() = %q, want %q", got, want)
	}
}
