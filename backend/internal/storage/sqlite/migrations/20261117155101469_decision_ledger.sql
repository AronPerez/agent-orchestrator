-- +goose Up
-- decision_entries is the append-only source of truth; decision_calls and its
-- search index are rebuildable projections. There are deliberately no foreign
-- keys to local sessions or projects because later sync may name remote rows.
CREATE TABLE decision_entries (
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
    UNIQUE (origin, origin_seq)
);
CREATE INDEX decision_entries_by_call ON decision_entries (call_id, stamp_wall, stamp_counter);
CREATE INDEX decision_entries_by_stamp ON decision_entries (stamp_wall, stamp_counter);

CREATE TABLE decision_calls (
    call_id           TEXT      NOT NULL PRIMARY KEY,
    origin            TEXT      NOT NULL,
    project_id        TEXT      NOT NULL DEFAULT '',
    stream_session_id TEXT      NOT NULL DEFAULT '',
    stream_name       TEXT      NOT NULL DEFAULT '',
    owner_session_id  TEXT      NOT NULL,
    state             TEXT      NOT NULL,
    delivery          TEXT      NOT NULL DEFAULT '',
    retired           INTEGER   NOT NULL DEFAULT 0,
    raised_at         TIMESTAMP NOT NULL,
    last_wall         INTEGER   NOT NULL,
    last_counter      INTEGER   NOT NULL,
    body              TEXT      NOT NULL,
    search_text       TEXT      NOT NULL DEFAULT ''
);
CREATE INDEX decision_calls_by_state ON decision_calls (state, raised_at);
CREATE INDEX decision_calls_by_owner ON decision_calls (origin, owner_session_id, raised_at);
CREATE INDEX decision_calls_by_delivery ON decision_calls (origin, delivery);

CREATE VIRTUAL TABLE decision_calls_fts USING fts5(
    search_text,
    content = 'decision_calls',
    content_rowid = 'rowid',
    tokenize = 'porter unicode61'
);

-- +goose StatementBegin
CREATE TRIGGER decision_calls_fts_insert
AFTER INSERT ON decision_calls
BEGIN
    INSERT INTO decision_calls_fts(rowid, search_text) VALUES (new.rowid, new.search_text);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER decision_calls_fts_update
AFTER UPDATE OF search_text ON decision_calls
BEGIN
    INSERT INTO decision_calls_fts(decision_calls_fts, rowid, search_text)
    VALUES ('delete', old.rowid, old.search_text);
    INSERT INTO decision_calls_fts(rowid, search_text) VALUES (new.rowid, new.search_text);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER decision_calls_fts_delete
AFTER DELETE ON decision_calls
BEGIN
    INSERT INTO decision_calls_fts(decision_calls_fts, rowid, search_text)
    VALUES ('delete', old.rowid, old.search_text);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS decision_calls_fts_delete;
DROP TRIGGER IF EXISTS decision_calls_fts_update;
DROP TRIGGER IF EXISTS decision_calls_fts_insert;
DROP TABLE IF EXISTS decision_calls_fts;
DROP TABLE IF EXISTS decision_calls;
DROP TABLE IF EXISTS decision_entries;
