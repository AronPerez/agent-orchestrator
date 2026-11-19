package sqlite

import "testing"

const (
	decisionNotificationsMigration int64 = 20261118232626352
	beforeDecisionNotifications    int64 = 20261118045922851
)

func TestMigrationDecisionNotificationsRoundTrip(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeDecisionNotifications)
	const timestamp = "2026-11-18T08:00:00Z"
	if _, err := db.Exec(`
INSERT INTO sessions (id, num, kind, activity_last_at, created_at, updated_at)
VALUES ('standalone-1', 1, 'worker', ?, ?, ?);
INSERT INTO notifications (
    id, session_id, project_id, pr_url, type, title, body, status,
    created_at, resolved_at, dismissed_at, source_key
) VALUES (
    'notice-1', 'standalone-1', NULL, 'https://github.com/acme/app/pull/42',
    'session_stalled', 'Stalled', 'Existing notification', 'read', ?, ?, NULL, 'stall:1'
);`, timestamp, timestamp, timestamp, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}

	assertExisting := func() {
		t.Helper()
		var sessionID, prURL, typ, title, body, status, sourceKey string
		var projectID any
		if err := db.QueryRow(`
SELECT session_id, project_id, pr_url, type, title, body, status, source_key
FROM notifications WHERE id = 'notice-1'`).Scan(
			&sessionID, &projectID, &prURL, &typ, &title, &body, &status, &sourceKey,
		); err != nil {
			t.Fatal(err)
		}
		if sessionID != "standalone-1" || projectID != nil || prURL != "https://github.com/acme/app/pull/42" ||
			typ != "session_stalled" || title != "Stalled" || body != "Existing notification" ||
			status != "read" || sourceKey != "stall:1" {
			t.Fatalf("existing notification changed: session=%q project=%#v pr=%q type=%q title=%q body=%q status=%q source=%q",
				sessionID, projectID, prURL, typ, title, body, status, sourceKey)
		}
	}
	assertDecisionSchema := func() {
		t.Helper()
		var nullableSession, callColumn, originColumn, callIndex int
		if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('notifications') WHERE name = 'session_id' AND "notnull" = 0`).Scan(&nullableSession); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('notifications') WHERE name = 'decision_call_id' AND "notnull" = 1`).Scan(&callColumn); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('notifications') WHERE name = 'decision_origin_host_id' AND "notnull" = 1`).Scan(&originColumn); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_notifications_decision_call'`).Scan(&callIndex); err != nil {
			t.Fatal(err)
		}
		if nullableSession != 1 || callColumn != 1 || originColumn != 1 || callIndex != 1 {
			t.Fatalf("decision schema nullable session=%d call=%d origin=%d call index=%d",
				nullableSession, callColumn, originColumn, callIndex)
		}
		for _, name := range []string{
			"idx_notifications_status_history", "idx_notifications_history", "idx_notifications_unresolved",
			"idx_notifications_live_history", "idx_notifications_session", "idx_notifications_open_dedupe",
			"idx_notifications_source_dedupe",
		} {
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("index %s missing after migration", name)
			}
		}
	}

	upTo(t, db, decisionNotificationsMigration)
	assertDecisionSchema()
	assertExisting()

	if _, err := db.Exec(`
INSERT INTO notifications (
    id, session_id, type, title, body, status, created_at, source_key,
    decision_call_id, decision_origin_host_id
) VALUES ('decision-1', NULL, 'decision_needed', 'Decision needed · ao', 'Ship it?', 'unread', ?,
    'decision:dec_1', 'dec_1', 'h_origin')`, timestamp); err != nil {
		t.Fatalf("insert sessionless decision notification: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO notifications (
    id, session_id, type, title, status, created_at, decision_call_id, decision_origin_host_id
) VALUES ('decision-2', NULL, 'decision_needed', 'dup', 'unread', ?, 'dec_1', 'h_other')`, timestamp); err == nil {
		t.Fatal("second notification for the same call was accepted")
	}

	// Deleting the session cascades its own notifications but never a call's.
	// change_log rows reference the session without cascade; production clears
	// them first (Store.DeleteSession), so the test does too.
	if _, err := db.Exec(`DELETE FROM change_log WHERE session_id = 'standalone-1'; DELETE FROM sessions WHERE id = 'standalone-1'`); err != nil {
		t.Fatal(err)
	}
	var remaining []string
	if err := func() error {
		rows, err := db.Query(`SELECT id FROM notifications ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			remaining = append(remaining, id)
		}
		return rows.Err()
	}(); err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0] != "decision-1" {
		t.Fatalf("after session delete remaining = %v, want [decision-1]", remaining)
	}

	// Restore a session row for the round trip.
	if _, err := db.Exec(`
INSERT INTO sessions (id, num, kind, activity_last_at, created_at, updated_at)
VALUES ('standalone-1', 1, 'worker', ?, ?, ?);
INSERT INTO notifications (id, session_id, project_id, pr_url, type, title, body, status, created_at, resolved_at, source_key)
VALUES ('notice-1', 'standalone-1', NULL, 'https://github.com/acme/app/pull/42', 'session_stalled', 'Stalled',
    'Existing notification', 'read', ?, ?, 'stall:1');`, timestamp, timestamp, timestamp, timestamp, timestamp); err != nil {
		t.Fatal(err)
	}

	downTo(t, db, beforeDecisionNotifications)
	var decisions, callColumn int
	if err := db.QueryRow(`SELECT count(*) FROM notifications WHERE id = 'decision-1'`).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('notifications') WHERE name = 'decision_call_id'`).Scan(&callColumn); err != nil {
		t.Fatal(err)
	}
	if decisions != 0 || callColumn != 0 {
		t.Fatalf("rollback kept decision rows=%d call column=%d", decisions, callColumn)
	}
	assertExisting()

	upTo(t, db, decisionNotificationsMigration)
	assertDecisionSchema()
	assertExisting()
}
