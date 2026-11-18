-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
-- The Decisions page needs a live invalidation signal and a bounded "recent
-- Done" window. change_log.project_id is already nullable (0140); only its
-- event_type CHECK has to admit decision_updated. Rebuilding change_log would
-- temporarily invalidate every CDC trigger that writes it, so widen the one
-- CHECK in sqlite_schema exactly as earlier constraint migrations do, then
-- force a schema reload and assert the postcondition.
PRAGMA writable_schema=ON;

UPDATE sqlite_schema
SET sql = replace(
    sql,
    '''review_run_updated''',
    '''review_run_updated'',
            ''decision_updated'''
)
WHERE type = 'table'
  AND name = 'change_log'
  AND instr(sql, '''decision_updated''') = 0;

PRAGMA writable_schema=RESET;

CREATE TEMP TABLE decision_board_schema_guard (
    widened INTEGER CHECK (widened = 1)
);
INSERT INTO decision_board_schema_guard
SELECT instr(sql, '''decision_updated''') > 0
FROM sqlite_schema
WHERE type = 'table' AND name = 'change_log';
DROP TABLE decision_board_schema_guard;
-- +goose StatementEnd

-- +goose StatementBegin
BEGIN IMMEDIATE;

-- outcome_at is a rebuildable filter column written from the reduced call's
-- OutcomeAt; the immutable decision_entries log stays authoritative. Values use
-- the driver's canonical UTC time.String() form so they compare as text.
ALTER TABLE decision_calls ADD COLUMN outcome_at TIMESTAMP;

UPDATE decision_calls
SET outcome_at = CASE
    WHEN json_extract(body, '$.outcomeAt') GLOB '????-??-??T??:??:??*Z'
        THEN replace(rtrim(json_extract(body, '$.outcomeAt'), 'Z'), 'T', ' ') || ' +0000 UTC'
    ELSE strftime('%Y-%m-%d %H:%M:%S', json_extract(body, '$.outcomeAt')) || ' +0000 UTC'
END
WHERE state = 'done'
  AND json_valid(body)
  AND json_type(body, '$.outcomeAt') = 'text'
  AND json_extract(body, '$.outcomeAt') NOT GLOB '0001-01-01*'
  AND strftime('%s', json_extract(body, '$.outcomeAt')) IS NOT NULL;

CREATE INDEX decision_calls_board ON decision_calls (state, retired, outcome_at, raised_at);
-- The board names only the streams its own calls belong to; this keeps that
-- lookup to those streams' calls instead of every call ever raised.
CREATE INDEX decision_calls_by_stream ON decision_calls (stream_session_id, origin);

-- Decision calls deliberately have no local project/session foreign keys (a
-- synced call may name a project this machine does not have), so the event
-- carries the project id only when it exists locally and never a session id.
CREATE TRIGGER decision_calls_cdc_insert
AFTER INSERT ON decision_calls
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT id FROM projects WHERE id = NEW.project_id),
        NULL,
        'decision_updated',
        json_object('callId', NEW.call_id),
        datetime('now')
    );
END;

CREATE TRIGGER decision_calls_cdc_update
AFTER UPDATE ON decision_calls
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT id FROM projects WHERE id = NEW.project_id),
        NULL,
        'decision_updated',
        json_object('callId', NEW.call_id),
        datetime('now')
    );
END;

CREATE TRIGGER decision_calls_cdc_delete
AFTER DELETE ON decision_calls
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (
        (SELECT id FROM projects WHERE id = OLD.project_id),
        NULL,
        'decision_updated',
        json_object('callId', OLD.call_id),
        datetime('now')
    );
END;

COMMIT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
BEGIN IMMEDIATE;

DROP TRIGGER IF EXISTS decision_calls_cdc_delete;
DROP TRIGGER IF EXISTS decision_calls_cdc_update;
DROP TRIGGER IF EXISTS decision_calls_cdc_insert;
DELETE FROM change_log WHERE event_type = 'decision_updated';

-- The daemon regenerates its timestamp-progress triggers for decision_calls on
-- the next open; drop them now because they name outcome_at.
DROP TRIGGER IF EXISTS "ao_timestamp_dcf25f187eaeaba0d55e2b707e02a02022b204333c92b997abaf23e52feb2dce_insert";
DROP TRIGGER IF EXISTS "ao_timestamp_dcf25f187eaeaba0d55e2b707e02a02022b204333c92b997abaf23e52feb2dce_update";
DROP TRIGGER IF EXISTS "ao_timestamp_dcf25f187eaeaba0d55e2b707e02a02022b204333c92b997abaf23e52feb2dce_delete";
DROP INDEX IF EXISTS decision_calls_by_stream;
DROP INDEX IF EXISTS decision_calls_board;
ALTER TABLE decision_calls DROP COLUMN outcome_at;

COMMIT;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA writable_schema=ON;

UPDATE sqlite_schema
SET sql = replace(
    sql,
    '''review_run_updated'',
            ''decision_updated''',
    '''review_run_updated'''
)
WHERE type = 'table' AND name = 'change_log';

PRAGMA writable_schema=RESET;

CREATE TEMP TABLE decision_board_schema_guard (
    narrowed INTEGER CHECK (narrowed = 1)
);
INSERT INTO decision_board_schema_guard
SELECT instr(sql, '''decision_updated''') = 0
FROM sqlite_schema
WHERE type = 'table' AND name = 'change_log';
DROP TABLE decision_board_schema_guard;
-- +goose StatementEnd
