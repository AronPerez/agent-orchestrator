package sqlite

import (
	"testing"
	"time"
)

func TestTrackerIntakeHandledMigrationBackfillsLiveIssueSessions(t *testing.T) {
	const before = int64(20261116173733857)
	db := openMigratedDatabaseCopy(t, before)
	now := time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC)
	mustExec(t, db, `INSERT INTO projects (id, path, display_name, registered_at) VALUES ('demo','/tmp/demo','Demo',?)`, now)
	for _, row := range []struct {
		id         string
		num        int
		issueID    string
		terminated bool
	}{
		{id: "demo-1", num: 1, issueID: "github:acme/demo#1"},
		{id: "demo-2", num: 2, issueID: "github:acme/demo#2", terminated: true},
		{id: "demo-3", num: 3},
		// Legacy races could leave more than one live session for one issue.
		// Upgrade must collapse them into a single handled record.
		{id: "demo-4", num: 4, issueID: "github:acme/demo#1"},
	} {
		mustExec(t, db, `INSERT INTO sessions
			(id, project_id, num, kind, issue_id, activity_state, activity_last_at, is_terminated, session_mode, created_at, updated_at)
			VALUES (?, 'demo', ?, 'worker', ?, 'idle', ?, ?, 'tui', ?, ?)`,
			row.id, row.num, row.issueID, now, row.terminated, now, now)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rows, err := db.Query(`SELECT issue_id, session_id FROM tracker_intake_handled ORDER BY issue_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got [][2]string
	for rows.Next() {
		var issueID, sessionID string
		if err := rows.Scan(&issueID, &sessionID); err != nil {
			t.Fatal(err)
		}
		got = append(got, [2]string{issueID, sessionID})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != [2]string{"github:acme/demo#1", "demo-1"} {
		t.Fatalf("backfilled rows = %+v, want only the live issue-linked session", got)
	}
}
