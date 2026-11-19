package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRepairPreservesSessionRevisionAndChangeLog(t *testing.T) {
	db := openMigratedTestDB(t)
	const legacy = "2026-10-21 09:00:00.123456789 +0530 IST"
	_, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at,latest_user_prompt_at,latest_interaction_at)
VALUES('preserved',1,'aider','idle',?,0,?,?,?,?);`, legacy, legacy, legacy, legacy, legacy)
	if err != nil {
		t.Fatal(err)
	}
	state := func() [3]int64 {
		t.Helper()
		var values [3]int64
		if err := db.QueryRow(`SELECT revision,(SELECT count(*) FROM change_log),COALESCE((SELECT seq FROM sqlite_sequence WHERE name='change_log'),-1) FROM sessions WHERE id='preserved'`).Scan(&values[0], &values[1], &values[2]); err != nil {
			t.Fatal(err)
		}
		return values
	}
	for _, name := range []string{"normal", "missing sequence", "validation error", "cleanup error"} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.Exec(`UPDATE sessions SET activity_last_at=?,created_at=?,updated_at=?,latest_user_prompt_at=?,latest_interaction_at=? WHERE id='preserved'`, legacy, legacy, legacy, legacy, legacy); err != nil {
				t.Fatal(err)
			}
			if name == "missing sequence" {
				if _, err := db.Exec(`DELETE FROM sqlite_sequence WHERE name='change_log'`); err != nil {
					t.Fatal(err)
				}
			}
			if name == "validation error" {
				if _, err := db.Exec(`CREATE TRIGGER keep_validation BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT,'validation retained'); END`); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := db.Exec(`DROP TRIGGER keep_validation`); err != nil {
						t.Fatal(err)
					}
				}()
			}
			if name == "cleanup error" {
				if _, err := db.Exec(`CREATE TRIGGER fail_cleanup BEFORE DELETE ON change_log BEGIN SELECT RAISE(ABORT,'cleanup retained'); END`); err != nil {
					t.Fatal(err)
				}
			}
			before := state()
			var beforeTime string
			if err := db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM sessions WHERE id='preserved'`).Scan(&beforeTime); err != nil {
				t.Fatal(err)
			}
			err = repairTimestamps(context.Background(), db)
			if strings.HasSuffix(name, "error") {
				want := strings.TrimSuffix(name, " error") + " retained"
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("%s was not retained: %v", name, err)
				}
				var afterTime string
				if err := db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM sessions WHERE id='preserved'`).Scan(&afterTime); err != nil || afterTime != beforeTime {
					t.Fatalf("failed repair changed timestamp from %q to %q: %v", beforeTime, afterTime, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if after := state(); after != before {
				t.Fatalf("representation-only repair changed [session revision, change-log rows, change-log sequence] from %v to %v", before, after)
			}
		})
	}
}

func TestPointTimestampNormalizationPreservesNoOpFacts(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at,latest_interaction_at) VALUES('ended',1,'aider','idle',CURRENT_TIMESTAMP,1,CURRENT_TIMESTAMP,'2026-10-21 09:00:00 +0530 IST','2026-10-21 09:00:00 +0530 IST')`); err != nil {
		t.Fatal(err)
	}
	columns, err := timestampColumns(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareTimestampProgress(context.Background(), db, columns); err != nil {
		t.Fatal(err)
	}
	s, err := OpenPreMigrated(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	facts := func() string {
		var value string
		if err := db.QueryRow(`SELECT json_array(revision,(SELECT json_group_array(json_array(seq,event_type,payload,CAST(created_at AS TEXT))) FROM change_log)) FROM sessions WHERE id='ended'`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := facts()
	if err := s.RecordSessionInteraction(context.Background(), "ended", "", time.Date(2026, 10, 21, 4, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if after := facts(); after != before {
		t.Fatalf("no-op normalization published facts: %s -> %s", before, after)
	}
	var normalized string
	if err := db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM sessions WHERE id='ended'`).Scan(&normalized); err != nil || normalized != "2026-10-21 03:30:00 +0000 UTC" {
		t.Fatalf("point normalization did not run with progress triggers installed: %q %v", normalized, err)
	}
	var paused int
	if err := db.QueryRow(`SELECT paused FROM timestamp_repair_control WHERE id=1`).Scan(&paused); err != nil || paused != 0 {
		t.Fatalf("point normalization left progress tracking paused: paused=%d err=%v", paused, err)
	}
}

func TestUnexpectedTimestampTriggerWriteRollsBackAndBlocksStartup(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`CREATE TABLE unrelated(value INTEGER);INSERT INTO unrelated VALUES(0);
 INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('guarded',1,'aider','idle',CURRENT_TIMESTAMP,0,'2026-10-21 09:00:00 +0530 IST','2026-10-21 09:00:00 +0530 IST');
 CREATE TRIGGER unexpected_timestamp_write AFTER UPDATE ON sessions BEGIN UPDATE unrelated SET value=value+1;END;`); err != nil {
		t.Fatal(err)
	}
	if err := repairTimestamps(context.Background(), db); err == nil || !strings.Contains(err.Error(), "unexpected timestamp repair writes") {
		t.Fatalf("unexpected write accepted: %v", err)
	}
	s, err := Open(dir)
	if err == nil {
		_ = s.Close()
		t.Fatal("unexpected trigger effects published unrepaired readers")
	}
	var n int
	var raw string
	if err := db.QueryRow(`SELECT value FROM unrelated`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("unexpected fact escaped rollback: %d %v", n, err)
	}
	if err := db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM sessions`).Scan(&raw); err != nil || raw != "2026-10-21 09:00:00 +0530 IST" {
		t.Fatalf("failed batch changed bytes: %q %v", raw, err)
	}
	ro, err := OpenReadOnly(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, ok, err := ro.GetSession(context.Background(), "guarded"); err != nil || !ok {
		t.Fatalf("fallback reader unavailable: %v %v", ok, err)
	}
}

// Normalizing a decision call's stored timestamps is a representation-only
// write: its decision_calls_cdc_update trigger fires, but the repair removes
// that event, so no decision_updated reaches clients for it.
func TestRepairDoesNotPublishDecisionEvents(t *testing.T) {
	db := openMigratedTestDB(t)
	const legacy = "2026-10-21 09:00:00.123456789 +0530 IST"
	if _, err := db.Exec(`INSERT INTO decision_calls(call_id,origin,owner_session_id,state,raised_at,last_wall,last_counter,body,outcome_at)
VALUES('dec_legacy','h_a','w-1','done',?,1,0,'{}',?)`, legacy, legacy); err != nil {
		t.Fatal(err)
	}
	events := func() (count int64, head int64) {
		t.Helper()
		if err := db.QueryRow(`SELECT count(*),COALESCE((SELECT seq FROM sqlite_sequence WHERE name='change_log'),-1) FROM change_log WHERE event_type='decision_updated'`).Scan(&count, &head); err != nil {
			t.Fatal(err)
		}
		return count, head
	}
	beforeCount, beforeHead := events()
	if beforeCount != 1 {
		t.Fatalf("seeded decision events = %d, want the insert's one", beforeCount)
	}
	if err := repairTimestamps(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if count, head := events(); count != beforeCount || head != beforeHead {
		t.Fatalf("repair published decision events: count %d->%d head %d->%d", beforeCount, count, beforeHead, head)
	}
	var raised, outcome string
	if err := db.QueryRow(`SELECT CAST(raised_at AS TEXT),CAST(outcome_at AS TEXT) FROM decision_calls WHERE call_id='dec_legacy'`).Scan(&raised, &outcome); err != nil {
		t.Fatal(err)
	}
	if raised != "2026-10-21 03:30:00.123456789 +0000 UTC" || outcome != raised {
		t.Fatalf("decision timestamps not normalized: raised=%q outcome=%q", raised, outcome)
	}
}

// Normalizing a sync status row's stored timestamps is representation-only too:
// its decision_sync_*_cdc_update triggers fire, and the repair removes those
// events, so no decision_sync_updated reaches clients for it.
func TestRepairDoesNotPublishDecisionSyncEvents(t *testing.T) {
	db := openMigratedTestDB(t)
	const legacy = "2026-10-21 09:00:00.123456789 +0530 IST"
	if _, err := db.Exec(`INSERT INTO decision_sync_peers(host_id,direction,created_at,last_success_at) VALUES('h_peer','outbound',?,?)`, legacy, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO decision_sync_conflicts(origin,origin_seq,local_entry_id,remote_entry_id,peer_host_id,detected_at) VALUES('h_x',3,'a','b','h_peer',?)`, legacy); err != nil {
		t.Fatal(err)
	}
	events := func() (count int64, head int64) {
		t.Helper()
		if err := db.QueryRow(`SELECT count(*),COALESCE((SELECT seq FROM sqlite_sequence WHERE name='change_log'),-1) FROM change_log WHERE event_type='decision_sync_updated'`).Scan(&count, &head); err != nil {
			t.Fatal(err)
		}
		return count, head
	}
	beforeCount, beforeHead := events()
	if beforeCount != 2 {
		t.Fatalf("seeded sync events = %d, want the two inserts", beforeCount)
	}
	if err := repairTimestamps(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if count, head := events(); count != beforeCount || head != beforeHead {
		t.Fatalf("repair published sync events: count %d->%d head %d->%d", beforeCount, count, beforeHead, head)
	}
	var created, detected string
	if err := db.QueryRow(`SELECT CAST(created_at AS TEXT) FROM decision_sync_peers WHERE host_id='h_peer'`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT CAST(detected_at AS TEXT) FROM decision_sync_conflicts WHERE origin='h_x'`).Scan(&detected); err != nil {
		t.Fatal(err)
	}
	if created != "2026-10-21 03:30:00.123456789 +0000 UTC" || detected != created {
		t.Fatalf("sync timestamps not normalized: created=%q detected=%q", created, detected)
	}
}
