-- Status page inputs (DESIGN-0027 § Status page) over the controls
-- schema. Global and unscoped: the page is aggregate-only and never
-- names an org. Read by the refresher every 30s, never per request.

-- name: StatusChecks :one
SELECT (SELECT max(c.finished_at) FROM checks c WHERE c.outcome = 'success') AS last_success,
       (SELECT max(c.finished_at) FROM checks c WHERE c.trigger IN ('webhook', 'push')) AS last_webhook,
       (SELECT count(*) FROM checks c WHERE c.finished_at >= @since::timestamptz AND c.outcome = 'error')::int AS errors,
       (SELECT count(*) FROM checks c WHERE c.finished_at >= @since::timestamptz)::int AS finished,
       (SELECT count(*) FROM repositories r WHERE r.active)::int AS active_repositories;

-- name: StatusServiceRuns :many
-- The last successful run of each service.
SELECT kind, max(finished_at)::timestamptz AS last_success
FROM service_runs
WHERE outcome = 'success'
GROUP BY kind;

-- name: StatusComplianceHistory :many
-- Fleet compliance at each UTC day's last snapshot since the given time,
-- summed over every org, control and lineage.
WITH daily AS (
    SELECT DISTINCT ON (date_trunc('day', cs.snapshot_at AT TIME ZONE 'UTC')) cs.snapshot_at
    FROM compliance_snapshots cs
    WHERE cs.snapshot_at >= @since::timestamptz
    ORDER BY date_trunc('day', cs.snapshot_at AT TIME ZONE 'UTC'), cs.snapshot_at DESC
)
SELECT s.snapshot_at, floor_percent(sum(s.compliant), sum(s.compliant) + sum(s.non_compliant))::numeric AS compliant_percent
FROM compliance_snapshots s
JOIN daily d ON d.snapshot_at = s.snapshot_at
GROUP BY s.snapshot_at
ORDER BY s.snapshot_at;

-- name: StatusOperations :one
-- Repositories checked since the given time, the current (most recently
-- activated, DESIGN-0030 D14) policy version's rollout and the last
-- snapshot.
SELECT (SELECT count(DISTINCT c.repository_id) FROM checks c
        WHERE c.finished_at >= @since::timestamptz AND c.outcome IN ('success', 'skipped'))::int AS checked,
       (SELECT count(*) FROM policy_versions)::int AS policy_versions,
       (SELECT p.rollout_completed_at FROM policy_versions p
        ORDER BY p.activated_at DESC, p.version DESC LIMIT 1) AS rollout_completed_at,
       (SELECT max(cs.snapshot_at) FROM compliance_snapshots cs) AS last_snapshot;
