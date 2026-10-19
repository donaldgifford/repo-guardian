-- The remediator's queries (IMPL-0030 Phase 1), run as rg_remediator.

-- name: LockPolicyStateForSlot :one
-- The PR cap's serialization point (DESIGN-0032 AR-0032-05): the
-- remediator may UPDATE only last_remediation_at here, which is what
-- lets it take the row lock.
SELECT max_open_prs FROM repository_policy_state WHERE repository_id = @repository_id FOR UPDATE;

-- name: CountSlots :one
SELECT count(*)::int FROM remediations WHERE repository_id = @repository_id AND state IN ('reserved', 'open');

-- name: InsertReservation :one
-- The run's intent (base_sha, branch, expected_head) is written with the
-- reservation, so it is committed before any external step (IMPL-0030
-- 4.4).
INSERT INTO remediations (
    repository_id, control_id, assignment_epoch, operation_key, kind, state, intent_revision,
    base_sha, branch, expected_head, reserved_until, remediated_generation, created_at, last_run_at)
VALUES (
    @repository_id, @control_id, @assignment_epoch, @operation_key, 'pull_request', 'reserved', @intent_revision,
    sqlc.narg(base_sha), sqlc.narg(branch), sqlc.narg(expected_head),
    now() + make_interval(secs => @ttl_seconds::float8), @remediated_generation, now(), now())
ON CONFLICT (operation_key) DO NOTHING
RETURNING id;

-- name: InsertAPIRemediation :exec
-- An API remediation (IMPL-0030 Phase 5): a recommendation, or a direct
-- apply written failed until every step is done, so a crash at any point
-- leaves a resumable row. It holds the one-open slot, never the PR cap.
INSERT INTO remediations (
    repository_id, control_id, assignment_epoch, operation_key, kind, state, intent_revision,
    proposal, remediated_generation, created_at, last_run_at)
VALUES (
    @repository_id, @control_id, @assignment_epoch, @operation_key, 'api', @state, @intent_revision,
    @proposal, @remediated_generation, now(), now())
ON CONFLICT (operation_key) DO NOTHING;

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

-- name: ExpireReservations :many
-- The remediator's one DELETE (RLS remediations_expire): a reservation
-- whose run never opened its pull request frees the slot. One org per
-- freed reservation, for remediation_reservations_expired_total.
DELETE FROM remediations m
WHERE m.state = 'reserved' AND m.reserved_until < @now::timestamptz
RETURNING (SELECT r.org FROM repositories r WHERE r.id = m.repository_id)::text AS org;

-- name: TrackedPullRequests :many
-- The evaluator's observation set (DESIGN-0032 D24): open remediation
-- pull requests, matched by number.
SELECT id, control_id, pr_number::int AS pr_number FROM remediations
WHERE repository_id = @repository_id AND kind = 'pull_request' AND state = 'open' AND pr_number IS NOT NULL
ORDER BY id;

-- name: ObserveOpenPR :one
-- The FROM subquery reads the row as it was, so moved says whether the
-- head changed since the last observation.
UPDATE remediations r SET observed_head_sha = @head_sha, observed_at = now()
FROM (SELECT q.id, q.observed_head_sha AS prev FROM remediations q WHERE q.id = @id) p
WHERE r.id = p.id AND r.state = 'open'
RETURNING (p.prev IS DISTINCT FROM @head_sha::text)::bool AS moved;

-- name: ObserveMergedPR :execrows
UPDATE remediations SET state = 'merged', closed_at = @closed_at::timestamptz, observed_at = now()
WHERE id = @id AND state = 'open';

-- name: ObserveClosedPR :execrows
-- Closed unmerged by a user: hold the slot's control until a newer
-- generation or reopen_after passes (closed_generation, next_eligible_at).
WITH v AS (SELECT @closed_at::timestamptz AS closed_at)
UPDATE remediations r SET
    state             = 'closed_by_user',
    closed_at         = v.closed_at,
    observed_at       = now(),
    closed_generation = COALESCE(
        (SELECT cr.eval_generation FROM control_results cr
         WHERE cr.repository_id = r.repository_id AND cr.control_id = r.control_id),
        r.remediated_generation),
    next_eligible_at  = v.closed_at + ps.reopen_after
FROM repository_policy_state ps, v
WHERE ps.repository_id = r.repository_id AND r.id = @id AND r.state = 'open';

-- name: DueRemediationsForRepository :many
-- What one evaluation hands to StartDueRemediations, in policy order.
SELECT repository_id, control_id, ordinal FROM remediation_due
WHERE repository_id = @repository_id
ORDER BY ordinal;

-- name: RemediationTarget :one
-- What a remediation run reads before acting and what its epoch fence
-- re-reads before every write (IMPL-0030 3.5, 4.1): the assignment,
-- whether the Remediation App can reach the repository, the evaluator's
-- result, the newest remediation's observed head, and whether the row
-- is in remediation_due. No assignment row means the control was
-- withdrawn.
SELECT r.org, r.name, r.active, COALESCE(r.provider_repo_id, 0)::bigint AS provider_repo_id,
       a.epoch, a.state, a.effective_mode, a.revision, a.control_version,
       COALESCE(acc.installation_id, 0)::bigint AS installation_id,
       (acc.installation_id IS NOT NULL AND i.suspended_at IS NULL)::bool AS remediable,
       COALESCE(i.account_type, '')::text AS account_type,
       (cr.repository_id IS NOT NULL)::bool AS has_result,
       COALESCE(cr.assignment_epoch, 0)::bigint AS result_epoch,
       COALESCE(cr.eval_generation, 0)::bigint AS eval_generation,
       COALESCE(cr.intent_revision, '')::text AS intent_revision,
       COALESCE(cr.status, '')::text AS status,
       COALESCE(cr.hold, '')::text AS hold,
       COALESCE((SELECT m.observed_head_sha FROM remediations m
                 WHERE m.repository_id = r.id AND m.control_id = a.control_id
                 ORDER BY m.id DESC LIMIT 1), '')::text AS observed_head_sha,
       EXISTS (SELECT 1 FROM remediation_due d
               WHERE d.repository_id = r.id AND d.control_id = a.control_id)::bool AS due
FROM repositories r
JOIN control_assignments a ON a.repository_id = r.id AND a.control_id = @control_id
LEFT JOIN app_repository_access acc ON acc.repository_id = r.id AND acc.app = 'remediate'
LEFT JOIN app_installations i ON i.app = acc.app AND i.installation_id = acc.installation_id
LEFT JOIN control_results cr ON cr.repository_id = r.id AND cr.control_id = a.control_id
WHERE r.id = @repository_id;

-- name: TouchLastRemediation :exec
-- The one column the remediator may update on repository_policy_state.
UPDATE repository_policy_state SET last_remediation_at = now() WHERE repository_id = @repository_id;

-- name: MaintenanceTarget :one
-- One maintenance candidate as the cleanup needs it (IMPL-0030 6.2,
-- 6.3): the row, the repository and the Remediation App's reach, and
-- when the reason for the cleanup was observed: the result turning
-- compliant, else the assignment's (or the repository's) last
-- resolution, which is when a withdrawal, exclusion or downgrade landed.
SELECT m.id, m.repository_id, m.control_id, m.kind, m.state, m.operation_key, m.assignment_epoch,
       COALESCE(m.pr_number, 0)::int AS pr_number,
       r.org, r.name,
       COALESCE(acc.installation_id, 0)::bigint AS installation_id,
       (acc.installation_id IS NOT NULL AND i.suspended_at IS NULL)::bool AS remediable,
       COALESCE(CASE WHEN cr.status = 'compliant' THEN cr.last_changed_at END,
                ca.resolved_at, ps.resolved_at, m.last_run_at)::timestamptz AS observed_at
FROM remediations m
JOIN repositories r ON r.id = m.repository_id
LEFT JOIN control_assignments ca ON (ca.repository_id, ca.control_id) = (m.repository_id, m.control_id)
LEFT JOIN repository_policy_state ps ON ps.repository_id = m.repository_id
LEFT JOIN control_results cr ON (cr.repository_id, cr.control_id) = (m.repository_id, m.control_id)
LEFT JOIN app_repository_access acc ON acc.repository_id = m.repository_id AND acc.app = 'remediate'
LEFT JOIN app_installations i ON i.app = 'remediate' AND i.installation_id = acc.installation_id
WHERE m.id = @id;

-- name: UpsertRemediateInstallation :exec
-- The Remediation App's discovery refreshes its own installation rows
-- (RLS app_installations_remediate). Suspension is left to the
-- lifecycle events, which the listing does not carry.
INSERT INTO app_installations (app, installation_id, account_login, account_type, repository_selection, seen_at)
VALUES ('remediate', @installation_id, @account_login, @account_type, @repository_selection, now())
ON CONFLICT (app, installation_id) DO UPDATE SET
    account_login = EXCLUDED.account_login,
    account_type = EXCLUDED.account_type,
    repository_selection = EXCLUDED.repository_selection,
    seen_at = EXCLUDED.seen_at;

-- name: RefreshRemediateAccess :many
-- Sets one installation's access rows to exactly the known repositories
-- its listing names and returns the repositories whose access appeared
-- or went (IMPL-0030 6.4). An unknown repository is the evaluator's to
-- discover first.
WITH wanted AS (
    SELECT id FROM repositories WHERE provider_repo_id = ANY (@provider_repo_ids::bigint[])
), removed AS (
    DELETE FROM app_repository_access a
    WHERE a.app = 'remediate' AND a.installation_id = @installation_id
      AND a.repository_id NOT IN (SELECT id FROM wanted)
    RETURNING a.repository_id
), added AS (
    INSERT INTO app_repository_access (repository_id, app, installation_id, seen_at)
    SELECT id, 'remediate', @installation_id, now() FROM wanted
    ON CONFLICT (repository_id, app) DO UPDATE SET installation_id = EXCLUDED.installation_id, seen_at = EXCLUDED.seen_at
    RETURNING repository_id, (xmax = 0) AS inserted
)
SELECT COALESCE(r.provider_repo_id, 0)::bigint AS provider_repo_id, r.org, r.name FROM repositories r
WHERE r.id IN (SELECT repository_id FROM removed UNION SELECT repository_id FROM added WHERE inserted)
ORDER BY r.id;

-- name: PruneRemediateInstallations :many
-- Deletes the Remediation App's installations a complete listing no
-- longer has (their access rows cascade) and returns the repositories
-- that lost access.
WITH gone AS (
    DELETE FROM app_installations
    WHERE app = 'remediate' AND NOT (installation_id = ANY (@seen::bigint[]))
    RETURNING installation_id
)
SELECT COALESCE(r.provider_repo_id, 0)::bigint AS provider_repo_id, r.org, r.name
FROM app_repository_access a
JOIN repositories r ON r.id = a.repository_id
WHERE a.app = 'remediate' AND a.installation_id IN (SELECT installation_id FROM gone)
ORDER BY r.id;
