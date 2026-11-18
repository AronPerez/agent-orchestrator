-- +goose Up
ALTER TABLE pr ADD COLUMN tamper_assessment_json TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE pr DROP COLUMN tamper_assessment_json;
