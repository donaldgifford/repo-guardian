-- name: LockCheckByKey :one
SELECT id, repository_id, outcome, pending_result FROM checks
WHERE check_key = $1
FOR UPDATE;

-- name: StageCheck :exec
INSERT INTO checks (repository_id, check_key, trigger, outcome, pending_result, policy_version, started_at)
VALUES ($1, $2, $3, 'pending', $4, $5, $6)
ON CONFLICT (check_key) DO UPDATE
SET pending_result = excluded.pending_result
WHERE checks.outcome = 'pending';

-- name: FinalizeCheck :one
-- Inserts the final row, or finalizes a staged one. Returns no row when
-- the key is already final.
INSERT INTO checks (repository_id, check_key, trigger, outcome, error, policy_version, started_at, finished_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (check_key) DO UPDATE
SET outcome = excluded.outcome,
    error = excluded.error,
    pending_result = NULL,
    policy_version = excluded.policy_version,
    finished_at = excluded.finished_at
WHERE checks.outcome = 'pending'
RETURNING id;

-- name: PruneChecks :execrows
DELETE FROM checks WHERE finished_at < $1 AND outcome <> 'pending';
