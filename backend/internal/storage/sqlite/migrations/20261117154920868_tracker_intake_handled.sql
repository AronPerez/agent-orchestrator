-- +goose Up

CREATE TABLE tracker_intake_handled (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    issue_id TEXT NOT NULL CHECK (trim(issue_id) <> ''),
    -- Deliberately not a sessions foreign key: deleting the session must not
    -- erase proof that this still-open tracker item was already delivered.
    session_id TEXT NOT NULL CHECK (trim(session_id) <> ''),
    handled_at TIMESTAMP NOT NULL,
    PRIMARY KEY (project_id, issue_id)
);

-- +goose Down

DROP TABLE tracker_intake_handled;
