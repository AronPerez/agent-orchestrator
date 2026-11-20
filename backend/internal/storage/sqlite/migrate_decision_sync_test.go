package sqlite

import (
	"sort"
	"testing"
)

const (
	decisionSyncMigrationVersion = int64(20261119220659132)
	decisionSyncPreviousVersion  = decisionBoardMigrationVersion
)

// The sync migration adds two status tables, widens the change_log CHECK in
// place for decision_sync_updated, and emits one event per row change on both
// tables. Every earlier event type and trigger survives, and down restores the
// earlier CHECK and trigger set.
func TestMigrationDecisionSyncRoundTrip(t *testing.T) {
	db := openMigratedDatabaseCopy(t, decisionSyncPreviousVersion)
	if _, err := db.Exec(`
INSERT INTO projects (id, path, registered_at) VALUES ('local', '/tmp/local', CURRENT_TIMESTAMP);
INSERT INTO sessions (id, project_id, num, activity_last_at, created_at, updated_at)
VALUES ('worker', 'local', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var beforeRows int
	var beforeHead int64
	if err := db.QueryRow(`SELECT count(*), coalesce(max(seq), 0) FROM change_log`).Scan(&beforeRows, &beforeHead); err != nil {
		t.Fatal(err)
	}
	beforeTriggers := decisionBoardTriggerNames(t, db)

	upTo(t, db, decisionSyncMigrationVersion)

	var afterRows int
	var afterHead int64
	if err := db.QueryRow(`SELECT count(*), coalesce(max(seq), 0) FROM change_log`).Scan(&afterRows, &afterHead); err != nil {
		t.Fatal(err)
	}
	if afterRows != beforeRows || afterHead != beforeHead {
		t.Fatalf("change_log rows/head = %d/%d, want %d/%d", afterRows, afterHead, beforeRows, beforeHead)
	}
	if n := decisionBoardEventCount(t, db, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN ('decision_sync_peers', 'decision_sync_conflicts')`); n != 2 {
		t.Fatalf("decision sync tables = %d, want 2", n)
	}
	wantTriggers := append(append([]string{}, beforeTriggers...),
		"decision_sync_conflicts_cdc_delete", "decision_sync_conflicts_cdc_insert", "decision_sync_conflicts_cdc_update",
		"decision_sync_peers_cdc_delete", "decision_sync_peers_cdc_insert", "decision_sync_peers_cdc_update")
	sort.Strings(wantTriggers)
	assertTriggerNames(t, decisionBoardTriggerNames(t, db), wantTriggers)

	// Every earlier event type is still admitted next to the new one.
	for _, typ := range []string{"session_created", "session_updated", "pr_created", "review_run_updated", "decision_updated", "decision_sync_updated"} {
		if _, err := db.Exec(`INSERT INTO change_log (project_id, event_type, payload) VALUES (NULL, ?, '{}')`, typ); err != nil {
			t.Fatalf("change_log refused %s after migration: %v", typ, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM change_log WHERE seq > ?`, afterHead); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO change_log (project_id, event_type, payload) VALUES (NULL, 'not_an_event', '{}')`); err == nil {
		t.Fatal("change_log accepted an unknown event type")
	}

	syncEvents := func(key, value string) int {
		t.Helper()
		return decisionBoardEventCount(t, db, `
SELECT count(*) FROM change_log
WHERE event_type = 'decision_sync_updated' AND project_id IS NULL AND session_id IS NULL
  AND json_extract(payload, '$.'||?) = ?`, key, value)
	}
	if _, err := db.Exec(`INSERT INTO decision_sync_peers (host_id, direction, created_at) VALUES ('h_peer', 'outbound', '2026-11-19 00:00:00 +0000 UTC')`); err != nil {
		t.Fatal(err)
	}
	if n := syncEvents("hostId", "h_peer"); n != 1 {
		t.Fatalf("peer insert events = %d, want 1", n)
	}
	if _, err := db.Exec(`UPDATE decision_sync_peers SET in_sync = 1 WHERE host_id = 'h_peer'`); err != nil {
		t.Fatal(err)
	}
	if n := syncEvents("hostId", "h_peer"); n != 2 {
		t.Fatalf("peer update events = %d, want 2", n)
	}
	if _, err := db.Exec(`DELETE FROM decision_sync_peers WHERE host_id = 'h_peer'`); err != nil {
		t.Fatal(err)
	}
	if n := syncEvents("hostId", "h_peer"); n != 3 {
		t.Fatalf("peer delete events = %d, want 3", n)
	}
	if _, err := db.Exec(`INSERT INTO decision_sync_peers (host_id, direction, created_at) VALUES ('h_bad', 'sideways', '2026-11-19 00:00:00 +0000 UTC')`); err == nil {
		t.Fatal("decision_sync_peers accepted an unknown direction")
	}
	if _, err := db.Exec(`
INSERT INTO decision_sync_conflicts (origin, origin_seq, local_entry_id, remote_entry_id, peer_host_id, detected_at)
VALUES ('h_a', 3, 'e_local', 'e_remote', 'h_peer', '2026-11-19 00:00:00 +0000 UTC')`); err != nil {
		t.Fatal(err)
	}
	if n := syncEvents("origin", "h_a"); n != 1 {
		t.Fatalf("conflict insert events = %d, want 1", n)
	}

	downTo(t, db, decisionSyncPreviousVersion)
	if n := decisionBoardEventCount(t, db, `SELECT count(*) FROM change_log WHERE event_type = 'decision_sync_updated'`); n != 0 {
		t.Fatalf("decision_sync_updated rows after rollback = %d", n)
	}
	if n := decisionBoardEventCount(t, db, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN ('decision_sync_peers', 'decision_sync_conflicts')`); n != 0 {
		t.Fatalf("decision sync tables after rollback = %d", n)
	}
	if _, err := db.Exec(`INSERT INTO change_log (project_id, event_type, payload) VALUES (NULL, 'decision_sync_updated', '{}')`); err == nil {
		t.Fatal("change_log accepted decision_sync_updated after rollback")
	}
	if _, err := db.Exec(`INSERT INTO change_log (project_id, event_type, payload) VALUES (NULL, 'decision_updated', '{}')`); err != nil {
		t.Fatalf("change_log refused decision_updated after rollback: %v", err)
	}
	assertTriggerNames(t, decisionBoardTriggerNames(t, db), beforeTriggers)

	upTo(t, db, decisionSyncMigrationVersion)
	if _, err := db.Exec(`INSERT INTO decision_sync_peers (host_id, direction, created_at) VALUES ('h_again', 'inbound', '2026-11-19 00:00:00 +0000 UTC')`); err != nil {
		t.Fatal(err)
	}
	if n := decisionBoardEventCount(t, db, `SELECT count(*) FROM change_log WHERE event_type = 'decision_sync_updated'`); n != 1 {
		t.Fatalf("decision_sync_updated rows after re-apply = %d, want 1", n)
	}
}

func assertTriggerNames(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("triggers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("triggers = %v, want %v", got, want)
		}
	}
}
