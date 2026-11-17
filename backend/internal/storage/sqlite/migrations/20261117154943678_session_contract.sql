-- +goose Up
ALTER TABLE sessions ADD COLUMN goal TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN timebox_deadline_at TIMESTAMP;
ALTER TABLE sessions ADD COLUMN contract_outcome TEXT NOT NULL DEFAULT ''
    CHECK (contract_outcome IN ('', 'met', 'blocked', 'limit_hit'));

-- +goose Down
ALTER TABLE sessions DROP COLUMN contract_outcome;
ALTER TABLE sessions DROP COLUMN timebox_deadline_at;
ALTER TABLE sessions DROP COLUMN goal;
