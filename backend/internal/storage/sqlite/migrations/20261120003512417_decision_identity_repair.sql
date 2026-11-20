-- +goose Up
-- Entries an identity repair moved out of the replicated log: a machine's
-- own entries under an identity it shared with a copy of itself, after it
-- re-issued them under a new identity. Every column is kept byte for byte,
-- with when and by which repair the row moved. Nothing reads this table to
-- reduce calls or to sync; it keeps the originals readable.
CREATE TABLE decision_entries_forked (
    id             TEXT      NOT NULL PRIMARY KEY,
    origin         TEXT      NOT NULL,
    origin_seq     INTEGER   NOT NULL,
    stamp_wall     INTEGER   NOT NULL,
    stamp_counter  INTEGER   NOT NULL,
    call_id        TEXT      NOT NULL,
    kind           TEXT      NOT NULL,
    payload        TEXT      NOT NULL,
    written_at     TIMESTAMP NOT NULL,
    author_kind    TEXT      NOT NULL,
    author_surface TEXT      NOT NULL DEFAULT '',
    author_session TEXT      NOT NULL DEFAULT '',
    archived_at    TIMESTAMP NOT NULL,
    repair_id      TEXT      NOT NULL
);
CREATE INDEX decision_entries_forked_by_repair ON decision_entries_forked (repair_id);

-- Every sync batch reads the held repair entries; there are only ever a few.
CREATE INDEX decision_entries_repairs ON decision_entries (kind) WHERE kind = 'identity_repaired';

-- +goose Down
DROP INDEX IF EXISTS decision_entries_repairs;
DROP TABLE IF EXISTS decision_entries_forked;
DELETE FROM timestamp_repair_progress WHERE table_name = 'decision_entries_forked';
