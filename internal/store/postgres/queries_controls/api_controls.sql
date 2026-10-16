-- API controls views over the controls schema (DESIGN-0032 § API,
-- IMPL-0029 8.2). Fleet and org compliance reuse ControlCompliance in
-- compliance.sql through the reader's scope guard; these are the
-- per-repository, per-version and history reads. Every query carries the
-- scope predicate; TestAPIQueries_AreScoped enforces it.

-- name: APIControlVersions :many
-- The versions and revisions of each control assigned per org and
-- lineage, with how many managed repositories carry each: an org on
-- codeowners@2 through replace sits beside one on @1.
SELECT r.org::text AS org, ca.control_id, ca.decision, ca.control_version, ca.revision, count(*)::int AS repositories
FROM control_assignments ca
JOIN repositories r ON r.id = ca.repository_id
JOIN repository_policy_state ps ON ps.repository_id = ca.repository_id
WHERE ca.state = 'active' AND ps.state = 'managed'
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
  AND (sqlc.narg(control_id)::text IS NULL OR ca.control_id = sqlc.narg(control_id)::text)
GROUP BY r.org, ca.control_id, ca.decision, ca.control_version, ca.revision
ORDER BY lower(r.org), ca.control_id, ca.decision, ca.control_version, ca.revision;

-- name: APIRepositoryControls :many
-- One repository's assignments with their results, in remediation
-- priority order. bucket is ControlCompliance's CASE, so a repository's
-- view and the compliance counts never disagree.
SELECT ca.control_id, ca.control_version, ca.revision, ca.state, ca.decision, ca.baseline_control, ca.source,
       ca.source_block, ca.replaced, ca.excluded_by, ca.reason, ca.error, ca.requested_mode, ca.effective_mode,
       ca.mode_source, ca.mode_reason, ca.epoch,
       (CASE
         WHEN ca.state = 'excluded'                              THEN 'excluded'
         WHEN ca.state = 'conflict'                              THEN 'unmeasurable_conflict'
         WHEN ps.state = 'parked'                                THEN 'unmeasurable_access_denied'
         WHEN EXISTS (SELECT 1 FROM app_repository_access a
                      JOIN app_installations i ON (i.app, i.installation_id) = (a.app, a.installation_id)
                      WHERE a.repository_id = r.id AND a.app = 'eval' AND i.suspended_at IS NOT NULL)
                                                                 THEN 'unmeasurable_suspended'
         WHEN cr.repository_id IS NULL                           THEN 'pending'
         WHEN cr.assignment_epoch <> ca.epoch OR cr.revision <> ca.revision THEN 'stale'
         ELSE cr.status
       END)::text AS bucket,
       cr.status, cr.evaluated_sha, cr.evaluated_at, cr.last_changed_at, cr.hold, cr.hold_since
FROM control_assignments ca
JOIN repositories r ON r.id = ca.repository_id
JOIN repository_policy_state ps ON ps.repository_id = ca.repository_id
LEFT JOIN control_results cr ON (cr.repository_id, cr.control_id) = (ca.repository_id, ca.control_id)
WHERE ca.repository_id = @repository_id
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
ORDER BY ca.ordinal, ca.control_id;

-- name: APIRepositoryRuleResults :many
SELECT rr.control_id, rr.rule_id, rr.status, rr.remediate, rr.evidence
FROM rule_results rr
JOIN repositories r ON r.id = rr.repository_id
WHERE rr.repository_id = @repository_id
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
ORDER BY rr.control_id, rr.rule_id;

-- name: APIComplianceHistory :many
-- Each UTC day's last snapshot per org, control and lineage since @since,
-- oldest first: bounded by days x orgs x controls, whatever the snapshot
-- interval.
SELECT h.org, h.control_id, h.decision, h.snapshot_at, h.compliant, h.non_compliant, h.pending, h.stale,
       h.unmeasurable_access_denied, h.unmeasurable_suspended, h.unmeasurable_conflict, h.not_applicable, h.unknown,
       h.excluded, h.assigned, h.percent, h.coverage
FROM (
    SELECT DISTINCT ON (date_trunc('day', cs.snapshot_at AT TIME ZONE 'UTC'), lower(cs.org), cs.control_id, cs.decision)
           cs.*
    FROM compliance_snapshots cs
    WHERE (@scope_all::bool OR lower(cs.org) = ANY (@scope_orgs::text[]))
      AND cs.snapshot_at >= @since
      AND (sqlc.narg(control_id)::text IS NULL OR cs.control_id = sqlc.narg(control_id)::text)
      AND (sqlc.narg(org)::text IS NULL OR lower(cs.org) = lower(sqlc.narg(org)::text))
    ORDER BY date_trunc('day', cs.snapshot_at AT TIME ZONE 'UTC'), lower(cs.org), cs.control_id, cs.decision,
             cs.snapshot_at DESC
) h
ORDER BY h.snapshot_at, lower(h.org), h.control_id, h.decision;

-- name: APISummaryEvaluations :one
-- Repositories evaluated successfully since @since, and the newest
-- evaluation (the epoch when there is none).
SELECT count(DISTINCT c.repository_id)::int AS evaluated,
       coalesce(max(c.finished_at), 'epoch'::timestamptz)::timestamptz AS last_evaluated_at
FROM checks c
JOIN repositories r ON r.id = c.repository_id
WHERE c.outcome = 'success' AND c.finished_at >= @since
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]));
