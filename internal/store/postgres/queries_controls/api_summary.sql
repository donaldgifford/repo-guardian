-- API summary aggregates (DESIGN-0037 § API, IMPL-0030 7.10, 7.11).
-- Every query carries the scope predicate; TestAPIQueries_AreScoped
-- enforces it.

-- name: APIFindingAge :many
-- Summary.finding_age: results in the non-compliant bucket by how long
-- ago they last changed, in six fixed buckets. The population and bucket
-- are ControlCompliance's, so the counts sum to the summary's
-- non_compliant. A bucket with none is absent; the reader fills it.
SELECT (CASE
          WHEN now() - x.last_changed_at < interval '1 day'    THEN '<1d'
          WHEN now() - x.last_changed_at < interval '7 days'   THEN '1-7d'
          WHEN now() - x.last_changed_at < interval '30 days'  THEN '7-30d'
          WHEN now() - x.last_changed_at < interval '90 days'  THEN '30-90d'
          WHEN now() - x.last_changed_at < interval '180 days' THEN '90-180d'
          ELSE '180d+'
        END)::text AS bucket,
       count(*)::int AS findings
FROM (
    SELECT cr.last_changed_at,
           CASE
             WHEN ca.state = 'excluded'                              THEN 'excluded'
             WHEN ca.state = 'conflict'                              THEN 'unmeasurable_conflict'
             WHEN ps.state = 'parked'                                THEN 'unmeasurable_access_denied'
             WHEN EXISTS (SELECT 1 FROM app_repository_access a
                          JOIN app_installations i ON (i.app, i.installation_id) = (a.app, a.installation_id)
                          WHERE a.repository_id = r.id AND a.app = 'eval' AND i.suspended_at IS NOT NULL)
                                                                     THEN 'unmeasurable_suspended'
             WHEN cr.assignment_epoch <> ca.epoch OR cr.revision <> ca.revision THEN 'stale'
             ELSE cr.status
           END AS posture_bucket
    FROM control_results cr
    JOIN control_assignments ca ON (ca.repository_id, ca.control_id) = (cr.repository_id, cr.control_id)
    JOIN repositories r ON r.id = cr.repository_id
    JOIN repository_policy_state ps ON ps.repository_id = cr.repository_id
    WHERE (ps.state = 'managed' OR (ps.state = 'parked' AND r.park_reason IN ('access_denied', 'unknown')))
      AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
) x
WHERE x.posture_bucket = 'non_compliant'
GROUP BY 1;

-- name: APIOpenPRAge :many
-- Summary.open_prs_by_age: open repo-guardian pull requests by
-- created_at, over the same active repositories as APIOrgOpenPRs, so
-- the counts sum to the summary's open_prs. A bucket with none is
-- absent; the reader fills it.
SELECT (CASE
          WHEN now() - m.created_at < interval '1 day'   THEN '<1d'
          WHEN now() - m.created_at < interval '7 days'  THEN '1-7d'
          WHEN now() - m.created_at < interval '30 days' THEN '7-30d'
          ELSE '30d+'
        END)::text AS bucket,
       count(*)::int AS pull_requests
FROM remediations m
JOIN repositories r ON r.id = m.repository_id
WHERE r.active AND m.kind = 'pull_request' AND m.state = 'open'
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
GROUP BY 1;
