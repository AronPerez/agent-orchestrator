-- +goose Up
ALTER TABLE sessions ADD COLUMN parent_host_id TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_sessions_remote_parent
    ON sessions(parent_host_id, parent_session_id)
    WHERE parent_host_id <> '' AND parent_session_id <> '';

-- +goose Down
DROP INDEX IF EXISTS idx_sessions_remote_parent;
ALTER TABLE sessions DROP COLUMN parent_host_id;
