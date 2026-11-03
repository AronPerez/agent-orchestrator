-- shortcut: session-scoped Chat only; add durable recipient facts before including shared/TUI history.
-- name: ListSessionCoordinationMessages :many
WITH outgoing AS (
    SELECT m.id FROM conversation_messages m
    JOIN conversations c ON c.id = m.conversation_id
    WHERE m.sender_session_id = sqlc.arg(session_id) AND m.sender_session_id <> '' AND c.scope = 'session'
    ORDER BY m.created_at DESC, m.id DESC LIMIT 51
), incoming AS (
    SELECT m.id FROM conversations c
    JOIN conversation_messages m ON m.conversation_id = c.id
    WHERE c.session_id = sqlc.arg(session_id) AND m.sender_session_id <> ''
    ORDER BY m.created_at DESC, m.id DESC LIMIT 51
)
SELECT m.id, m.sender_session_id, c.session_id AS target_session_id,
       substr(m.text, 1, 240) AS summary, m.created_at
FROM conversation_messages m
JOIN conversations c ON c.id = m.conversation_id
WHERE m.id IN (SELECT id FROM outgoing UNION SELECT id FROM incoming)
ORDER BY m.created_at DESC, m.id DESC
LIMIT 51;

-- name: ListSessionCoordinationReports :many
SELECT id, session_id, state, delivery_state, substr(CASE WHEN note <> '' THEN note ELSE message END, 1, 240) AS summary, created_at
FROM reports WHERE session_id = ?
ORDER BY created_at DESC, id DESC
LIMIT 51;
