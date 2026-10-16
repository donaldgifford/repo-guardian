-- name: StageCheck :exec
-- A retried evaluation replaces its staged payload until the record step
-- finalizes the row.
INSERT INTO checks (repository_id, check_key, trigger, outcome, pending_result, policy_version, started_at)
VALUES (@repository_id, @check_key, @trigger, 'pending', @pending_result, @policy_version, @started_at)
ON CONFLICT (check_key) DO UPDATE SET pending_result = EXCLUDED.pending_result, policy_version = EXCLUDED.policy_version
WHERE checks.outcome = 'pending';

-- name: LockCheck :one
SELECT c.id, c.repository_id, c.outcome, c.pending_result, r.org
FROM checks c JOIN repositories r ON r.id = c.repository_id
WHERE c.check_key = @check_key
FOR UPDATE OF c;

-- name: FinishCheck :exec
UPDATE checks SET outcome = @outcome, error = @error, pending_result = NULL, finished_at = now() WHERE id = @id;

-- name: AssignmentFence :one
-- The record step's fence (DESIGN-0030 D14): held until commit, so a
-- concurrent resolution cannot move the epoch under it.
SELECT state, epoch, effective_mode FROM control_assignments
WHERE repository_id = @repository_id AND control_id = @control_id
FOR SHARE;

-- name: LockResult :one
SELECT assignment_epoch, status, fingerprint, eval_generation, last_changed_at FROM control_results
WHERE repository_id = @repository_id AND control_id = @control_id
FOR UPDATE;

-- name: UpsertResult :exec
-- Evaluation's columns only (record-is-narrow): remediated_generation,
-- hold and hold_since are the remediator's.
INSERT INTO control_results (
    repository_id, control_id, control_version, revision, assignment_epoch, status, fingerprint, intent_revision,
    eval_generation, evaluated_sha, evaluated_at, last_changed_at)
VALUES (
    @repository_id, @control_id, @control_version, @revision, @assignment_epoch, @status, @fingerprint, @intent_revision,
    @eval_generation, @evaluated_sha, now(), @last_changed_at)
ON CONFLICT (repository_id, control_id) DO UPDATE SET
    control_version = EXCLUDED.control_version,
    revision = EXCLUDED.revision,
    assignment_epoch = EXCLUDED.assignment_epoch,
    status = EXCLUDED.status,
    fingerprint = EXCLUDED.fingerprint,
    intent_revision = EXCLUDED.intent_revision,
    eval_generation = EXCLUDED.eval_generation,
    evaluated_sha = EXCLUDED.evaluated_sha,
    evaluated_at = EXCLUDED.evaluated_at,
    last_changed_at = EXCLUDED.last_changed_at;

-- name: ListRuleStatuses :many
SELECT rule_id, status FROM rule_results WHERE repository_id = @repository_id AND control_id = @control_id;

-- name: UpsertRuleResult :exec
INSERT INTO rule_results (repository_id, control_id, rule_id, status, remediate, evidence, evidence_kind, evidence_version)
VALUES (@repository_id, @control_id, @rule_id, @status, @remediate, @evidence, @evidence_kind, @evidence_version)
ON CONFLICT (repository_id, control_id, rule_id) DO UPDATE SET
    status = EXCLUDED.status,
    remediate = EXCLUDED.remediate,
    evidence = EXCLUDED.evidence,
    evidence_kind = EXCLUDED.evidence_kind,
    evidence_version = EXCLUDED.evidence_version;

-- name: DeleteRuleResultsNotIn :exec
DELETE FROM rule_results
WHERE repository_id = @repository_id AND control_id = @control_id AND NOT (rule_id = ANY (@keep::text[]));

-- name: EvaluationTarget :one
-- One repository as the evaluator sees it: identity, policy state and
-- the Evaluation App's installation. The LEFT JOINs leave a repository
-- with no resolution or no access row readable.
SELECT r.org, r.name, r.active, r.park_reason,
       ps.state AS policy_state, ps.policy_version,
       a.installation_id, i.account_type
FROM repositories r
LEFT JOIN repository_policy_state ps ON ps.repository_id = r.id
LEFT JOIN app_repository_access a ON a.repository_id = r.id AND a.app = 'eval'
LEFT JOIN app_installations i ON i.app = 'eval' AND i.installation_id = a.installation_id
WHERE r.id = @id;
