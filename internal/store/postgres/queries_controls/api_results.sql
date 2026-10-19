-- API results (DESIGN-0037 § API, IMPL-0030 7.7): the cross-repository
-- list of recorded control results behind the Findings page. The
-- population and the bucket are ControlCompliance's (compliance.sql):
-- managed repositories plus those parked for access loss, bucket by the
-- same CASE, so bucket=non_compliant's total is the summary's
-- non_compliant by construction (OQ15). A result exists, so pending
-- never appears. Every query carries the scope predicate;
-- TestAPIQueries_AreScoped enforces it.

-- name: APIResults :many
-- One page, keyed by (last_changed_at, repository_id, control_id):
-- ascending by default (oldest change first), descending when
-- @descending.
-- failing_rules and reason come from the rule results that fail or
-- error, in rule id order; reason is the first one's evidence reason,
-- else its evidence_kind.
SELECT x.repository_id, x.org, x.name, x.control_id, x.control_version, x.decision, x.status, x.bucket,
       x.last_changed_at, coalesce(f.failing, '{}')::text[] AS failing_rules, coalesce(f.reason, '')::text AS reason
FROM (
    SELECT r.id AS repository_id, r.org, r.name, cr.control_id, cr.control_version, ca.decision, cr.status,
           (CASE
             WHEN ca.state = 'excluded'                              THEN 'excluded'
             WHEN ca.state = 'conflict'                              THEN 'unmeasurable_conflict'
             WHEN ps.state = 'parked'                                THEN 'unmeasurable_access_denied'
             WHEN EXISTS (SELECT 1 FROM app_repository_access a
                          JOIN app_installations i ON (i.app, i.installation_id) = (a.app, a.installation_id)
                          WHERE a.repository_id = r.id AND a.app = 'eval' AND i.suspended_at IS NOT NULL)
                                                                     THEN 'unmeasurable_suspended'
             WHEN cr.assignment_epoch <> ca.epoch OR cr.revision <> ca.revision THEN 'stale'
             ELSE cr.status
           END)::text AS bucket,
           cr.last_changed_at
    FROM control_results cr
    JOIN control_assignments ca ON (ca.repository_id, ca.control_id) = (cr.repository_id, cr.control_id)
    JOIN repositories r ON r.id = cr.repository_id
    JOIN repository_policy_state ps ON ps.repository_id = cr.repository_id
    WHERE (ps.state = 'managed' OR (ps.state = 'parked' AND r.park_reason IN ('access_denied', 'unknown')))
      AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
      AND (sqlc.narg(org)::text IS NULL OR lower(r.org) = lower(sqlc.narg(org)::text))
      AND (sqlc.narg(control_id)::text IS NULL OR cr.control_id = sqlc.narg(control_id)::text)
      AND (sqlc.narg(decision)::text IS NULL OR ca.decision = sqlc.narg(decision)::text)
      AND (sqlc.narg(rule_id)::text IS NULL
           OR EXISTS (SELECT 1 FROM rule_results rr
                      WHERE (rr.repository_id, rr.control_id) = (cr.repository_id, cr.control_id)
                        AND rr.rule_id = sqlc.narg(rule_id)::text AND rr.status IN ('fail', 'error')))
) x
LEFT JOIN LATERAL (
    SELECT array_agg(rr.rule_id ORDER BY rr.rule_id)::text[] AS failing,
           (array_agg(coalesce(rr.evidence->>'reason', rr.evidence_kind) ORDER BY rr.rule_id))[1] AS reason
    FROM rule_results rr
    WHERE (rr.repository_id, rr.control_id) = (x.repository_id, x.control_id) AND rr.status IN ('fail', 'error')
) f ON true
WHERE (sqlc.narg(bucket)::text IS NULL OR x.bucket = sqlc.narg(bucket)::text)
  AND (NOT @has_after::bool
       OR (@descending::bool AND (x.last_changed_at, x.repository_id, x.control_id)
                           < (@after_at::timestamptz, @after_repository_id::bigint, @after_control_id::text))
       OR (NOT @descending::bool AND (x.last_changed_at, x.repository_id, x.control_id)
                               > (@after_at::timestamptz, @after_repository_id::bigint, @after_control_id::text)))
ORDER BY CASE WHEN @descending::bool THEN x.last_changed_at END DESC,
         CASE WHEN @descending::bool THEN x.repository_id END DESC,
         CASE WHEN @descending::bool THEN x.control_id END DESC,
         x.last_changed_at, x.repository_id, x.control_id
LIMIT @page_limit;

-- name: APIResultsTotal :one
-- How many results APIResults' filters match, for the first page only
-- (OQ16).
SELECT count(*)::int AS total
FROM (
    SELECT (CASE
             WHEN ca.state = 'excluded'                              THEN 'excluded'
             WHEN ca.state = 'conflict'                              THEN 'unmeasurable_conflict'
             WHEN ps.state = 'parked'                                THEN 'unmeasurable_access_denied'
             WHEN EXISTS (SELECT 1 FROM app_repository_access a
                          JOIN app_installations i ON (i.app, i.installation_id) = (a.app, a.installation_id)
                          WHERE a.repository_id = r.id AND a.app = 'eval' AND i.suspended_at IS NOT NULL)
                                                                     THEN 'unmeasurable_suspended'
             WHEN cr.assignment_epoch <> ca.epoch OR cr.revision <> ca.revision THEN 'stale'
             ELSE cr.status
           END)::text AS bucket
    FROM control_results cr
    JOIN control_assignments ca ON (ca.repository_id, ca.control_id) = (cr.repository_id, cr.control_id)
    JOIN repositories r ON r.id = cr.repository_id
    JOIN repository_policy_state ps ON ps.repository_id = cr.repository_id
    WHERE (ps.state = 'managed' OR (ps.state = 'parked' AND r.park_reason IN ('access_denied', 'unknown')))
      AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
      AND (sqlc.narg(org)::text IS NULL OR lower(r.org) = lower(sqlc.narg(org)::text))
      AND (sqlc.narg(control_id)::text IS NULL OR cr.control_id = sqlc.narg(control_id)::text)
      AND (sqlc.narg(decision)::text IS NULL OR ca.decision = sqlc.narg(decision)::text)
      AND (sqlc.narg(rule_id)::text IS NULL
           OR EXISTS (SELECT 1 FROM rule_results rr
                      WHERE (rr.repository_id, rr.control_id) = (cr.repository_id, cr.control_id)
                        AND rr.rule_id = sqlc.narg(rule_id)::text AND rr.status IN ('fail', 'error')))
) x
WHERE sqlc.narg(bucket)::text IS NULL OR x.bucket = sqlc.narg(bucket)::text;
