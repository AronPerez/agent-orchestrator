package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

var syncBase = time.UnixMilli(1_700_000_000_000).UTC()

// syncEntry is a received entry with ordering already assigned by its origin.
func syncEntry(origin string, seq int64, callID string, kind domain.DecisionEntryKind, payload string) domain.DecisionEntry {
	return domain.DecisionEntry{
		ID:     fmt.Sprintf("dent_%s_%d", origin, seq),
		Origin: origin, OriginSeq: seq,
		Stamp:  domain.DecisionStamp{WallMS: syncBase.UnixMilli() + seq*1000},
		CallID: callID, Kind: kind, Payload: json.RawMessage(payload),
		WrittenAt:  syncBase.Add(time.Duration(seq) * time.Second),
		AuthorKind: domain.DecisionAuthorPerson, AuthorSurface: domain.RequestSurfaceCLI,
	}
}

func syncRaised(origin string, seq int64, callID string) domain.DecisionEntry {
	e := decisionRaised("", origin, callID, "w-1", "", "", syncBase.Add(time.Duration(seq)*time.Second))
	out := syncEntry(origin, seq, callID, domain.DecisionKindRaised, string(e.Payload))
	out.AuthorKind, out.AuthorSurface, out.AuthorSession = domain.DecisionAuthorSession, "", "w-1"
	return out
}

func syncAnswer(origin string, seq int64, callID string) domain.DecisionEntry {
	return syncEntry(origin, seq, callID, domain.DecisionKindAnswered, `{"choice":"option","option":1}`)
}

func syncRun(origin string, from, to int64, callID string) []domain.DecisionEntry {
	var out []domain.DecisionEntry
	for seq := from; seq <= to; seq++ {
		out = append(out, syncEntry(origin, seq, callID, "future_kind", fmt.Sprintf(`{"n":%d}`, seq)))
	}
	return out
}

func applySync(t *testing.T, s *sqlite.Store, peer string, entries []domain.DecisionEntry) domain.DecisionApplyResult {
	t.Helper()
	res, err := s.ApplyDecisionEntries(context.Background(), peer, entries, syncBase)
	if err != nil {
		t.Fatalf("ApplyDecisionEntries: %v", err)
	}
	return res
}

func TestApplyDecisionEntriesStoresVerbatim(t *testing.T) {
	s := newTestStore(t)
	payload := `{"note":"<&> é \u00e9","zz":{"b":1,  "a":[1,2]},"unknown":true}`
	e := syncEntry("h_b", 1, "dec_1", "future_kind", payload)
	e.Stamp = domain.DecisionStamp{WallMS: 1234, Counter: 7}
	if res := applySync(t, s, "h_b", []domain.DecisionEntry{e}); res.Applied != 1 {
		t.Fatalf("applied = %d, want 1", res.Applied)
	}
	got, err := s.ListDecisionEntries(context.Background(), "dec_1")
	if err != nil || len(got) != 1 {
		t.Fatalf("entries = %v, %v", got, err)
	}
	if string(got[0].Payload) != payload {
		t.Fatalf("payload = %q, want %q", got[0].Payload, payload)
	}
	if !domain.SameDecisionEntry(got[0], e) {
		t.Fatalf("stored entry = %+v, want %+v", got[0], e)
	}
}

func TestApplyDecisionEntriesIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	batch := append([]domain.DecisionEntry{syncRaised("h_b", 1, "dec_1")}, syncAnswer("h_b", 2, "dec_1"))
	applySync(t, s, "h_b", batch)
	res := applySync(t, s, "h_b", batch)
	if res.Applied != 0 || res.Duplicates != 2 || len(res.Gaps) != 0 || len(res.Conflicts) != 0 {
		t.Fatalf("second apply = %+v, want 0 applied, 2 duplicates", res)
	}
	v, err := s.DecisionVector(context.Background())
	if err != nil || v["h_b"] != 2 {
		t.Fatalf("vector = %v, %v", v, err)
	}
}

func TestApplyDecisionEntriesStopsAtGap(t *testing.T) {
	s := newTestStore(t)
	applySync(t, s, "h_b", syncRun("h_a", 1, 2, "dec_1"))
	batch := []domain.DecisionEntry{syncRun("h_a", 3, 3, "dec_1")[0], syncRun("h_a", 5, 5, "dec_1")[0], syncRun("h_a", 6, 6, "dec_1")[0]}
	res := applySync(t, s, "h_b", batch)
	if res.Applied != 1 || len(res.Gaps) != 1 || res.Gaps[0] != (domain.DecisionSyncGap{Origin: "h_a", Have: 3, Got: 5}) {
		t.Fatalf("result = %+v, want a:3 applied and gap {a 3 5}", res)
	}
	v, _ := s.DecisionVector(context.Background())
	if v["h_a"] != 3 {
		t.Fatalf("vector = %v, want h_a:3", v)
	}
}

func TestApplyDecisionEntriesRecordsForkOnce(t *testing.T) {
	s := newTestStore(t)
	applySync(t, s, "h_b", syncRun("h_a", 1, 2, "dec_1"))
	fork := syncRun("h_a", 2, 3, "dec_1")
	fork[0].ID = "dent_fork_2"
	res := applySync(t, s, "h_peer", fork)
	if res.Applied != 0 || len(res.Conflicts) != 1 {
		t.Fatalf("fork result = %+v, want one conflict and nothing applied", res)
	}
	c := res.Conflicts[0]
	if c.Origin != "h_a" || c.OriginSeq != 2 || c.LocalEntryID != "dent_h_a_2" || c.RemoteEntryID != "dent_fork_2" || c.PeerHostID != "h_peer" {
		t.Fatalf("conflict = %+v", c)
	}
	if again := applySync(t, s, "h_peer", fork); len(again.Conflicts) != 0 || again.Applied != 0 {
		t.Fatalf("repeat = %+v, want nothing recorded or applied", again)
	}
	if later := applySync(t, s, "h_b", syncRun("h_a", 3, 4, "dec_1")); later.Applied != 0 {
		t.Fatalf("later h_a batch applied %d, want 0 (origin stopped)", later.Applied)
	}
	if other := applySync(t, s, "h_b", syncRun("h_b", 1, 2, "dec_2")); other.Applied != 2 {
		t.Fatalf("h_b batch applied %d, want 2", other.Applied)
	}
	got, _ := s.ListDecisionEntries(context.Background(), "dec_1")
	for _, e := range got {
		if e.ID == "dent_fork_2" {
			t.Fatal("forked entry was stored")
		}
	}
	conflicts, err := s.ListDecisionSyncConflicts(context.Background())
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("conflicts = %v, %v", conflicts, err)
	}
	heads, err := s.DecisionHeads(context.Background())
	if err != nil || !heads["h_a"].Stopped || heads["h_b"].Stopped {
		t.Fatalf("heads = %+v, %v", heads, err)
	}
}

func TestApplyDecisionEntriesRestoresOwnOrigin(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		appendDecision(t, s, decisionAnswered(fmt.Sprintf("dent_self_%d", i), "h_self", "dec_1", 1, syncBase), syncBase)
	}
	if res := applySync(t, s, "h_b", syncRun("h_self", 4, 5, "dec_1")); res.Applied != 2 {
		t.Fatalf("restore applied %d, want 2", res.Applied)
	}
	other := syncRun("h_self", 3, 3, "dec_1")
	if res := applySync(t, s, "h_b", other); len(res.Conflicts) != 1 {
		t.Fatalf("own-origin rewrite = %+v, want a conflict", res)
	}
}

func TestApplyDecisionEntriesRejectsInvalidBatch(t *testing.T) {
	s := newTestStore(t)
	batch := syncRun("h_a", 1, 3, "dec_1")
	batch[2].Payload = json.RawMessage(`[]`)
	_, err := s.ApplyDecisionEntries(context.Background(), "h_b", batch, syncBase)
	if !errors.Is(err, domain.ErrDecisionEntryInvalid) {
		t.Fatalf("err = %v, want ErrDecisionEntryInvalid", err)
	}
	if v, _ := s.DecisionVector(context.Background()); len(v) != 0 {
		t.Fatalf("vector = %v, want nothing stored", v)
	}
}

func TestApplyDecisionEntriesReprojectsCall(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	applySync(t, s, "h_a", []domain.DecisionEntry{syncRaised("h_a", 1, "dec_1")})
	head, err := s.LatestSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res := applySync(t, s, "h_b", []domain.DecisionEntry{syncAnswer("h_b", 1, "dec_1")})
	if len(res.Calls) != 1 || res.Calls[0].ID != "dec_1" {
		t.Fatalf("calls = %+v", res.Calls)
	}
	call, ok, err := s.GetDecisionCall(ctx, "dec_1")
	if err != nil || !ok {
		t.Fatalf("call = %v %v", ok, err)
	}
	if call.State != domain.DecisionStateInProgress || call.Delivery != domain.DecisionDeliveryPending {
		t.Fatalf("call state/delivery = %s/%s, want in_progress/pending", call.State, call.Delivery)
	}
	evs, err := s.EventsAfter(ctx, head, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.Type == cdc.EventDecisionUpdated && strings.Contains(string(e.Payload), "dec_1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no decision_updated after apply: %+v", evs)
	}
}

func TestApplyRaisedAfterAnswerBuildsCall(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if res := applySync(t, s, "h_b", []domain.DecisionEntry{syncAnswer("h_b", 1, "dec_1")}); len(res.Calls) != 0 {
		t.Fatalf("calls before raised = %+v", res.Calls)
	}
	if _, ok, _ := s.GetDecisionCall(ctx, "dec_1"); ok {
		t.Fatal("call exists before its raised entry")
	}
	res := applySync(t, s, "h_a", []domain.DecisionEntry{syncRaised("h_a", 1, "dec_1")})
	if len(res.Calls) != 1 || res.Calls[0].State != domain.DecisionStateInProgress {
		t.Fatalf("calls after raised = %+v", res.Calls)
	}
}

func TestListDecisionEntriesAfterPages(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, origin := range []string{"h_c", "h_a", "h_b"} {
		for from := int64(1); from <= 400; from += 100 {
			applySync(t, s, "h_x", syncRun(origin, from, from+99, "dec_"+origin))
		}
	}
	have := domain.DecisionVector{}
	var pages [][]domain.DecisionEntry
	for {
		page, more, err := s.ListDecisionEntriesAfter(ctx, have, 500, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, page)
		for _, e := range page {
			if e.OriginSeq != have[e.Origin]+1 {
				t.Fatalf("page out of order at %s:%d after %d", e.Origin, e.OriginSeq, have[e.Origin])
			}
			have[e.Origin] = e.OriginSeq
		}
		if !more {
			break
		}
		if len(pages) > 5 {
			t.Fatal("paging did not end")
		}
	}
	if len(pages) != 3 || len(pages[0]) != 500 || len(pages[1]) != 500 || len(pages[2]) != 200 {
		t.Fatalf("page sizes = %d pages", len(pages))
	}
	if pages[0][0].Origin != "h_a" || pages[2][199].Origin != "h_c" {
		t.Fatal("origins not ascending")
	}

	big := newTestStore(t)
	pad := strings.Repeat("x", 400<<10)
	var batch []domain.DecisionEntry
	for seq := int64(1); seq <= 3; seq++ {
		batch = append(batch, syncEntry("h_a", seq, "dec_big", "future_kind", `{"p":"`+pad[:len(pad)-int(seq)]+`"}`))
	}
	// 400 KiB payloads exceed the received-entry cap, so store them as local
	// history via the same verbatim path a restore would use.
	for _, e := range batch {
		if len(e.Payload) <= domain.MaxDecisionSyncPayloadBytes {
			t.Fatal("test payload unexpectedly under the cap")
		}
	}
	for _, e := range batch {
		appendDecision(t, big, domain.DecisionEntry{ID: e.ID, Origin: "h_a", CallID: e.CallID, Kind: e.Kind, Payload: e.Payload, WrittenAt: e.WrittenAt, AuthorKind: e.AuthorKind}, syncBase)
	}
	first, more, err := big.ListDecisionEntriesAfter(ctx, nil, 500, 1<<20)
	if err != nil || len(first) != 2 || !more {
		t.Fatalf("first byte-capped page = %d entries, more %v, %v", len(first), more, err)
	}
	second, more, err := big.ListDecisionEntriesAfter(ctx, domain.DecisionVector{"h_a": 2}, 500, 1<<20)
	if err != nil || len(second) != 1 || more {
		t.Fatalf("second byte-capped page = %d entries, more %v, %v", len(second), more, err)
	}
	single, _, err := big.ListDecisionEntriesAfter(ctx, nil, 500, 10)
	if err != nil || len(single) != 1 {
		t.Fatalf("page under a tiny byte cap = %d entries, want at least one", len(single))
	}
}

func TestListDecisionEntriesAfterHoldsBackConflictedOrigin(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	applySync(t, s, "h_x", syncRun("h_a", 1, 4, "dec_1"))
	applySync(t, s, "h_x", syncRun("h_b", 1, 2, "dec_2"))
	fork := syncRun("h_a", 3, 3, "dec_1")
	fork[0].ID = "dent_fork"
	applySync(t, s, "h_peer", fork)
	page, more, err := s.ListDecisionEntriesAfter(ctx, nil, 500, 1<<20)
	if err != nil || more {
		t.Fatalf("page more=%v err=%v", more, err)
	}
	var got []string
	for _, e := range page {
		got = append(got, fmt.Sprintf("%s:%d", e.Origin, e.OriginSeq))
	}
	if strings.Join(got, ",") != "h_b:1,h_b:2" {
		t.Fatalf("page = %v, want h_a quarantined entirely", got)
	}
	if page, _, _ := s.ListDecisionEntriesAfter(ctx, domain.DecisionVector{"h_a": 2, "h_b": 2}, 500, 1<<20); len(page) != 0 {
		t.Fatalf("page past the conflict = %d entries, want 0", len(page))
	}
}

func TestCheckDecisionHeadsRecordsFork(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	applySync(t, s, "h_x", syncRun("h_a", 1, 3, "dec_1"))
	conflicts, err := s.CheckDecisionHeads(ctx, "h_peer", domain.DecisionHeads{
		"h_a": {Seq: 3, ID: "dent_other"},
		"h_b": {Seq: 9, ID: "dent_unknown"},
	}, syncBase)
	if err != nil || len(conflicts) != 1 || conflicts[0].OriginSeq != 3 || conflicts[0].LocalEntryID != "dent_h_a_3" {
		t.Fatalf("conflicts = %+v, %v", conflicts, err)
	}
	if again, _ := s.CheckDecisionHeads(ctx, "h_peer", domain.DecisionHeads{"h_a": {Seq: 2, ID: "dent_h_a_2"}}, syncBase); len(again) != 0 {
		t.Fatalf("matching head recorded %+v", again)
	}
}

func TestLocalAppendAfterApplyOrdersAfterRemoteStamp(t *testing.T) {
	s := newTestStore(t)
	ahead := syncAnswer("h_b", 1, "dec_1")
	ahead.Stamp = domain.DecisionStamp{WallMS: syncBase.Add(10 * time.Minute).UnixMilli(), Counter: 3}
	applySync(t, s, "h_b", []domain.DecisionEntry{ahead})
	local, _ := appendDecision(t, s, decisionAnswered("dent_local", "h_a", "dec_1", 1, syncBase), syncBase)
	if domain.CompareDecisionEntries(local, ahead) <= 0 {
		t.Fatalf("local stamp %+v does not order after remote %+v", local.Stamp, ahead.Stamp)
	}
}

func TestAuditDecisionOriginsFindsHoles(t *testing.T) {
	dataDir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dataDir)
	applySync(t, s, "h_x", syncRun("h_a", 1, 3, "dec_1"))
	if gaps, err := s.AuditDecisionOrigins(context.Background()); err != nil || len(gaps) != 0 {
		t.Fatalf("contiguous audit = %+v, %v", gaps, err)
	}
	// Apply never stores a hole, so plant one the way an older build's bug or
	// a hand-edited database would: straight into the table.
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(`INSERT INTO decision_entries (id, origin, origin_seq, stamp_wall, stamp_counter, call_id, kind, payload, written_at, author_kind)
VALUES ('dent_hole', 'h_b', 2, 1, 0, 'dec_2', 'answered', '{}', '2026-11-19 00:00:00 +0000 UTC', 'person')`); err != nil {
		t.Fatal(err)
	}
	gaps, err := s.AuditDecisionOrigins(context.Background())
	if err != nil || len(gaps) != 1 || gaps[0] != (domain.DecisionSyncGap{Origin: "h_b", Have: 1, Got: 2}) {
		t.Fatalf("audit = %+v, %v; want h_b holding 1 entry with highest 2", gaps, err)
	}
}

func TestAuditDecisionOriginsReportsNoneForSlice1Data(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 5; i++ {
		appendDecision(t, s, decisionAnswered(fmt.Sprintf("dent_%d", i), "h_a", "dec_1", 1, syncBase), syncBase)
		appendDecision(t, s, decisionAnswered(fmt.Sprintf("dent_b%d", i), "h_b", "dec_1", 1, syncBase), syncBase)
	}
	if gaps, err := s.AuditDecisionOrigins(context.Background()); err != nil || len(gaps) != 0 {
		t.Fatalf("audit = %+v, %v", gaps, err)
	}
	last, err := s.DecisionOriginsLastHeard(context.Background())
	if err != nil || len(last) != 2 {
		t.Fatalf("last heard = %v, %v", last, err)
	}
}

// The SQLite store and the in-memory ledger the convergence tests use must
// behave identically under the sync rules.
func TestSQLiteLedgerConformance(t *testing.T) {
	ledgertest.RunApplyConformance(t, func(t *testing.T, _ string) decisionsync.Ledger {
		return newTestStore(t)
	})
}
