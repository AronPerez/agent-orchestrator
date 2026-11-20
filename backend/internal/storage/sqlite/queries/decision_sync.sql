-- Decision ledger replication. decision_entries stays the only truth; these
-- queries read it by origin and sequence and record sync-only status.

-- name: DecisionVector :many
SELECT origin, CAST(MAX(origin_seq) AS INTEGER) AS max_seq
FROM decision_entries
GROUP BY origin
ORDER BY origin;

-- name: DecisionHeads :many
SELECT e.origin, e.origin_seq, e.id, e.written_at
FROM decision_entries AS e
JOIN (
    SELECT origin, MAX(origin_seq) AS max_seq
    FROM decision_entries
    GROUP BY origin
) AS heads ON heads.origin = e.origin AND heads.max_seq = e.origin_seq
ORDER BY e.origin;

-- name: ListDecisionEntriesForOriginAfter :many
SELECT *
FROM decision_entries
WHERE origin = sqlc.arg(origin)
  AND origin_seq > sqlc.arg(after_seq)
  AND origin_seq < sqlc.arg(before_seq)
ORDER BY origin_seq
LIMIT sqlc.arg(result_limit);

-- name: GetDecisionEntryByID :one
SELECT * FROM decision_entries WHERE id = ?;

-- name: GetDecisionEntryByOriginSeq :one
SELECT * FROM decision_entries WHERE origin = ? AND origin_seq = ?;

-- name: InsertDecisionSyncConflict :execrows
INSERT INTO decision_sync_conflicts (
    origin, origin_seq, local_entry_id, remote_entry_id, peer_host_id, detected_at
)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (origin, origin_seq) DO NOTHING;

-- name: ListDecisionSyncConflicts :many
SELECT *
FROM decision_sync_conflicts
ORDER BY origin, origin_seq;

-- name: AuditDecisionOrigins :many
SELECT origin,
       CAST(COUNT(*) AS INTEGER) AS held,
       CAST(MAX(origin_seq) AS INTEGER) AS max_seq
FROM decision_entries
GROUP BY origin
HAVING COUNT(*) <> MAX(origin_seq) OR MIN(origin_seq) <> 1
ORDER BY origin;

-- name: UpsertDecisionSyncPeer :exec
INSERT INTO decision_sync_peers (host_id, label, url, direction, device_id, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (host_id) DO UPDATE SET
    label = excluded.label,
    url = excluded.url,
    direction = excluded.direction,
    device_id = excluded.device_id;

-- name: RecordInboundDecisionSyncPeer :execrows
INSERT INTO decision_sync_peers (
    host_id, label, direction, created_at, last_attempt_at, last_success_at, peer_protocol
)
VALUES (sqlc.arg(host_id), sqlc.arg(label), 'inbound', sqlc.arg(at), sqlc.arg(at), sqlc.arg(at), sqlc.arg(peer_protocol))
ON CONFLICT (host_id) DO UPDATE SET
    label = excluded.label,
    last_attempt_at = excluded.last_attempt_at,
    last_success_at = excluded.last_success_at,
    peer_protocol = excluded.peer_protocol
WHERE decision_sync_peers.direction = 'inbound';

-- name: RecordDecisionSyncSuccess :execrows
UPDATE decision_sync_peers
SET last_attempt_at = sqlc.arg(at),
    last_success_at = sqlc.arg(at),
    last_error_code = '',
    in_sync = sqlc.arg(in_sync),
    peer_vector = sqlc.arg(peer_vector),
    clock_ahead_ms = sqlc.arg(clock_ahead_ms),
    peer_protocol = sqlc.arg(peer_protocol),
    -- The peer's own name fills an empty label; a name the person chose stays.
    label = CASE WHEN label = '' THEN CAST(sqlc.arg(label) AS TEXT) ELSE label END
WHERE host_id = sqlc.arg(host_id);

-- name: RecordDecisionSyncFailure :execrows
UPDATE decision_sync_peers
SET last_attempt_at = sqlc.arg(at),
    last_error_code = sqlc.arg(error_code),
    in_sync = 0
WHERE host_id = sqlc.arg(host_id);

-- name: ListDecisionSyncPeers :many
SELECT * FROM decision_sync_peers ORDER BY host_id;

-- name: GetDecisionSyncPeer :one
SELECT * FROM decision_sync_peers WHERE host_id = ?;

-- name: DeleteDecisionSyncPeer :execrows
DELETE FROM decision_sync_peers WHERE host_id = ?;

-- name: DecisionOriginLabels :many
SELECT calls.origin,
       CAST(COALESCE(json_extract(calls.body, '$.raised.machineLabel'), '') AS TEXT) AS machine_label
FROM decision_calls AS calls
JOIN (
    SELECT origin, MAX(raised_at) AS newest
    FROM decision_calls
    GROUP BY origin
) AS latest ON latest.origin = calls.origin AND latest.newest = calls.raised_at
ORDER BY calls.origin;

-- name: ListDecisionOriginIDs :many
SELECT origin_seq, id FROM decision_entries
WHERE origin = sqlc.arg(origin) AND origin_seq >= sqlc.arg(from_seq)
ORDER BY origin_seq
LIMIT sqlc.arg(result_limit);

-- name: ListDecisionRepairEntries :many
SELECT * FROM decision_entries
WHERE kind = 'identity_repaired'
ORDER BY origin, origin_seq;

-- name: ArchiveDecisionEntry :execrows
-- Copies the row byte for byte; a row archived before stays as it was.
INSERT OR IGNORE INTO decision_entries_forked (
    id, origin, origin_seq, stamp_wall, stamp_counter, call_id, kind, payload,
    written_at, author_kind, author_surface, author_session, archived_at, repair_id
)
SELECT id, origin, origin_seq, stamp_wall, stamp_counter, call_id, kind, payload,
       written_at, author_kind, author_surface, author_session,
       sqlc.arg(archived_at), sqlc.arg(repair_id)
FROM decision_entries
WHERE decision_entries.id = sqlc.arg(id);

-- name: RemoveArchivedDecisionEntry :execrows
-- Only a row the archive already holds leaves the log.
DELETE FROM decision_entries
WHERE decision_entries.id = sqlc.arg(id)
  AND EXISTS (SELECT 1 FROM decision_entries_forked AS f WHERE f.id = decision_entries.id);

-- name: ListArchivedDecisionEntries :many
SELECT * FROM decision_entries_forked
WHERE repair_id = ?
ORDER BY origin, origin_seq;

-- name: DeleteDecisionSyncConflictsForOrigin :execrows
DELETE FROM decision_sync_conflicts WHERE origin = ?;

-- name: DeleteDecisionCall :execrows
DELETE FROM decision_calls WHERE call_id = ?;
