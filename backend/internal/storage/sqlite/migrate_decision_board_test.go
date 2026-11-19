package sqlite

import (
	"database/sql"
	"sort"
	"testing"
)

const (
	decisionBoardMigrationVersion = int64(20261118232824790)
	decisionBoardPreviousVersion  = int64(20261118232626352)
)

func decisionBoardTriggerNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'trigger' AND name NOT GLOB 'ao_timestamp_*' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

func decisionBoardOutcomes(t *testing.T, db *sql.DB) map[string]sql.NullString {
	t.Helper()
	rows, err := db.Query(`SELECT call_id, CAST(outcome_at AS TEXT) FROM decision_calls`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	outcomes := map[string]sql.NullString{}
	for rows.Next() {
		var id string
		var at sql.NullString
		if err := rows.Scan(&id, &at); err != nil {
			t.Fatal(err)
		}
		outcomes[id] = at
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return outcomes
}

func decisionBoardEventCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func insertDecisionBoardCall(t *testing.T, db *sql.DB, callID, projectID, state, body string) {
	t.Helper()
	if _, err := db.Exec(`
INSERT INTO decision_calls (
    call_id, origin, project_id, owner_session_id, state, raised_at,
    last_wall, last_counter, body
) VALUES (?, 'host-a', ?, 'owner', ?, '2026-11-01 00:00:00 +0000 UTC', 1, 0, ?)`,
		callID, projectID, state, body); err != nil {
		t.Fatalf("insert decision call %s: %v", callID, err)
	}
}

// The board migration widens the change_log CHECK in place: every row, its
// sequence and every existing CDC trigger survive, existing writers keep
// emitting their own types, and decision calls emit decision_updated with a
// project id only when that project exists locally.
func TestMigrationDecisionBoardRoundTrip(t *testing.T) {
	db := openMigratedDatabaseCopy(t, decisionBoardPreviousVersion)
	if _, err := db.Exec(`
INSERT INTO projects (id, path, registered_at) VALUES ('local', '/tmp/local', CURRENT_TIMESTAMP);
INSERT INTO sessions (id, project_id, num, activity_last_at, created_at, updated_at)
VALUES ('worker', 'local', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	insertDecisionBoardCall(t, db, "dec_done_utc", "local", "done", `{"outcomeAt":"2026-11-10T09:08:07.123456789Z"}`)
	insertDecisionBoardCall(t, db, "dec_done_offset", "", "done", `{"outcomeAt":"2026-11-10T09:08:07+02:00"}`)
	insertDecisionBoardCall(t, db, "dec_open", "remote", "in_progress", `{"outcomeAt":"0001-01-01T00:00:00Z"}`)

	var beforeRows int
	var beforeHead int64
	if err := db.QueryRow(`SELECT count(*), coalesce(max(seq), 0) FROM change_log`).Scan(&beforeRows, &beforeHead); err != nil {
		t.Fatal(err)
	}
	if beforeRows == 0 {
		t.Fatal("seed produced no change_log rows to preserve")
	}
	beforeTriggers := decisionBoardTriggerNames(t, db)

	upTo(t, db, decisionBoardMigrationVersion)

	var afterRows int
	var afterHead int64
	if err := db.QueryRow(`SELECT count(*), coalesce(max(seq), 0) FROM change_log`).Scan(&afterRows, &afterHead); err != nil {
		t.Fatal(err)
	}
	if afterRows != beforeRows || afterHead != beforeHead {
		t.Fatalf("change_log rows/head = %d/%d, want %d/%d", afterRows, afterHead, beforeRows, beforeHead)
	}
	wantTriggers := append(append([]string{}, beforeTriggers...),
		"decision_calls_cdc_delete", "decision_calls_cdc_insert", "decision_calls_cdc_update")
	sort.Strings(wantTriggers)
	gotTriggers := decisionBoardTriggerNames(t, db)
	if len(gotTriggers) != len(wantTriggers) {
		t.Fatalf("triggers after migration = %v, want %v", gotTriggers, wantTriggers)
	}
	for i := range wantTriggers {
		if gotTriggers[i] != wantTriggers[i] {
			t.Fatalf("triggers after migration = %v, want %v", gotTriggers, wantTriggers)
		}
	}

	outcomes := decisionBoardOutcomes(t, db)
	if got := outcomes["dec_done_utc"]; !got.Valid || got.String != "2026-11-10 09:08:07.123456789 +0000 UTC" {
		t.Fatalf("utc outcome_at = %#v", got)
	}
	if got := outcomes["dec_done_offset"]; !got.Valid || got.String != "2026-11-10 07:08:07 +0000 UTC" {
		t.Fatalf("offset outcome_at = %#v", got)
	}
	if got := outcomes["dec_open"]; got.Valid {
		t.Fatalf("open call outcome_at = %#v, want NULL", got)
	}
	var boardIndexes int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name IN ('decision_calls_board', 'decision_calls_by_stream')`).Scan(&boardIndexes); err != nil || boardIndexes != 2 {
		t.Fatalf("decision board indexes = %d, %v", boardIndexes, err)
	}

	// Representative existing writers still emit their own event types.
	if _, err := db.Exec(`UPDATE sessions SET display_name = 'renamed' WHERE id = 'worker'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO pr (url, session_id, updated_at) VALUES ('https://github.com/acme/app/pull/1', 'worker', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO review (id, session_id, project_id, harness, created_at, updated_at)
VALUES ('review-1', 'worker', 'local', 'claude-code', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
INSERT INTO review_run (id, review_id, session_id, harness, created_at)
VALUES ('run-1', 'review-1', 'worker', 'claude-code', CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"session_updated", "pr_created", "review_run_created"} {
		if n := decisionBoardEventCount(t, db, `SELECT count(*) FROM change_log WHERE seq > ? AND event_type = ? AND project_id = 'local'`, beforeHead, typ); n == 0 {
			t.Fatalf("no %s event after migration", typ)
		}
	}

	// Local, missing and absent projects all emit without FK errors.
	insertDecisionBoardCall(t, db, "dec_local", "local", "needs_you", `{}`)
	insertDecisionBoardCall(t, db, "dec_remote", "remote-only", "needs_you", `{}`)
	insertDecisionBoardCall(t, db, "dec_none", "", "needs_you", `{}`)
	for callID, wantProject := range map[string]sql.NullString{
		"dec_local":  {String: "local", Valid: true},
		"dec_remote": {},
		"dec_none":   {},
	} {
		var project, session sql.NullString
		if err := db.QueryRow(`
SELECT project_id, session_id FROM change_log
WHERE event_type = 'decision_updated' AND json_extract(payload, '$.callId') = ?`, callID).Scan(&project, &session); err != nil {
			t.Fatalf("decision_updated for %s: %v", callID, err)
		}
		if project != wantProject || session.Valid {
			t.Fatalf("decision_updated %s project=%#v session=%#v, want project %#v and no session", callID, project, session, wantProject)
		}
	}
	if _, err := db.Exec(`UPDATE decision_calls SET state = 'in_progress' WHERE call_id = 'dec_remote'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM decision_calls WHERE call_id = 'dec_none'`); err != nil {
		t.Fatal(err)
	}
	if n := decisionBoardEventCount(t, db, `SELECT count(*) FROM change_log WHERE event_type = 'decision_updated' AND json_extract(payload, '$.callId') = 'dec_remote'`); n != 2 {
		t.Fatalf("dec_remote events = %d, want insert + update", n)
	}
	if n := decisionBoardEventCount(t, db, `SELECT count(*) FROM change_log WHERE event_type = 'decision_updated' AND json_extract(payload, '$.callId') = 'dec_none'`); n != 2 {
		t.Fatalf("dec_none events = %d, want insert + delete", n)
	}

	downTo(t, db, decisionBoardPreviousVersion)
	if n := decisionBoardEventCount(t, db, `SELECT count(*) FROM change_log WHERE event_type = 'decision_updated'`); n != 0 {
		t.Fatalf("decision_updated rows after rollback = %d", n)
	}
	if n := decisionBoardEventCount(t, db, `SELECT count(*) FROM pragma_table_info('decision_calls') WHERE name = 'outcome_at'`); n != 0 {
		t.Fatal("outcome_at survived rollback")
	}
	if n := decisionBoardEventCount(t, db, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name IN ('decision_calls_board', 'decision_calls_by_stream')`); n != 0 {
		t.Fatalf("decision board indexes after rollback = %d", n)
	}
	if _, err := db.Exec(`INSERT INTO change_log (project_id, event_type, payload) VALUES ('local', 'decision_updated', '{}')`); err == nil {
		t.Fatal("change_log accepted decision_updated after rollback")
	}
	afterDown := decisionBoardTriggerNames(t, db)
	if len(afterDown) != len(beforeTriggers) {
		t.Fatalf("triggers after rollback = %v, want %v", afterDown, beforeTriggers)
	}
	for i := range beforeTriggers {
		if afterDown[i] != beforeTriggers[i] {
			t.Fatalf("triggers after rollback = %v, want %v", afterDown, beforeTriggers)
		}
	}

	upTo(t, db, decisionBoardMigrationVersion)
	insertDecisionBoardCall(t, db, "dec_again", "", "needs_you", `{}`)
	if n := decisionBoardEventCount(t, db, `SELECT count(*) FROM change_log WHERE event_type = 'decision_updated'`); n != 1 {
		t.Fatalf("decision_updated rows after re-apply = %d, want 1", n)
	}
}
