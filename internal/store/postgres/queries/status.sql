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
