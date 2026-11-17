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

-- Preserve the pre-feature behavior for upgrades: any issue with a live
-- session was already delivered, even though older builds had no ledger.
INSERT INTO tracker_intake_handled (project_id, issue_id, session_id, handled_at)
SELECT project_id, issue_id, MIN(id), MIN(created_at)
FROM sessions
WHERE project_id IS NOT NULL
  AND trim(project_id) <> ''
  AND issue_id IS NOT NULL
  AND trim(issue_id) <> ''
  AND is_terminated = FALSE
GROUP BY project_id, issue_id;

-- +goose Down

DROP TABLE tracker_intake_handled;
