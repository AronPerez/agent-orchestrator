-- +goose Up
ALTER TABLE sessions ADD COLUMN plan_first_state TEXT NOT NULL DEFAULT ''
    CHECK (plan_first_state IN ('', 'pending', 'approved', 'declined'));

ALTER TABLE session_artifacts ADD COLUMN role TEXT NOT NULL DEFAULT ''
    CHECK (role IN ('', 'plan_approval'));

-- +goose Down
ALTER TABLE session_artifacts DROP COLUMN role;
ALTER TABLE sessions DROP COLUMN plan_first_state;
