-- The remediator's queries (IMPL-0030 Phase 1), run as rg_remediator.

-- name: LockPolicyStateForSlot :one
-- The PR cap's serialization point (DESIGN-0032 AR-0032-05): the
-- remediator may UPDATE only last_remediation_at here, which is what
-- lets it take the row lock.
SELECT max_open_prs FROM repository_policy_state WHERE repository_id = @repository_id FOR UPDATE;

-- name: CountSlots :one
SELECT count(*)::int FROM remediations WHERE repository_id = @repository_id AND state IN ('reserved', 'open');

-- name: InsertReservation :one
INSERT INTO remediations (
    repository_id, control_id, assignment_epoch, operation_key, kind, state, intent_revision,
    reserved_until, remediated_generation, created_at, last_run_at)
VALUES (
    @repository_id, @control_id, @assignment_epoch, @operation_key, 'pull_request', 'reserved', @intent_revision,
    now() + make_interval(secs => @ttl_seconds::float8), @remediated_generation, now(), now())
ON CONFLICT (operation_key) DO NOTHING
RETURNING id;

-- name: RemediationByOperationKey :one
SELECT * FROM remediations WHERE operation_key = @operation_key;

-- name: ClaimRemediation :one
-- FOR NO KEY UPDATE, not FOR UPDATE: journal inserts take FOR KEY SHARE
-- on this row through their foreign key, and must commit on their own
-- connection while the claim is held.
SELECT * FROM remediations WHERE operation_key = @operation_key FOR NO KEY UPDATE SKIP LOCKED;

-- name: LatestRemediation :one
SELECT * FROM remediations
WHERE repository_id = @repository_id AND control_id = @control_id
ORDER BY id DESC LIMIT 1;

-- name: UpdateRemediation :exec
-- The remediator's columns of a claimed row; the evaluator's
-- observation columns are never written here (DESIGN-0032 AR-0032-01).
UPDATE remediations SET
    state = @state,
    intent_revision = @intent_revision,
    base_sha = @base_sha,
    branch = @branch,
    expected_head = @expected_head,
    reserved_until = @reserved_until,
    pr_number = @pr_number,
    pr_url = @pr_url,
    proposal = @proposal,
    hold = @hold,
    closed_at = @closed_at,
    close_reason = @close_reason,
    last_run_at = now()
WHERE id = @id;

-- name: SetRemediationIntent :exec
UPDATE remediations SET intent_revision = @intent_revision, last_run_at = now() WHERE id = @id;

-- name: AppendRemediationStep :one
-- seq is the next per remediation; one claimed operation writes its
-- journal serially, so the max is stable.
INSERT INTO remediation_steps (
    remediation_id, operation_key, seq, kind, resource_key, blob_sha, committed_sha, pr_number, status,
    before, after, error, occurred_at)
SELECT @remediation_id, @operation_key, COALESCE(max(seq), 0) + 1, @kind, @resource_key, @blob_sha, @committed_sha,
       @pr_number, @status, @before, @after, @error, now()
FROM remediation_steps WHERE remediation_id = @remediation_id
RETURNING seq;

-- name: ListRemediationSteps :many
SELECT * FROM remediation_steps WHERE remediation_id = @remediation_id ORDER BY seq;

-- name: AppendRemediationEvent :exec
INSERT INTO remediation_events (remediation_id, kind, detail, occurred_at) VALUES (@remediation_id, @kind, @detail, now());

-- name: LockResultForRun :one
-- The record step's fence: the remediator holds no UPDATE on
-- control_assignments, so it locks the result row (which withdrawal
-- deletes) and compares epochs under that lock.
SELECT cr.assignment_epoch, ca.epoch AS current_epoch, ca.state AS assignment_state
FROM control_results cr
JOIN control_assignments ca ON ca.repository_id = cr.repository_id AND ca.control_id = cr.control_id
WHERE cr.repository_id = @repository_id AND cr.control_id = @control_id
FOR UPDATE OF cr;

-- name: RecordResultRun :exec
UPDATE control_results
SET remediated_generation = GREATEST(remediated_generation, @generation::bigint), hold = NULL, hold_since = NULL
WHERE repository_id = @repository_id AND control_id = @control_id;

-- name: RecordRemediationRun :execrows
-- acknowledged_head_sha is a compare-and-set: a head the evaluator
-- observed after the run started stays unacknowledged, so it is due.
UPDATE remediations
SET remediated_generation = GREATEST(remediated_generation, @generation::bigint),
    intent_revision       = @intent_revision,
    acknowledged_head_sha = CASE
        WHEN observed_head_sha IS NOT DISTINCT FROM sqlc.narg(processed_head)::text THEN sqlc.narg(processed_head)::text
        ELSE acknowledged_head_sha END,
    hold                  = NULL,
    last_run_at           = now()
WHERE operation_key = @operation_key;

-- name: SetResultHold :execrows
-- hold_since keeps the moment the current hold began.
UPDATE control_results
SET hold       = sqlc.narg(hold)::text,
    hold_since = CASE
        WHEN sqlc.narg(hold)::text IS NULL THEN NULL
        WHEN hold IS NOT DISTINCT FROM sqlc.narg(hold)::text THEN hold_since
        ELSE now() END
WHERE repository_id = @repository_id AND control_id = @control_id AND assignment_epoch = @assignment_epoch;

-- name: SetRemediationPermissionHold :execrows
-- Blocked cleanup after the result row is gone: the hold lands on the
-- control's newest remediation.
UPDATE remediations SET hold = 'permission'
WHERE id = (
    SELECT r.id FROM remediations r
    WHERE r.repository_id = @repository_id AND r.control_id = @control_id
    ORDER BY r.id DESC LIMIT 1
);

-- name: ResultExists :one
SELECT EXISTS (SELECT 1 FROM control_results WHERE repository_id = @repository_id AND control_id = @control_id);

-- name: DueRemediations :many
SELECT repository_id, control_id, ordinal FROM remediation_due
ORDER BY repository_id, ordinal
LIMIT @batch;

-- name: MaintenanceCandidates :many
SELECT remediation_id, repository_id, control_id, kind, state, action::text AS action FROM remediation_maintenance
ORDER BY remediation_id
LIMIT @batch;

-- name: ExpireReservations :execrows
-- The remediator's one DELETE (RLS remediations_expire): a reservation
-- whose run never opened its pull request frees the slot.
DELETE FROM remediations WHERE state = 'reserved' AND reserved_until < @now::timestamptz;
