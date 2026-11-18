-- +goose Up
ALTER TABLE reports
    ADD COLUMN verification_state TEXT NOT NULL DEFAULT ''
    CHECK (verification_state IN ('', 'passed', 'failed'));
ALTER TABLE reports ADD COLUMN verification_evidence TEXT NOT NULL DEFAULT '';
ALTER TABLE reports ADD COLUMN verification_started_at TIMESTAMP;
ALTER TABLE reports ADD COLUMN verification_completed_at TIMESTAMP;

-- +goose Down
ALTER TABLE reports DROP COLUMN verification_completed_at;
ALTER TABLE reports DROP COLUMN verification_started_at;
ALTER TABLE reports DROP COLUMN verification_evidence;
ALTER TABLE reports DROP COLUMN verification_state;
