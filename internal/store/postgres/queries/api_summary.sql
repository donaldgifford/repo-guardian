-- API queries (DESIGN-0027, IMPL-0025 Phase 14). Every api_*.sql query
-- is scoped in SQL by the caller's visible orgs with exactly
--
--   (@scope_all::bool OR lower(<alias>.org) = ANY (@scope_orgs::text[]))
--
-- scope_orgs is lower-cased and empty scope_orgs with scope_all false
-- matches nothing. TestAPIQueries_AreScoped fails on a query without the
-- predicate. Unlike compliance.sql, an empty org list never means "all".

-- name: APISummaryRepositories :many
-- Repositories in scope by active state and park reason.
SELECT r.active, coalesce(r.park_reason, '')::text AS park_reason, count(*)::int AS repositories
FROM repositories r
WHERE (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
GROUP BY r.active, r.park_reason
ORDER BY r.active DESC, park_reason;

-- name: APISummaryFindings :one
-- Finding counts over active repositories in scope, with the shared
-- compliance math (see compliance.sql).
SELECT count(*) FILTER (WHERE f.status = 'compliant')::int AS compliant,
       count(*) FILTER (WHERE f.status = 'non_compliant')::int AS non_compliant,
       count(*) FILTER (WHERE f.status = 'not_applicable')::int AS not_applicable,
       count(*) FILTER (WHERE f.status = 'unknown')::int AS unknown,
       CASE WHEN count(*) FILTER (WHERE f.status IN ('compliant', 'non_compliant')) = 0 THEN NULL
            ELSE (floor(count(*) FILTER (WHERE f.status = 'compliant') * 1000.0
                        / count(*) FILTER (WHERE f.status IN ('compliant', 'non_compliant'))) / 10)::numeric
       END AS compliant_percent
FROM findings f
JOIN repositories r ON r.id = f.repository_id
WHERE r.active
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]));

-- name: APISummaryOpenPRs :many
-- One row per open repo-guardian PR (one branch per repository), aged
-- by the PR's created_at from its findings' evidence.
SELECT r.id, min((f.evidence -> 'pr' ->> 'created_at')::timestamptz)::timestamptz AS created_at
FROM findings f
JOIN repositories r ON r.id = f.repository_id
WHERE r.active AND f.remediation = 'pr_open' AND f.evidence -> 'pr' ->> 'created_at' IS NOT NULL
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
GROUP BY r.id
ORDER BY r.id;
