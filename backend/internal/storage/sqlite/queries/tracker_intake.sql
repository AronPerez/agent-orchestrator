-- name: RecordTrackerIntakeHandled :exec
INSERT INTO tracker_intake_handled (project_id, issue_id, session_id, state, recorded_at)
VALUES (?, ?, ?, 'handled', ?)
ON CONFLICT (project_id, issue_id) DO UPDATE SET
    state = 'handled',
    recorded_at = excluded.recorded_at
WHERE tracker_intake_handled.state = 'pending'
  AND tracker_intake_handled.session_id = excluded.session_id;

-- name: ListTrackerIntakeHandled :many
SELECT project_id, issue_id, session_id, recorded_at
FROM tracker_intake_handled
WHERE project_id = ? AND state = 'handled'
ORDER BY recorded_at, issue_id;

-- name: GetTrackerIntakeClaim :one
SELECT project_id, issue_id, session_id, state, recorded_at
FROM tracker_intake_handled
WHERE project_id = ? AND issue_id = ?;

-- name: GetTrackerIntakeClaimBySession :one
SELECT project_id, issue_id, session_id, state, recorded_at
FROM tracker_intake_handled
WHERE session_id = ?;

-- name: ListPendingTrackerIntakeClaims :many
SELECT project_id, issue_id, session_id, state, recovery_state, recorded_at
FROM tracker_intake_handled
WHERE project_id = ? AND state = 'pending'
ORDER BY recorded_at, issue_id;

-- name: ListAllPendingTrackerIntakeClaims :many
SELECT project_id, issue_id, session_id, recovery_state, recorded_at
FROM tracker_intake_handled
WHERE state = 'pending'
ORDER BY recorded_at, project_id, issue_id;

-- name: ListTrackerIntakeClaims :many
SELECT project_id, issue_id, session_id, state, recorded_at
FROM tracker_intake_handled
WHERE project_id = ?
ORDER BY recorded_at, issue_id;

-- name: CountLiveIssueLinkedSessions :one
SELECT COUNT(*)
FROM (
    SELECT id AS session_id
    FROM sessions
    WHERE sessions.project_id = sqlc.arg(project_id)
      AND issue_id IS NOT NULL
      AND trim(issue_id) <> ''
      AND is_terminated = FALSE
      AND NOT EXISTS (
          SELECT 1
          FROM tracker_intake_handled AS released_claim
          WHERE released_claim.session_id = sessions.id
            AND released_claim.state = 'pending'
            AND COALESCE(json_extract(released_claim.recovery_state, '$.capacityReleased'), 0) = 1
      )
    UNION
    SELECT session_id
    FROM tracker_intake_handled
    WHERE tracker_intake_handled.project_id = sqlc.arg(project_id)
      AND state = 'pending'
      AND COALESCE(json_extract(recovery_state, '$.capacityReleased'), 0) = 0
);

-- name: ListLiveTrackerIssueReferences :many
SELECT issue_id
FROM sessions
WHERE project_id = ?
  AND issue_id IS NOT NULL
  AND trim(issue_id) <> ''
  AND is_terminated = FALSE;

-- name: SetPendingTrackerIntakeRecovery :execrows
UPDATE tracker_intake_handled
SET recovery_state = ?
WHERE session_id = ? AND state = 'pending';

-- name: GetPendingTrackerIntakeRecovery :one
SELECT recovery_state
FROM tracker_intake_handled
WHERE session_id = ? AND state = 'pending';

-- name: InsertTrackerIntakeClaim :exec
INSERT INTO tracker_intake_handled (project_id, issue_id, session_id, state, recorded_at)
VALUES (?, ?, ?, 'pending', ?);

-- name: CommitTrackerIntakeSession :execrows
UPDATE tracker_intake_handled
SET state = 'handled', recorded_at = ?
WHERE session_id = ? AND state = 'pending';

-- name: DeletePendingTrackerIntakeClaim :execrows
DELETE FROM tracker_intake_handled
WHERE session_id = ? AND state = 'pending';
