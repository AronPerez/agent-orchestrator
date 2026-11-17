package domain

import (
	"reflect"
	"testing"
)

func TestDeriveDecisionStream(t *testing.T) {
	sessions := []SessionRecord{
		{ID: "orch", Kind: KindOrchestrator},
		{ID: "epic", ParentSessionID: "orch", Kind: KindWorker, DisplayName: "Checkout epic"},
		{ID: "sub", ParentSessionID: "epic", Kind: KindWorker},
		{ID: "w-1", ParentSessionID: "sub", Kind: KindWorker},
		{ID: "w-2", ParentSessionID: "epic", Kind: KindWorker},
		{ID: "lone", ParentSessionID: "orch", Kind: KindWorker},
		{ID: "solo", Kind: KindWorker},
		{ID: "boss", Kind: KindWorker},
		{ID: "hand", ParentSessionID: "boss", Kind: KindWorker},
	}
	tests := map[SessionID]SessionID{
		"w-2":  "epic",
		"w-1":  "epic",
		"sub":  "epic",
		"epic": "epic",
		"hand": "boss",
		"boss": "boss",
		"lone": "",
		"solo": "",
		"orch": "",
		"gone": "",
	}
	for raiser, want := range tests {
		t.Run(string(raiser), func(t *testing.T) {
			got, ok := DeriveDecisionStream(raiser, sessions)
			if got.ID != want || ok != (want != "") {
				t.Fatalf("DeriveDecisionStream(%q) = %q, %v; want %q, %v", raiser, got.ID, ok, want, want != "")
			}
		})
	}
}

func TestDeriveDecisionStreamSurvivesBrokenLineage(t *testing.T) {
	sessions := []SessionRecord{
		{ID: "a", ParentSessionID: "b", Kind: KindWorker},
		{ID: "b", ParentSessionID: "a", Kind: KindWorker},
		{ID: "self", ParentSessionID: "self", Kind: KindWorker},
		{ID: "orphan", ParentSessionID: "missing", Kind: KindWorker},
	}
	for _, tc := range []struct {
		raiser SessionID
		want   SessionID
	}{
		{raiser: "a", want: "b"},
		{raiser: "self"},
		{raiser: "orphan"},
	} {
		got, ok := DeriveDecisionStream(tc.raiser, sessions)
		if got.ID != tc.want || ok != (tc.want != "") {
			t.Fatalf("DeriveDecisionStream(%q) = %q, %v; want %q, %v", tc.raiser, got.ID, ok, tc.want, tc.want != "")
		}
	}
}

func TestAssignDecisionStreams(t *testing.T) {
	streams := []DecisionStream{{Origin: "h_a", SessionID: "epic", Name: "Checkout epic"}}
	calls := []DecisionCall{
		{ID: "early", Origin: "h_a", Raised: DecisionRaisedPayload{OwnerSessionID: "epic"}},
		{ID: "old", Origin: "h_a", Raised: DecisionRaisedPayload{OwnerSessionID: "w-2", StreamSessionID: "epic", StreamName: "Checkout"}},
		{ID: "other", Origin: "h_b", Raised: DecisionRaisedPayload{OwnerSessionID: "epic"}},
		{ID: "lone", Origin: "h_a", Raised: DecisionRaisedPayload{OwnerSessionID: "lone"}},
	}
	original := append([]DecisionCall(nil), calls...)
	got := AssignDecisionStreams(calls, streams)
	want := []struct {
		id     string
		stream SessionID
		name   string
	}{
		{id: "early", stream: "epic", name: "Checkout epic"},
		{id: "old", stream: "epic", name: "Checkout epic"},
		{id: "other"},
		{id: "lone"},
	}
	for i, expected := range want {
		if got[i].ID != expected.id || got[i].Raised.StreamSessionID != expected.stream || got[i].Raised.StreamName != expected.name {
			t.Errorf("call %d = %+v, want id=%q stream=%q name=%q", i, got[i], expected.id, expected.stream, expected.name)
		}
	}
	if !reflect.DeepEqual(calls, original) {
		t.Fatalf("AssignDecisionStreams mutated input:\n got: %+v\nwant: %+v", calls, original)
	}
}
