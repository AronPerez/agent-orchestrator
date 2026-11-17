-- +goose Up
ALTER TABLE sessions ADD COLUMN contract_stall_nudged_at TIMESTAMP;
ALTER TABLE sessions ADD COLUMN contract_stall_escalated_at TIMESTAMP;
ALTER TABLE sessions ADD COLUMN contract_timebox_prompted_at TIMESTAMP;

-- +goose Down
ALTER TABLE sessions DROP COLUMN contract_timebox_prompted_at;
ALTER TABLE sessions DROP COLUMN contract_stall_escalated_at;
ALTER TABLE sessions DROP COLUMN contract_stall_nudged_at;
