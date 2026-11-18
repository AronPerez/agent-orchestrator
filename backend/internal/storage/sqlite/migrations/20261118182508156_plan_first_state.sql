-- +goose Up
ALTER TABLE sessions ADD COLUMN plan_first_state TEXT NOT NULL DEFAULT ''
    CHECK (plan_first_state IN ('', 'pending', 'approved', 'declined'));

-- +goose Down
ALTER TABLE sessions DROP COLUMN plan_first_state;
