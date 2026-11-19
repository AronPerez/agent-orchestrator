-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
-- Decision ledger sync records per-peer freshness and forked origins, and the
-- Decisions page needs a live signal when either changes. Widen change_log's
-- event_type CHECK in place exactly as the decision board migration does:
-- rebuilding change_log would invalidate every CDC trigger that writes it.
PRAGMA writable_schema=ON;

UPDATE sqlite_schema
SET sql = replace(
    sql,
    '''decision_updated''',
    '''decision_updated'',
            ''decision_sync_updated'''
)
WHERE type = 'table'
  AND name = 'change_log'
  AND instr(sql, '''decision_sync_updated''') = 0;

PRAGMA writable_schema=RESET;

CREATE TEMP TABLE decision_sync_schema_guard (
    widened INTEGER CHECK (widened = 1)
);
INSERT INTO decision_sync_schema_guard
SELECT instr(sql, '''decision_sync_updated''') > 0
FROM sqlite_schema
WHERE type = 'table' AND name = 'change_log';
DROP TABLE decision_sync_schema_guard;
-- +goose StatementEnd

-- +goose StatementBegin
BEGIN IMMEDIATE;

-- Operational status for each machine this one syncs decisions with. It holds
-- no secrets: peer bearer tokens live in <data dir>/decision-sync/peers.json.
-- Every column is rebuildable by syncing again; the decision_entries log stays
-- the only truth.
CREATE TABLE decision_sync_peers (
    host_id          TEXT      NOT NULL PRIMARY KEY,
    label            TEXT      NOT NULL DEFAULT '',
    url              TEXT      NOT NULL DEFAULT '',
    direction        TEXT      NOT NULL CHECK (direction IN ('outbound', 'inbound')),
    device_id        TEXT      NOT NULL DEFAULT '',
    created_at       TIMESTAMP NOT NULL,
    last_attempt_at  TIMESTAMP,
    last_success_at  TIMESTAMP,
    last_error_code  TEXT      NOT NULL DEFAULT '',
    in_sync          INTEGER   NOT NULL DEFAULT 0,
    peer_vector      TEXT      NOT NULL DEFAULT '{}',
    clock_ahead_ms   INTEGER   NOT NULL DEFAULT 0,
    peer_protocol    INTEGER   NOT NULL DEFAULT 0
);

-- Two different entries that claimed one identity (a copied data directory).
-- The origin stops syncing at its lowest conflicted sequence on this machine.
CREATE TABLE decision_sync_conflicts (
    origin          TEXT      NOT NULL,
    origin_seq      INTEGER   NOT NULL,
    local_entry_id  TEXT      NOT NULL,
    remote_entry_id TEXT      NOT NULL,
    peer_host_id    TEXT      NOT NULL,
    detected_at     TIMESTAMP NOT NULL,
    PRIMARY KEY (origin, origin_seq)
);

CREATE TRIGGER decision_sync_peers_cdc_insert
AFTER INSERT ON decision_sync_peers
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'decision_sync_updated', json_object('hostId', NEW.host_id), datetime('now'));
END;

CREATE TRIGGER decision_sync_peers_cdc_update
AFTER UPDATE ON decision_sync_peers
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'decision_sync_updated', json_object('hostId', NEW.host_id), datetime('now'));
END;

CREATE TRIGGER decision_sync_peers_cdc_delete
AFTER DELETE ON decision_sync_peers
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'decision_sync_updated', json_object('hostId', OLD.host_id), datetime('now'));
END;

CREATE TRIGGER decision_sync_conflicts_cdc_insert
AFTER INSERT ON decision_sync_conflicts
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'decision_sync_updated', json_object('origin', NEW.origin), datetime('now'));
END;

CREATE TRIGGER decision_sync_conflicts_cdc_update
AFTER UPDATE ON decision_sync_conflicts
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'decision_sync_updated', json_object('origin', NEW.origin), datetime('now'));
END;

CREATE TRIGGER decision_sync_conflicts_cdc_delete
AFTER DELETE ON decision_sync_conflicts
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NULL, NULL, 'decision_sync_updated', json_object('origin', OLD.origin), datetime('now'));
END;

COMMIT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
BEGIN IMMEDIATE;

DROP TRIGGER IF EXISTS decision_sync_conflicts_cdc_delete;
DROP TRIGGER IF EXISTS decision_sync_conflicts_cdc_update;
DROP TRIGGER IF EXISTS decision_sync_conflicts_cdc_insert;
DROP TRIGGER IF EXISTS decision_sync_peers_cdc_delete;
DROP TRIGGER IF EXISTS decision_sync_peers_cdc_update;
DROP TRIGGER IF EXISTS decision_sync_peers_cdc_insert;
-- Dropping each table also drops the timestamp-progress triggers the daemon
-- generated for it.
DROP TABLE IF EXISTS decision_sync_conflicts;
DROP TABLE IF EXISTS decision_sync_peers;
DELETE FROM timestamp_repair_progress WHERE table_name IN ('decision_sync_peers', 'decision_sync_conflicts');
DELETE FROM change_log WHERE event_type = 'decision_sync_updated';

COMMIT;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA writable_schema=ON;

UPDATE sqlite_schema
SET sql = replace(
    sql,
    '''decision_updated'',
            ''decision_sync_updated''',
    '''decision_updated'''
)
WHERE type = 'table' AND name = 'change_log';

PRAGMA writable_schema=RESET;

CREATE TEMP TABLE decision_sync_schema_guard (
    narrowed INTEGER CHECK (narrowed = 1)
);
INSERT INTO decision_sync_schema_guard
SELECT instr(sql, '''decision_sync_updated''') = 0
FROM sqlite_schema
WHERE type = 'table' AND name = 'change_log';
DROP TABLE decision_sync_schema_guard;
-- +goose StatementEnd
