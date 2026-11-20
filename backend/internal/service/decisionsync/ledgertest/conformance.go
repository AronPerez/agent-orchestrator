package ledgertest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
)

var conformanceAt = time.UnixMilli(1_700_000_000_000).UTC()

// Received builds an entry as its origin wrote it, for feeding a ledger
// through ApplyDecisionEntries.
func Received(origin string, seq int64, callID string, kind domain.DecisionEntryKind, payload string) domain.DecisionEntry {
	return domain.DecisionEntry{
		ID:     fmt.Sprintf("dent_%s_%d", origin, seq),
		Origin: origin, OriginSeq: seq,
		Stamp:  domain.DecisionStamp{WallMS: conformanceAt.UnixMilli() + seq*1000},
		CallID: callID, Kind: kind, Payload: json.RawMessage(payload),
		WrittenAt:  conformanceAt.Add(time.Duration(seq) * time.Second),
		AuthorKind: domain.DecisionAuthorPerson, AuthorSurface: domain.RequestSurfaceCLI,
	}
}

// ReceivedRun is entries from..to of origin, each of an unknown kind.
func ReceivedRun(origin string, from, to int64, callID string) []domain.DecisionEntry {
	var out []domain.DecisionEntry
	for seq := from; seq <= to; seq++ {
		out = append(out, Received(origin, seq, callID, "future_kind", fmt.Sprintf(`{"n":%d}`, seq)))
	}
	return out
}

func receivedRaised(origin string, seq int64, callID string) domain.DecisionEntry {
	e := Raised(callID, "w-1", origin+"-label")
	out := Received(origin, seq, callID, domain.DecisionKindRaised, string(e.Payload))
	out.AuthorKind, out.AuthorSurface, out.AuthorSession = domain.DecisionAuthorSession, "", "w-1"
	return out
}

// RepairFork is a copy's own entries from..to under origin: a raised entry
// for callID first, then answers.
func RepairFork(origin string, from, to int64, callID string) []domain.DecisionEntry {
	var out []domain.DecisionEntry
	for seq := from; seq <= to; seq++ {
		e := Received(origin, seq, callID, domain.DecisionKindAnswered, fmt.Sprintf(`{"choice":"A","option":1,"note":"%d"}`, seq))
		if seq == from {
			e = receivedRaised(origin, seq, callID)
		}
		e.ID = fmt.Sprintf("dent_fork_%d", seq)
		out = append(out, e)
	}
	return out
}

// RepairEntries is what repairing fork writes under newOrigin, starting at
// sequence 1. pairsPerEntry 0 puts every pair in one part.
func RepairEntries(t *testing.T, repairID string, fork []domain.DecisionEntry, newOrigin string, pairsPerEntry int) []domain.DecisionEntry {
	t.Helper()
	n := 0
	out, err := domain.BuildDecisionRepairEntries(domain.DecisionRepairInput{
		RepairID: repairID, Origin: fork[0].Origin, FromSeq: fork[0].OriginSeq, NewOrigin: newOrigin,
		StartSeq: 1, Fork: fork, Stamp: domain.DecisionStamp{WallMS: conformanceAt.UnixMilli() + 1e6},
		Now: conformanceAt.Add(time.Hour), PairsPerEntry: pairsPerEntry,
		NewID: func() string { n++; return fmt.Sprintf("dent_%s_%s_%d", newOrigin, repairID, n) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// RunApplyConformance holds a Ledger implementation to the sync rules:
// verbatim storage, idempotence, gaps, forks, own-origin restore, invalid
// batches, reprojection, paging and held-back origins. newLedger returns an
// empty ledger for a machine with hostID.
func RunApplyConformance(t *testing.T, newLedger func(t *testing.T, hostID string) decisionsync.Ledger) {
	ctx := context.Background()
	apply := func(t *testing.T, l decisionsync.Ledger, peer string, entries []domain.DecisionEntry) domain.DecisionApplyResult {
		t.Helper()
		res, err := l.ApplyDecisionEntries(ctx, peer, entries, conformanceAt)
		if err != nil {
			t.Fatalf("ApplyDecisionEntries: %v", err)
		}
		return res
	}
	vector := func(t *testing.T, l decisionsync.Ledger) domain.DecisionVector {
		t.Helper()
		v, err := l.DecisionVector(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	all := func(t *testing.T, l decisionsync.Ledger) []domain.DecisionEntry {
		t.Helper()
		var out []domain.DecisionEntry
		have := domain.DecisionVector{}
		for {
			page, more, err := l.ListDecisionEntriesAfter(ctx, have, 500, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range page {
				have[e.Origin] = e.OriginSeq
			}
			out = append(out, page...)
			if !more {
				return out
			}
		}
	}

	stopped := func(t *testing.T, l decisionsync.Ledger, origin string) bool {
		t.Helper()
		heads, err := l.DecisionHeads(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return heads[origin].Stopped
	}

	t.Run("StoresVerbatim", func(t *testing.T) {
		l := newLedger(t, "h_self")
		payload := `{"note":"<&> é \u00e9","zz":{"b":1,  "a":[1,2.50]},"unknown":true}`
		e := Received("h_b", 1, "dec_1", "future_kind", payload)
		e.Stamp = domain.DecisionStamp{WallMS: 1234, Counter: 7}
		if res := apply(t, l, "h_b", []domain.DecisionEntry{e}); res.Applied != 1 {
			t.Fatalf("applied = %d", res.Applied)
		}
		got := all(t, l)
		if len(got) != 1 || string(got[0].Payload) != payload || !domain.SameDecisionEntry(got[0], e) {
			t.Fatalf("stored = %+v, want %+v", got, e)
		}
	})

	t.Run("IsIdempotent", func(t *testing.T) {
		l := newLedger(t, "h_self")
		batch := []domain.DecisionEntry{receivedRaised("h_b", 1, "dec_1"), Received("h_b", 2, "dec_1", domain.DecisionKindAnswered, `{"choice":"option","option":1}`)}
		apply(t, l, "h_b", batch)
		res := apply(t, l, "h_b", batch)
		if res.Applied != 0 || res.Duplicates != 2 || len(res.Gaps)+len(res.Conflicts) != 0 {
			t.Fatalf("second apply = %+v", res)
		}
		if v := vector(t, l); v["h_b"] != 2 {
			t.Fatalf("vector = %v", v)
		}
	})

	t.Run("StopsAtGap", func(t *testing.T) {
		l := newLedger(t, "h_self")
		apply(t, l, "h_b", ReceivedRun("h_a", 1, 2, "dec_1"))
		batch := append(append(ReceivedRun("h_a", 3, 3, "dec_1"), ReceivedRun("h_a", 5, 5, "dec_1")...), ReceivedRun("h_a", 6, 6, "dec_1")...)
		res := apply(t, l, "h_b", batch)
		if res.Applied != 1 || len(res.Gaps) != 1 || res.Gaps[0] != (domain.DecisionSyncGap{Origin: "h_a", Have: 3, Got: 5}) {
			t.Fatalf("result = %+v", res)
		}
		if v := vector(t, l); v["h_a"] != 3 {
			t.Fatalf("vector = %v", v)
		}
	})

	t.Run("RecordsForkOnce", func(t *testing.T) {
		l := newLedger(t, "h_self")
		apply(t, l, "h_b", ReceivedRun("h_a", 1, 2, "dec_1"))
		fork := ReceivedRun("h_a", 2, 3, "dec_1")
		fork[0].ID = "dent_fork_2"
		res := apply(t, l, "h_peer", fork)
		if res.Applied != 0 || len(res.Conflicts) != 1 {
			t.Fatalf("fork = %+v", res)
		}
		c := res.Conflicts[0]
		if c.Origin != "h_a" || c.OriginSeq != 2 || c.LocalEntryID != "dent_h_a_2" || c.RemoteEntryID != "dent_fork_2" || c.PeerHostID != "h_peer" {
			t.Fatalf("conflict = %+v", c)
		}
		if again := apply(t, l, "h_peer", fork); len(again.Conflicts)+again.Applied != 0 {
			t.Fatalf("repeat = %+v", again)
		}
		if later := apply(t, l, "h_b", ReceivedRun("h_a", 3, 4, "dec_1")); later.Applied != 0 {
			t.Fatalf("stopped origin applied %d", later.Applied)
		}
		if other := apply(t, l, "h_b", ReceivedRun("h_b", 1, 2, "dec_2")); other.Applied != 2 {
			t.Fatalf("other origin applied %d", other.Applied)
		}
		heads, err := l.DecisionHeads(ctx)
		if err != nil || !heads["h_a"].Stopped || heads["h_b"].Stopped || heads["h_a"].ID != "dent_h_a_2" {
			t.Fatalf("heads = %+v, %v", heads, err)
		}
	})

	t.Run("SameIDDifferentContentIsConflict", func(t *testing.T) {
		l := newLedger(t, "h_self")
		apply(t, l, "h_b", ReceivedRun("h_a", 1, 1, "dec_1"))
		changed := ReceivedRun("h_a", 1, 1, "dec_1")
		changed[0].Payload = json.RawMessage(`{"n": 1}`)
		if res := apply(t, l, "h_b", changed); len(res.Conflicts) != 1 || res.Duplicates != 0 {
			t.Fatalf("result = %+v", res)
		}
	})

	t.Run("RestoresOwnOrigin", func(t *testing.T) {
		l := newLedger(t, "h_self")
		apply(t, l, "h_b", ReceivedRun("h_self", 1, 3, "dec_1"))
		if res := apply(t, l, "h_b", ReceivedRun("h_self", 4, 5, "dec_1")); res.Applied != 2 {
			t.Fatalf("restore = %+v", res)
		}
		other := ReceivedRun("h_self", 3, 3, "dec_1")
		other[0].ID = "dent_other"
		if res := apply(t, l, "h_b", other); len(res.Conflicts) != 1 {
			t.Fatalf("rewrite = %+v", res)
		}
	})

	t.Run("RejectsInvalidBatch", func(t *testing.T) {
		l := newLedger(t, "h_self")
		batch := ReceivedRun("h_a", 1, 3, "dec_1")
		batch[2].Payload = json.RawMessage(`[]`)
		if _, err := l.ApplyDecisionEntries(ctx, "h_b", batch, conformanceAt); !errors.Is(err, domain.ErrDecisionEntryInvalid) {
			t.Fatalf("err = %v", err)
		}
		if v := vector(t, l); len(v) != 0 {
			t.Fatalf("vector = %v, want nothing stored", v)
		}
	})

	t.Run("ReprojectsCall", func(t *testing.T) {
		l := newLedger(t, "h_self")
		if res := apply(t, l, "h_b", []domain.DecisionEntry{Received("h_b", 1, "dec_1", domain.DecisionKindAnswered, `{"choice":"option","option":1}`)}); len(res.Calls) != 0 {
			t.Fatalf("call before raised = %+v", res.Calls)
		}
		res := apply(t, l, "h_a", []domain.DecisionEntry{receivedRaised("h_a", 1, "dec_1")})
		if len(res.Calls) != 1 || res.Calls[0].State != domain.DecisionStateInProgress || res.Calls[0].Delivery != domain.DecisionDeliveryPending {
			t.Fatalf("calls = %+v", res.Calls)
		}
	})

	t.Run("Pages", func(t *testing.T) {
		l := newLedger(t, "h_self")
		for _, origin := range []string{"h_c", "h_a", "h_b"} {
			for from := int64(1); from <= 400; from += 100 {
				apply(t, l, "h_x", ReceivedRun(origin, from, from+99, "dec_"+origin))
			}
		}
		have := domain.DecisionVector{}
		var sizes []int
		for {
			page, more, err := l.ListDecisionEntriesAfter(ctx, have, 500, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range page {
				if e.OriginSeq != have[e.Origin]+1 {
					t.Fatalf("out of order at %s:%d", e.Origin, e.OriginSeq)
				}
				have[e.Origin] = e.OriginSeq
			}
			sizes = append(sizes, len(page))
			if !more || len(sizes) > 5 {
				break
			}
		}
		if fmt.Sprint(sizes) != "[500 500 200]" {
			t.Fatalf("page sizes = %v", sizes)
		}

		big := newLedger(t, "h_self")
		pad := strings.Repeat("x", 40<<10)
		apply(t, big, "h_x", []domain.DecisionEntry{
			Received("h_a", 1, "dec_big", "future_kind", `{"p":"`+pad+`"}`),
			Received("h_a", 2, "dec_big", "future_kind", `{"p":"`+pad+`"}`),
			Received("h_a", 3, "dec_big", "future_kind", `{"p":"`+pad+`"}`),
		})
		if page, more, _ := big.ListDecisionEntriesAfter(ctx, nil, 500, 100<<10); len(page) != 2 || !more {
			t.Fatalf("byte-capped first page = %d, more %v", len(page), more)
		}
		if page, more, _ := big.ListDecisionEntriesAfter(ctx, domain.DecisionVector{"h_a": 2}, 500, 100<<10); len(page) != 1 || more {
			t.Fatalf("byte-capped second page = %d, more %v", len(page), more)
		}
		if page, _, _ := big.ListDecisionEntriesAfter(ctx, nil, 500, 10); len(page) != 1 {
			t.Fatalf("tiny byte cap page = %d, want at least one", len(page))
		}
	})

	t.Run("HoldsBackConflictedOrigin", func(t *testing.T) {
		l := newLedger(t, "h_self")
		apply(t, l, "h_x", ReceivedRun("h_a", 1, 4, "dec_1"))
		apply(t, l, "h_x", ReceivedRun("h_b", 1, 2, "dec_2"))
		fork := ReceivedRun("h_a", 3, 3, "dec_1")
		fork[0].ID = "dent_fork"
		apply(t, l, "h_peer", fork)
		page, more, err := l.ListDecisionEntriesAfter(ctx, nil, 500, 1<<20)
		if err != nil || more {
			t.Fatalf("more=%v err=%v", more, err)
		}
		got := make([]string, 0, len(page))
		for _, e := range page {
			got = append(got, fmt.Sprintf("%s:%d", e.Origin, e.OriginSeq))
		}
		if strings.Join(got, ",") != "h_b:1,h_b:2" {
			t.Fatalf("page = %v", got)
		}
	})

	t.Run("RepairMovesForkOutAndResumes", func(t *testing.T) {
		l := newLedger(t, "h_b")
		apply(t, l, "h_c", ReceivedRun("h_x", 1, 2, "dec_s"))
		fork := RepairFork("h_x", 3, 4, "dec_f")
		apply(t, l, "h_c", fork)
		if c, _ := l.CheckDecisionHeads(ctx, "h_o", domain.DecisionHeads{"h_x": {Seq: 3, ID: "dent_o_3"}}, conformanceAt); len(c) != 1 {
			t.Fatalf("fork not noticed: %+v", c)
		}
		repair := RepairEntries(t, "rep_1", fork, "h_y", 1)
		if res := apply(t, l, "h_c", repair[:1]); res.Applied != 1 {
			t.Fatalf("first part applied = %d", res.Applied)
		}
		if v := vector(t, l); v["h_x"] != 4 || !stopped(t, l, "h_x") {
			t.Fatalf("one of two parts moved the fork: vector %v", v)
		}
		res := apply(t, l, "h_c", repair[1:])
		if v := vector(t, l); v["h_x"] != 2 || v["h_y"] != 4 || stopped(t, l, "h_x") {
			t.Fatalf("after repair vector %v stopped %v", v, stopped(t, l, "h_x"))
		}
		var moved *domain.DecisionCall
		for i := range res.Calls {
			if res.Calls[i].ID == "dec_f" {
				moved = &res.Calls[i]
			}
		}
		if moved == nil || moved.Origin != "h_y" {
			t.Fatalf("repaired call = %+v", moved)
		}
		original := ReceivedRun("h_x", 3, 4, "dec_o")
		for i := range original {
			original[i].ID = fmt.Sprintf("dent_o_%d", 3+i)
		}
		if res := apply(t, l, "h_o", original); res.Applied != 2 || len(res.Conflicts) != 0 {
			t.Fatalf("original history after repair = %+v", res)
		}
	})

	t.Run("RepairLeavingAHoleKeepsQuarantine", func(t *testing.T) {
		l := newLedger(t, "h_b")
		apply(t, l, "h_c", ReceivedRun("h_x", 1, 1, "dec_s"))
		fork := RepairFork("h_x", 2, 4, "dec_f")
		apply(t, l, "h_c", fork)
		if c, _ := l.CheckDecisionHeads(ctx, "h_o", domain.DecisionHeads{"h_x": {Seq: 2, ID: "dent_o_2"}}, conformanceAt); len(c) != 1 {
			t.Fatalf("fork not noticed: %+v", c)
		}
		// The repair lists 2 and 3 only; 4 stays and leaves a hole at 2.
		apply(t, l, "h_c", RepairEntries(t, "rep_1", fork[:2], "h_y", 0))
		if !stopped(t, l, "h_x") {
			t.Fatal("an origin with a hole in its history resumed")
		}
	})

	t.Run("RepairedOriginalsNeverReturn", func(t *testing.T) {
		l := newLedger(t, "h_b")
		apply(t, l, "h_c", ReceivedRun("h_x", 1, 2, "dec_s"))
		fork := RepairFork("h_x", 3, 4, "dec_f")
		// The repair arrives before the fork itself, then an older machine
		// sends the fork: it is never stored, and its head is no conflict.
		apply(t, l, "h_c", RepairEntries(t, "rep_1", fork, "h_y", 0))
		res := apply(t, l, "h_old", fork)
		if res.Applied != 0 || len(res.Conflicts) != 0 {
			t.Fatalf("repaired fork applied: %+v", res)
		}
		if v := vector(t, l); v["h_x"] != 2 {
			t.Fatalf("vector %v", v)
		}
		if c, err := l.CheckDecisionHeads(ctx, "h_old", domain.DecisionHeads{"h_x": {Seq: 2, ID: fork[1].ID}}, conformanceAt); err != nil || len(c) != 0 {
			t.Fatalf("a repaired head is a conflict: %+v, %v", c, err)
		}
	})

	t.Run("ChecksHeads", func(t *testing.T) {
		l := newLedger(t, "h_self")
		apply(t, l, "h_x", ReceivedRun("h_a", 1, 3, "dec_1"))
		conflicts, err := l.CheckDecisionHeads(ctx, "h_peer", domain.DecisionHeads{
			"h_a": {Seq: 3, ID: "dent_other"},
			"h_b": {Seq: 9, ID: "dent_unknown"},
		}, conformanceAt)
		if err != nil || len(conflicts) != 1 || conflicts[0].OriginSeq != 3 || conflicts[0].LocalEntryID != "dent_h_a_3" {
			t.Fatalf("conflicts = %+v, %v", conflicts, err)
		}
		if again, _ := l.CheckDecisionHeads(ctx, "h_peer", domain.DecisionHeads{"h_a": {Seq: 2, ID: "dent_h_a_2"}}, conformanceAt); len(again) != 0 {
			t.Fatalf("matching head recorded %+v", again)
		}
	})
}
