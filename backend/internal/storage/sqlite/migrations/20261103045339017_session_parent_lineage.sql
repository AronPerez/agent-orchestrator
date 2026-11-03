-- +goose Up
ALTER TABLE sessions ADD COLUMN parent_session_id TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_sessions_parent ON sessions(parent_session_id) WHERE parent_session_id <> '';
CREATE INDEX idx_messages_sender_created ON conversation_messages(sender_session_id, created_at) WHERE sender_session_id <> '';

-- +goose Down
DROP INDEX idx_messages_sender_created;
DROP INDEX idx_sessions_parent;
ALTER TABLE sessions DROP COLUMN parent_session_id;
