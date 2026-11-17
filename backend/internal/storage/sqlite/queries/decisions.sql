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
    last_wall, last_counter, body, search_text
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
    search_text = excluded.search_text
RETURNING *;

-- name: GetDecisionCall :one
SELECT * FROM decision_calls WHERE call_id = ?;

-- name: ListDecisionCallsByOwner :many
SELECT *
FROM decision_calls
WHERE origin = ? AND owner_session_id = ?
ORDER BY raised_at DESC, call_id DESC
LIMIT ?;

-- name: ListDecisionCallsByState :many
SELECT *
FROM decision_calls
WHERE state = ?
ORDER BY raised_at, call_id
LIMIT ?;

-- name: ListDecisionCallsAwaitingDelivery :many
SELECT *
FROM decision_calls
WHERE origin = ? AND delivery = 'pending'
ORDER BY raised_at, call_id;

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
