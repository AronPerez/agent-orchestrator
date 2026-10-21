-- Fork compatibility only: receipts for the frozen UI over provider switching.
-- No old saga worker, native-session registry, or failure uploader is restored.

-- name: ReserveForkSwitchLaunch :execrows
UPDATE sessions SET runtime_launch_id = sqlc.arg(launch_id), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND revision = sqlc.arg(expected_revision)
    AND harness = sqlc.arg(expected_harness) AND session_mode = 'tui'
    AND is_terminated = sqlc.arg(expected_terminated) AND runtime_handle_id = '';

-- name: ListForkAgentSwitches :many
SELECT * FROM agent_switches WHERE session_id = ? ORDER BY requested_at DESC, id DESC;

-- name: ListActiveForkAgentSwitches :many
SELECT * FROM agent_switches WHERE state NOT IN ('completed', 'failed');

-- name: InsertForkAgentSwitch :exec
INSERT INTO agent_switches (id, session_id, idempotency_key, request_fingerprint,
    from_harness, target_harness, source_generation_id, requested_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FinishForkAgentSwitch :execrows
UPDATE agent_switches SET state = sqlc.arg(state), error_code = sqlc.arg(error_code), updated_at = sqlc.arg(updated_at),
    target_start_mode = CASE WHEN sqlc.arg(state) = 'completed' AND target_start_mode = '' THEN 'fresh' ELSE target_start_mode END,
    agent_handoff_status = CASE WHEN agent_handoff_status = 'requested' THEN 'unavailable' ELSE agent_handoff_status END
WHERE id = sqlc.arg(id) AND state NOT IN ('completed', 'failed');

-- name: PurgeForkAgentSwitchOutbox :exec
DELETE FROM agent_switch_failure_outbox;

-- name: PurgeForkAgentSwitchReceipts :exec
DELETE FROM agent_switch_failure_receipts;

-- name: PurgeForkAgentSwitchDeliveryState :exec
DELETE FROM agent_switch_failure_delivery_state;

-- name: DisableForkAgentSwitchReporting :exec
UPDATE agent_switch_failure_policy SET enabled = 0, consent_generation = '',
    destination_fingerprint = '', updated_at = CURRENT_TIMESTAMP WHERE singleton = 1;
