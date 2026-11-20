package sqlite

import "testing"

const decisionIdentityRepairMigrationVersion = int64(20261120003512417)

// The repair migration adds the archive table and the repair-entry index,
// and down removes both, leaving decision_entries as it was.
func TestMigrationDecisionIdentityRepairRoundTrip(t *testing.T) {
	db := openMigratedDatabaseCopy(t, decisionSyncMigrationVersion)
	if _, err := db.Exec(`INSERT INTO decision_entries (id, origin, origin_seq, stamp_wall, stamp_counter, call_id, kind, payload, written_at, author_kind)
VALUES ('dent_1', 'h_x', 1, 1, 0, 'dec_1', 'raised', '{}', '2026-11-20 00:00:00 +0000 UTC', 'person')`); err != nil {
		t.Fatal(err)
	}
	objects := func() int {
		return decisionBoardEventCount(t, db, `SELECT count(*) FROM sqlite_master
WHERE name IN ('decision_entries_forked', 'decision_entries_forked_by_repair', 'decision_entries_repairs')`)
	}

	upTo(t, db, decisionIdentityRepairMigrationVersion)
	if n := objects(); n != 3 {
		t.Fatalf("repair objects after up = %d, want 3", n)
	}
	if _, err := db.Exec(`INSERT INTO decision_entries_forked
SELECT *, '2026-11-20 00:00:01 +0000 UTC', 'rep_1' FROM decision_entries WHERE id = 'dent_1'`); err != nil {
		t.Fatalf("archive a row: %v", err)
	}
	var plan string
	if err := db.QueryRow(`EXPLAIN QUERY PLAN SELECT id FROM decision_entries WHERE kind = 'identity_repaired'`).Scan(new(int), new(int), new(int), &plan); err != nil {
		t.Fatal(err)
	}
	if plan != "SEARCH decision_entries USING INDEX decision_entries_repairs (kind=?)" {
		t.Fatalf("repair entries are read by %q", plan)
	}

	downTo(t, db, decisionSyncMigrationVersion)
	if n := objects(); n != 0 {
		t.Fatalf("repair objects after down = %d", n)
	}
	if n := decisionBoardEventCount(t, db, `SELECT count(*) FROM decision_entries WHERE id = 'dent_1'`); n != 1 {
		t.Fatalf("decision_entries rows after down = %d", n)
	}
	upTo(t, db, decisionIdentityRepairMigrationVersion)
	if n := objects(); n != 3 {
		t.Fatalf("repair objects after re-apply = %d", n)
	}
}
