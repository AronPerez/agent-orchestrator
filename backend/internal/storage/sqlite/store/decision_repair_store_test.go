package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync/ledgertest"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

const rawDecisionColumns = `id, origin, origin_seq, stamp_wall, stamp_counter, call_id, kind,
	CAST(payload AS BLOB), CAST(written_at AS BLOB), author_kind, author_surface, author_session`

// rawDecisionRows reads every stored byte of each row, keyed by id.
func rawDecisionRows(t *testing.T, raw *sql.DB, table string) map[string][]string {
	t.Helper()
	rows, err := raw.Query(`SELECT ` + rawDecisionColumns + ` FROM ` + table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		cols := make([]sql.RawBytes, 12)
		ptrs := make([]any, len(cols))
		for i := range cols {
			ptrs[i] = &cols[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(cols))
		for i, c := range cols {
			row[i] = string(c)
		}
		out[row[0]] = row
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestArchiveDecisionForkKeepsEveryColumn(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	now := time.UnixMilli(1_800_000_000_000).UTC()

	fork := ledgertest.RepairFork("h_x", 3, 4, "dec_f")
	fork[1].Payload = json.RawMessage(`{"choice":"A",  "option":1,"note":"<&> é"}`)
	received := append(ledgertest.ReceivedRun("h_x", 1, 2, "dec_s"), fork...)
	received = append(received, ledgertest.ReceivedRun("h_b", 1, 2, "dec_b")...)
	if _, err := s.ApplyDecisionEntries(ctx, "h_peer", received, now); err != nil {
		t.Fatal(err)
	}
	if c, err := s.CheckDecisionHeads(ctx, "h_peer", domain.DecisionHeads{"h_x": {Seq: 3, ID: "dent_o_3"}}, now); err != nil || len(c) != 1 {
		t.Fatalf("conflict = %+v, %v", c, err)
	}
	raw := openRawDB(t, dir)
	before := rawDecisionRows(t, raw, "decision_entries")

	plan := domain.DecisionRepairPlan{
		RepairID: "rep_1", Origin: "h_x", FromSeq: 3, NewOrigin: "h_y",
		ForkIDs: []string{fork[0].ID, fork[1].ID},
	}
	n := 0
	newID := func() string { n++; return fmt.Sprintf("dent_new_%d", n) }
	if wrote, err := s.ReissueDecisionFork(ctx, plan, newID, now); err != nil || !wrote {
		t.Fatalf("reissue = %v, %v", wrote, err)
	}
	if wrote, err := s.ReissueDecisionFork(ctx, plan, newID, now); err != nil || wrote {
		t.Fatalf("second reissue = %v, %v", wrote, err)
	}
	if v, _ := s.DecisionVector(ctx); v["h_y"] != 3 {
		t.Fatalf("new identity holds %d entries, want 3", v["h_y"])
	}
	for range 2 {
		if complete, err := s.ArchiveDecisionFork(ctx, "rep_1", now); err != nil || !complete {
			t.Fatalf("archive = %v, %v", complete, err)
		}
	}

	after := rawDecisionRows(t, raw, "decision_entries")
	forked := rawDecisionRows(t, raw, "decision_entries_forked")
	for _, e := range fork {
		if !reflect.DeepEqual(forked[e.ID], before[e.ID]) {
			t.Fatalf("archived %s = %q, was %q", e.ID, forked[e.ID], before[e.ID])
		}
		if _, still := after[e.ID]; still {
			t.Fatalf("%s is still in the log", e.ID)
		}
	}
	for id, row := range before {
		if _, moved := forked[id]; moved {
			continue
		}
		if !reflect.DeepEqual(after[id], row) {
			t.Fatalf("%s changed: %q, was %q", id, after[id], row)
		}
	}
	if len(after) != len(before)-2+3 {
		t.Fatalf("log holds %d rows, want %d", len(after), len(before)+1)
	}

	archived, err := s.ListArchivedDecisionEntries(ctx, "rep_1")
	if err != nil || len(archived) != 2 || !archived[0].ArchivedAt.Equal(now) {
		t.Fatalf("archived = %+v, %v", archived, err)
	}
	if conflicts, _ := s.ListDecisionSyncConflicts(ctx); len(conflicts) != 0 {
		t.Fatalf("conflicts left: %+v", conflicts)
	}
	call, ok, err := s.GetDecisionCall(ctx, "dec_f")
	if err != nil || !ok || call.Origin != "h_y" {
		t.Fatalf("repaired call = %+v, %v, %v", call, ok, err)
	}
}
