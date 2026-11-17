-- name: RecordTrackerIntakeHandled :exec
INSERT INTO tracker_intake_handled (project_id, issue_id, session_id, handled_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (project_id, issue_id) DO NOTHING;

-- name: ListTrackerIntakeHandled :many
SELECT project_id, issue_id, session_id, handled_at
FROM tracker_intake_handled
WHERE project_id = ?
ORDER BY handled_at, issue_id;
