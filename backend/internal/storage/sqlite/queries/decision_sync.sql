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
