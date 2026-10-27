-- name: UpsertPolicyState :exec
INSERT INTO repository_policy_state (repository_id, state, reason, max_open_prs, reopen_after, policy_version, resolved_at)
VALUES (@repository_id, @state, @reason, @max_open_prs, make_interval(secs => @reopen_after_seconds::float8), @policy_version, now())
ON CONFLICT (repository_id) DO UPDATE SET
    state = EXCLUDED.state,
    reason = EXCLUDED.reason,
    max_open_prs = EXCLUDED.max_open_prs,
    reopen_after = EXCLUDED.reopen_after,
    policy_version = EXCLUDED.policy_version,
    resolved_at = EXCLUDED.resolved_at;

-- name: GetPolicyState :one
SELECT state, reason, policy_version FROM repository_policy_state WHERE repository_id = @repository_id;

-- name: ListAssignments :many
SELECT * FROM control_assignments WHERE repository_id = @repository_id ORDER BY ordinal, control_id;

-- name: NextEpoch :one
SELECT nextval('assignment_epoch_seq')::bigint AS epoch;

-- name: UpsertAssignment :exec
INSERT INTO control_assignments (
    repository_id, control_id, control_version, revision, state, decision, baseline_control, source, source_block,
    ordinal, replaced, excluded_by, reason, error, requested_mode, effective_mode, mode_source, mode_reason, epoch,
    policy_version, resolved_at)
VALUES (
    @repository_id, @control_id, @control_version, @revision, @state, @decision, @baseline_control, @source, @source_block,
    @ordinal, @replaced, @excluded_by, @reason, @error, @requested_mode, @effective_mode, @mode_source, @mode_reason, @epoch,
    @policy_version, now())
ON CONFLICT (repository_id, control_id) DO UPDATE SET
    control_version = EXCLUDED.control_version,
    revision = EXCLUDED.revision,
    state = EXCLUDED.state,
    decision = EXCLUDED.decision,
    baseline_control = EXCLUDED.baseline_control,
    source = EXCLUDED.source,
    source_block = EXCLUDED.source_block,
    ordinal = EXCLUDED.ordinal,
    replaced = EXCLUDED.replaced,
    excluded_by = EXCLUDED.excluded_by,
    reason = EXCLUDED.reason,
    error = EXCLUDED.error,
    requested_mode = EXCLUDED.requested_mode,
    effective_mode = EXCLUDED.effective_mode,
    mode_source = EXCLUDED.mode_source,
    mode_reason = EXCLUDED.mode_reason,
    epoch = EXCLUDED.epoch,
    policy_version = EXCLUDED.policy_version,
    resolved_at = EXCLUDED.resolved_at;

-- name: DeleteAssignment :exec
DELETE FROM control_assignments WHERE repository_id = @repository_id AND control_id = @control_id;

-- name: ListResultControls :many
SELECT control_id FROM control_results WHERE repository_id = @repository_id ORDER BY control_id;

-- name: DeleteResult :one
-- Withdrawal (DESIGN-0030 D6): the row goes, cascading to rule_results,
-- and what it said is returned for the to_status = NULL event.
DELETE FROM control_results WHERE repository_id = @repository_id AND control_id = @control_id
RETURNING control_version, revision, assignment_epoch, status, eval_generation;

-- name: InsertResultEvent :exec
INSERT INTO result_events (
    repository_id, control_id, control_version, revision, assignment_epoch, rule_id, from_status, to_status,
    eval_generation, occurred_at)
VALUES (
    @repository_id, @control_id, @control_version, @revision, @assignment_epoch, @rule_id, @from_status, @to_status,
    @eval_generation, now());
