package domain

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func originIDs(prefix string, from, to int64) []DecisionOriginID {
	var out []DecisionOriginID
	for seq := from; seq <= to; seq++ {
		out = append(out, DecisionOriginID{Seq: seq, ID: fmt.Sprintf("%s%d", prefix, seq)})
	}
	return out
}

func TestDecisionForkPoint(t *testing.T) {
	shared := originIDs("s", 1, 4)
	for _, tc := range []struct {
		name        string
		local, peer []DecisionOriginID
		want        int64
		found       bool
	}{
		{"equal length", append(shared, originIDs("c", 5, 7)...), append(shared, originIDs("o", 5, 7)...), 5, true},
		{"copy is longer", append(shared, originIDs("c", 5, 9)...), append(shared, originIDs("o", 5, 6)...), 5, true},
		{"original is longer", append(shared, originIDs("c", 5, 6)...), append(shared, originIDs("o", 5, 9)...), 5, true},
		{"a lagging peer is no evidence", append(shared, originIDs("c", 5, 6)...), shared, 0, false},
		{"a lagging peer that differs where it holds", append(shared, originIDs("c", 5, 9)...), append(originIDs("s", 1, 3), DecisionOriginID{Seq: 4, ID: "o4"}), 4, true},
		{"nothing differs", shared, append(shared, originIDs("o", 5, 6)...), 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := DecisionForkPoint(tc.local, tc.peer)
			if got != tc.want || found != tc.found {
				t.Fatalf("DecisionForkPoint = %d, %v; want %d, %v", got, found, tc.want, tc.found)
			}
		})
	}
}

func TestReissueDecisionPayloadKeepsOriginalBytes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"option":2,"note":"a < b & c"}`, `{"reissuedFrom":"dent_1","option":2,"note":"a < b & c"}`},
		{`  { "option" : 2 }`, `{"reissuedFrom":"dent_1", "option" : 2 }`},
		{`{}`, `{"reissuedFrom":"dent_1"}`},
		{` { } `, `{"reissuedFrom":"dent_1"}`},
	} {
		got, err := ReissueDecisionPayload(json.RawMessage(tc.in), "dent_1")
		if err != nil {
			t.Fatalf("ReissueDecisionPayload(%s): %v", tc.in, err)
		}
		if string(got) != tc.want {
			t.Fatalf("ReissueDecisionPayload(%s) = %s, want %s", tc.in, got, tc.want)
		}
		if from, _ := DecisionReissuedFrom(got); from != "dent_1" {
			t.Fatalf("DecisionReissuedFrom(%s) = %q", got, from)
		}
	}
	// A payload already re-issued once names the newest original.
	got, err := ReissueDecisionPayload(json.RawMessage(`{"reissuedFrom":"dent_0","option":1}`), "dent_1")
	if err != nil {
		t.Fatal(err)
	}
	if from, _ := DecisionReissuedFrom(got); from != "dent_1" {
		t.Fatalf("re-issued twice names %q, want dent_1: %s", from, got)
	}
	if _, err := ReissueDecisionPayload(json.RawMessage(`[1]`), "dent_1"); err == nil {
		t.Fatal("a payload that is not an object was re-issued")
	}
}

func TestBuildDecisionRepairEntries(t *testing.T) {
	stamp := func(ms int64) DecisionStamp { return DecisionStamp{WallMS: ms} }
	at := time.UnixMilli(1_000).UTC()
	fork := make([]DecisionEntry, 0, 5)
	for i := range 5 {
		fork = append(fork, DecisionEntry{
			ID: fmt.Sprintf("x%d", 7+i), Origin: "h_x", OriginSeq: int64(7 + i), Stamp: stamp(int64(100 + i)),
			CallID: "dec_1", Kind: DecisionKindAnswered, Payload: json.RawMessage(fmt.Sprintf(`{"option":%d}`, i+1)),
			WrittenAt: at.Add(time.Duration(i) * time.Second), AuthorKind: DecisionAuthorPerson, AuthorSurface: RequestSurfaceMobile,
		})
	}
	n := 0
	in := DecisionRepairInput{
		RepairID: "rep_1", Origin: "h_x", FromSeq: 7, NewOrigin: "h_y", StartSeq: 1, Fork: fork,
		Stamp: stamp(900), Now: at.Add(time.Hour), PairsPerEntry: 2,
		NewID: func() string { n++; return fmt.Sprintf("new%d", n) },
	}
	out, err := BuildDecisionRepairEntries(in)
	if err != nil {
		t.Fatal(err)
	}
	// Three repair entries (2+2+1 pairs) come first, then the five copies.
	if len(out) != 8 {
		t.Fatalf("got %d entries, want 8", len(out))
	}
	pairs := map[string]string{}
	for i, e := range out[:3] {
		if e.Kind != DecisionKindIdentityRepaired || e.Origin != "h_y" || e.OriginSeq != int64(i+1) || e.CallID != DecisionRepairCallID("rep_1") {
			t.Fatalf("repair entry %d = %+v", i, e)
		}
		var p DecisionIdentityRepairedPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.RepairID != "rep_1" || p.Origin != "h_x" || p.FromSeq != 7 || p.Part != i+1 || p.Parts != 3 {
			t.Fatalf("repair payload %d = %+v", i, p)
		}
		for _, pair := range p.Reissued {
			pairs[pair[0]] = pair[1]
		}
	}
	for i, e := range out[3:] {
		orig := fork[i]
		if e.Origin != "h_y" || e.OriginSeq != int64(4+i) || e.Stamp != orig.Stamp || !e.WrittenAt.Equal(orig.WrittenAt) ||
			e.CallID != orig.CallID || e.Kind != orig.Kind || e.AuthorKind != orig.AuthorKind || e.AuthorSurface != orig.AuthorSurface {
			t.Fatalf("copy %d = %+v, original %+v", i, e, orig)
		}
		want := fmt.Sprintf(`{"reissuedFrom":%q,"option":%d}`, orig.ID, i+1)
		if string(e.Payload) != want {
			t.Fatalf("copy %d payload = %s, want %s", i, e.Payload, want)
		}
		if pairs[orig.ID] != e.ID {
			t.Fatalf("repair entries map %s to %q, copy is %s", orig.ID, pairs[orig.ID], e.ID)
		}
		if err := ValidateDecisionSyncEntry(e); err != nil {
			t.Fatalf("copy %d does not sync: %v", i, err)
		}
	}
	if err := ValidateDecisionSyncEntry(out[0]); err != nil {
		t.Fatalf("repair entry does not sync: %v", err)
	}
}

func TestReduceTreatsAnOriginalAsItsReissuedCopy(t *testing.T) {
	raised := decisionEntry(t, "r1", 1, DecisionKindRaised, DecisionRaisedPayload{
		Title: "Pick one", Options: []DecisionOption{{Label: "A"}, {Label: "B"}}, OwnerSessionID: "project-1",
	})
	copyOf := func(id, from string, wall int64, kind DecisionEntryKind, payload any) DecisionEntry {
		e := decisionEntry(t, id, wall, kind, payload)
		reissued, err := ReissueDecisionPayload(e.Payload, from)
		if err != nil {
			t.Fatal(err)
		}
		e.Payload = reissued
		return e
	}
	answer := copyOf("a1-copy", "a1", 2, DecisionKindAnswered, DecisionAnsweredPayload{Option: 2, Choice: "B"})
	sent := decisionEntry(t, "d1", 3, DecisionKindDelivered, DecisionDeliveredPayload{ForEntry: "a1", Status: DecisionDeliveredSent, SessionID: "project-1"})

	call, ok := ReduceDecisionCall([]DecisionEntry{raised, answer, sent})
	if !ok || call.Delivery != DecisionDeliverySent || call.Redelivery || call.StandingEntryID != "a1-copy" {
		t.Fatalf("delivered original did not count for its copy: %+v", call)
	}
	call, _ = ReduceDecisionCall([]DecisionEntry{raised, answer})
	if call.Delivery != DecisionDeliveryPending {
		t.Fatalf("undelivered copy is %q, want pending", call.Delivery)
	}
	outcome := decisionEntry(t, "o1", 4, DecisionKindOutcome, DecisionOutcomePayload{ForEntry: "a1", Text: "done"})
	call, _ = ReduceDecisionCall([]DecisionEntry{raised, answer, sent, outcome})
	if call.State != DecisionStateDone {
		t.Fatalf("outcome for the original did not close its copy: state %q", call.State)
	}
}
