-- The controls compliance queries (DESIGN-0030 § Compliance queries,
-- AR-0030-05). Posture starts from assignments and LEFT JOINs results at
-- the assignment's epoch and revision, so every active assignment lands
-- in exactly one bucket and a new one is pending, never invisible.
--
-- Repositories in scope are the managed ones plus those parked for
-- access loss (access_denied, or unknown, the fail-safe), whose kept
-- results read as unmeasurable. Excluded, unmanaged and repositories
-- parked for other reasons are reported separately (D5).
--
-- decision is the baseline lineage; an excluded assignment counts under
-- the lineage it would have had. lineage filters: 'all', 'baseline'
-- (baseline_control set, whichever layer fulfils it, D17) or 'org_added'.
-- orgs scopes the result; lower-case the names, empty means every org.
--
-- PERF: posture is MATERIALIZED so each row's bucket (and its suspension
-- subquery) is computed once. Inlined, every FILTER below re-evaluated
-- the CASE, and the inflated cost estimate tipped the query into JIT:
-- 1.1s at 20k repositories and 200k assignments, 200ms materialized
-- (IMPL-0029 7.4, TestAPIPerf).

-- name: ControlCompliance :many
WITH posture AS MATERIALIZED (
    SELECT r.org, ca.control_id,
           CASE WHEN ca.decision <> 'excluded' THEN ca.decision
                WHEN ca.baseline_control IS NULL THEN 'org_added'
                ELSE 'baseline' END AS decision,
           CASE
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
           END AS bucket
    FROM control_assignments ca
    JOIN repositories r ON r.id = ca.repository_id
    JOIN repository_policy_state ps ON ps.repository_id = ca.repository_id
    LEFT JOIN control_results cr ON (cr.repository_id, cr.control_id) = (ca.repository_id, ca.control_id)
    WHERE (ps.state = 'managed' OR (ps.state = 'parked' AND r.park_reason IN ('access_denied', 'unknown')))
      AND (cardinality(@orgs::text[]) = 0 OR lower(r.org) = ANY (@orgs::text[]))
      AND CASE @lineage::text
            WHEN 'baseline'  THEN ca.baseline_control IS NOT NULL
            WHEN 'org_added' THEN ca.decision = 'org_added'
                                  OR (ca.decision = 'excluded' AND ca.baseline_control IS NULL)
            ELSE true
          END
), counts AS (
    SELECT org, control_id, decision,
           count(*) FILTER (WHERE bucket = 'compliant')::int                  AS compliant,
           count(*) FILTER (WHERE bucket = 'non_compliant')::int              AS non_compliant,
           count(*) FILTER (WHERE bucket = 'pending')::int                    AS pending,
           count(*) FILTER (WHERE bucket = 'stale')::int                      AS stale,
           count(*) FILTER (WHERE bucket = 'unmeasurable_access_denied')::int AS unmeasurable_access_denied,
           count(*) FILTER (WHERE bucket = 'unmeasurable_suspended')::int     AS unmeasurable_suspended,
           count(*) FILTER (WHERE bucket = 'unmeasurable_conflict')::int      AS unmeasurable_conflict,
           count(*) FILTER (WHERE bucket = 'not_applicable')::int             AS not_applicable,
           count(*) FILTER (WHERE bucket = 'unknown')::int                    AS unknown,
           count(*) FILTER (WHERE bucket = 'excluded')::int                   AS excluded,
           count(*) FILTER (WHERE bucket NOT IN ('excluded', 'unmeasurable_conflict'))::int AS assigned
    FROM posture
    GROUP BY org, control_id, decision
)
SELECT org, control_id, decision, compliant, non_compliant, pending, stale, unmeasurable_access_denied,
       unmeasurable_suspended, unmeasurable_conflict, not_applicable, unknown, excluded, assigned,
       floor_percent(compliant, compliant + non_compliant)::numeric AS percent,
       floor_percent(compliant + non_compliant + not_applicable, assigned)::numeric AS coverage
FROM counts
ORDER BY org, control_id, decision;

-- name: ControlExclusions :many
-- The exclusions report (D5): excluded and conflicted assignments with
-- their reasons, and excluded repositories, per org.
SELECT r.org, r.name, ca.control_id AS control_id, ca.state, coalesce(ca.reason, ca.error, '')::text AS reason,
       coalesce(ca.excluded_by, '')::text AS excluded_by
FROM control_assignments ca
JOIN repositories r ON r.id = ca.repository_id
WHERE ca.state IN ('excluded', 'conflict')
  AND (cardinality(@orgs::text[]) = 0 OR lower(r.org) = ANY (@orgs::text[]))
UNION ALL
SELECT r.org, r.name, ''::text, ps.state, coalesce(ps.reason, '')::text, ''::text
FROM repository_policy_state ps
JOIN repositories r ON r.id = ps.repository_id
WHERE ps.state = 'excluded'
  AND (cardinality(@orgs::text[]) = 0 OR lower(r.org) = ANY (@orgs::text[]))
ORDER BY 1, 2, 3;

-- name: FloorPercent :one
-- The percentage function on its own, for the Go mirror's agreement test.
SELECT floor_percent(@num::bigint, @den::bigint)::numeric AS percent;

-- name: InsertComplianceSnapshot :exec
-- One controls compliance snapshot row, copied from ControlCompliance in
-- the same transaction so a snapshot never disagrees with the live view.
INSERT INTO compliance_snapshots (
    org, control_id, decision, snapshot_at, compliant, non_compliant, pending, stale, unmeasurable_access_denied,
    unmeasurable_suspended, unmeasurable_conflict, not_applicable, unknown, excluded, assigned, percent, coverage
) VALUES (
    @org, @control_id, @decision, @snapshot_at, @compliant, @non_compliant, @pending, @stale, @unmeasurable_access_denied,
    @unmeasurable_suspended, @unmeasurable_conflict, @not_applicable, @unknown, @excluded, @assigned, @percent, @coverage
);

-- name: OpenRemediationPRs :many
-- remediation_prs_open (IMPL-0030 7.1): open remediation pull requests
-- per org, control and age bucket, for every api replica to publish.
SELECT r.org, m.control_id,
       (CASE
          WHEN now() - m.created_at < interval '1 day'   THEN '<1d'
          WHEN now() - m.created_at < interval '7 days'  THEN '1-7d'
          WHEN now() - m.created_at < interval '30 days' THEN '7-30d'
          ELSE '30d+'
        END)::text AS age_bucket,
       count(*)::int AS open
FROM remediations m
JOIN repositories r ON r.id = m.repository_id
WHERE m.kind = 'pull_request' AND m.state = 'open'
GROUP BY r.org, m.control_id, age_bucket
ORDER BY r.org, m.control_id, age_bucket;

-- name: RemediationHolds :many
-- remediation_holds (IMPL-0030 7.2): control results held from
-- remediation per org, hold reason and how long they have been held,
-- so a conflict held past a week can alert (OQ11) without a counter.
SELECT r.org, c.hold::text AS reason,
       (CASE
          WHEN now() - c.hold_since < interval '1 day'   THEN '<1d'
          WHEN now() - c.hold_since < interval '7 days'  THEN '1-7d'
          WHEN now() - c.hold_since < interval '30 days' THEN '7-30d'
          ELSE '30d+'
        END)::text AS age_bucket,
       count(*)::int AS held
FROM control_results c
JOIN repositories r ON r.id = c.repository_id
WHERE c.hold IS NOT NULL AND c.hold_since IS NOT NULL
GROUP BY r.org, c.hold, age_bucket
ORDER BY r.org, reason, age_bucket;
