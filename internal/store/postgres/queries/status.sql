-- Status page inputs (DESIGN-0027 § Status page). Global and unscoped:
-- the page is aggregate-only and never names an org. Read by the
-- refresher every 30s, never per request.

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

-- name: StatusRates :many
-- Rate snapshots of live installations.
SELECT rate_limit::int AS rate_limit, rate_remaining::int AS rate_remaining, rate_reset_at::timestamptz AS rate_reset_at
FROM installations
WHERE removed_at IS NULL AND suspended_at IS NULL
  AND rate_observed_at IS NOT NULL AND rate_limit IS NOT NULL AND rate_remaining IS NOT NULL AND rate_reset_at IS NOT NULL;

-- name: StatusComplianceHistory :many
-- Fleet compliance at each UTC day's last snapshot since the given time,
-- summed over every org and rule, floored to one decimal like the shared
-- compliance query.
WITH daily AS (
    SELECT DISTINCT ON (date_trunc('day', cs.snapshot_at AT TIME ZONE 'UTC')) cs.snapshot_at
    FROM compliance_snapshots cs
    WHERE cs.snapshot_at >= @since::timestamptz
    ORDER BY date_trunc('day', cs.snapshot_at AT TIME ZONE 'UTC'), cs.snapshot_at DESC
)
SELECT s.snapshot_at,
       CASE WHEN sum(s.compliant) + sum(s.non_compliant) = 0 THEN NULL
            ELSE (floor(sum(s.compliant) * 1000.0 / (sum(s.compliant) + sum(s.non_compliant))) / 10)::numeric
       END AS compliant_percent
FROM compliance_snapshots s
JOIN daily d ON d.snapshot_at = s.snapshot_at
GROUP BY s.snapshot_at
ORDER BY s.snapshot_at;

-- name: StatusOperations :one
-- Repositories checked since the given time, the newest policy version's
-- rollout and the last snapshot.
SELECT (SELECT count(DISTINCT c.repository_id) FROM checks c
        WHERE c.finished_at >= @since::timestamptz AND c.outcome IN ('success', 'skipped'))::int AS checked,
       (SELECT count(*) FROM policy_versions)::int AS policy_versions,
       (SELECT p.rollout_completed_at FROM policy_versions p
        ORDER BY p.first_seen_at DESC, p.version DESC LIMIT 1) AS rollout_completed_at,
       (SELECT max(cs.snapshot_at) FROM compliance_snapshots cs) AS last_snapshot;
