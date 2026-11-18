-- name: NextDecisionOriginSequence :one
SELECT COALESCE(MAX(origin_seq), 0) + 1
FROM decision_entries
WHERE origin = ?;

-- name: NewestDecisionStamp :one
SELECT stamp_wall, stamp_counter
FROM decision_entries
ORDER BY stamp_wall DESC, stamp_counter DESC, origin DESC, id DESC
LIMIT 1;

-- name: CreateDecisionEntry :one
INSERT INTO decision_entries (
    id, origin, origin_seq, stamp_wall, stamp_counter, call_id, kind, payload,
    written_at, author_kind, author_surface, author_session
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListDecisionEntries :many
SELECT *
FROM decision_entries
WHERE call_id = ?
ORDER BY stamp_wall, stamp_counter, origin, id;

-- name: UpsertDecisionCall :one
INSERT INTO decision_calls (
    call_id, origin, project_id, stream_session_id, stream_name,
    owner_session_id, state, delivery, retired, raised_at,
    last_wall, last_counter, body, search_text, outcome_at
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(call_id) DO UPDATE SET
    origin = excluded.origin,
    project_id = excluded.project_id,
    stream_session_id = excluded.stream_session_id,
    stream_name = excluded.stream_name,
    owner_session_id = excluded.owner_session_id,
    state = excluded.state,
    delivery = excluded.delivery,
    retired = excluded.retired,
    raised_at = excluded.raised_at,
    last_wall = excluded.last_wall,
    last_counter = excluded.last_counter,
    body = excluded.body,
    search_text = excluded.search_text,
    outcome_at = excluded.outcome_at
RETURNING *;

-- name: GetDecisionCall :one
SELECT * FROM decision_calls WHERE call_id = ?;

-- name: ListDecisionCallsForSession :many
SELECT *
FROM decision_calls
WHERE origin = sqlc.arg(origin)
  AND (
    owner_session_id = sqlc.arg(session_id)
    OR json_extract(body, '$.deliveredTo') = sqlc.arg(session_id)
  )
ORDER BY raised_at DESC, call_id DESC
LIMIT sqlc.arg(result_limit);

-- name: ListDecisionCallsByState :many
SELECT *
FROM decision_calls
WHERE state = ?
ORDER BY raised_at, call_id
LIMIT ?;

-- name: ListDecisionCallsForBoard :many
-- Every active call plus non-retired Done calls whose outcome landed at or
-- after done_since. Grouping and display order belong to the client.
SELECT *
FROM decision_calls
WHERE state IN ('needs_you', 'in_progress')
   OR (state = 'done' AND retired = 0 AND outcome_at >= sqlc.arg(done_since))
ORDER BY raised_at, call_id;

-- name: ListDecisionEntriesAwaitingDelivery :many
SELECT pending.*
FROM decision_entries AS pending
JOIN decision_calls AS calls ON calls.call_id = pending.call_id
WHERE calls.origin = ?
  AND pending.kind IN ('answered', 'dismissed')
  AND NOT EXISTS (
      SELECT 1
      FROM decision_entries AS delivered
      WHERE delivered.call_id = pending.call_id
        AND delivered.kind = 'delivered'
        AND CASE
            WHEN json_valid(delivered.payload)
            THEN json_extract(delivered.payload, '$.forEntry') = pending.id
            ELSE 0
        END
  )
ORDER BY pending.stamp_wall, pending.stamp_counter, pending.origin, pending.id;

-- name: ListDecisionStreams :many
SELECT current.origin, current.stream_session_id, current.stream_name
FROM decision_calls AS current
WHERE current.stream_session_id <> ''
  AND current.call_id = (
      SELECT latest.call_id
      FROM decision_calls AS latest
      JOIN decision_entries AS raised
        ON raised.call_id = latest.call_id AND raised.kind = 'raised'
      WHERE latest.origin = current.origin
        AND latest.stream_session_id = current.stream_session_id
      ORDER BY raised.stamp_wall DESC, raised.stamp_counter DESC, raised.origin DESC, raised.id DESC
      LIMIT 1
  )
ORDER BY current.origin, current.stream_session_id;
