-- +goose Up
ALTER TABLE reports ADD COLUMN verification_workspace_revision TEXT NOT NULL DEFAULT '';
ALTER TABLE reports ADD COLUMN verification_interaction_at TIMESTAMP;

-- +goose Down
ALTER TABLE reports DROP COLUMN verification_interaction_at;
ALTER TABLE reports DROP COLUMN verification_workspace_revision;
