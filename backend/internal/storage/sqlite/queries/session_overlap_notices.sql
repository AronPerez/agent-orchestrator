-- name: ListProjectSessionOverlapNotices :many
SELECT n.session_id, n.peer_session_id, n.notified_at
FROM session_overlap_notices n
JOIN sessions s ON s.id = n.session_id
WHERE s.project_id = ?
ORDER BY n.session_id, n.peer_session_id;

-- name: UpsertSessionOverlapNotice :exec
INSERT INTO session_overlap_notices (session_id, peer_session_id, notified_at)
VALUES (?, ?, ?)
ON CONFLICT (session_id, peer_session_id) DO UPDATE SET
    notified_at = excluded.notified_at;

-- name: DeleteSessionOverlapNotice :exec
DELETE FROM session_overlap_notices
WHERE session_id = ? AND peer_session_id = ?;
