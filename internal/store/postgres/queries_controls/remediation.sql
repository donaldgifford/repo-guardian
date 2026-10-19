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
