-- API remediations (DESIGN-0032 § API, DESIGN-0037 § Remediations view,
-- IMPL-0030 7.4 and 7.16). Every query carries the scope predicate on
-- lower(r.org), in every UNION ALL branch that reads repositories;
-- TestAPIQueries_AreScoped enforces it. Pages are keyset, fetched at
-- limit + 1 by the reader.

-- name: APIRemediations :many
-- The remediations rows, newest first, and, when @include_held, the
-- results held before a pull request exists (OQ20): a control_results
-- hold on a branch reason, as a pull-request row with id 0, no state and
-- no pull request, dated from hold_since. remediations.hold is only ever
-- permission, so the two branches never list one hold twice. Keyed by
-- (at, repository_id, control_id, id) descending.
SELECT x.id, x.repository_id, x.org, x.name, x.control_id, x.kind, x.state, x.intent_revision, x.branch,
       x.pr_number, x.pr_url, x.observed_head_sha, x.acknowledged_head_sha, x.hold, x.hold_since, x.proposal,
       x.at, x.last_run_at, x.closed_at, x.close_reason
FROM (
    SELECT 0::bigint AS id, r.id AS repository_id, r.org, r.name, c.control_id, 'pull_request'::text AS kind,
           NULL::text AS state, c.intent_revision, NULL::text AS branch, NULL::int AS pr_number, NULL::text AS pr_url,
           NULL::text AS observed_head_sha, NULL::text AS acknowledged_head_sha, c.hold, c.hold_since,
           NULL::jsonb AS proposal, coalesce(c.hold_since, c.last_changed_at)::timestamptz AS at,
           NULL::timestamptz AS last_run_at, NULL::timestamptz AS closed_at, NULL::text AS close_reason
    FROM control_results c
    JOIN repositories r ON r.id = c.repository_id
    WHERE @include_held::bool
      AND c.hold IN ('pr_cap', 'foreign_branch', 'stale_branch', 'conflict')
      AND c.hold = ANY (sqlc.narg(holds)::text[])
      AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
      AND (sqlc.narg(org)::text IS NULL OR lower(r.org) = lower(sqlc.narg(org)::text))
      AND (sqlc.narg(control_id)::text IS NULL OR c.control_id = sqlc.narg(control_id)::text)
      AND (sqlc.narg(before)::timestamptz IS NULL OR coalesce(c.hold_since, c.last_changed_at) < sqlc.narg(before)::timestamptz)
    UNION ALL
    SELECT m.id, r.id, r.org, r.name, m.control_id, m.kind, m.state, m.intent_revision, m.branch, m.pr_number, m.pr_url,
           m.observed_head_sha, m.acknowledged_head_sha, m.hold, NULL::timestamptz, m.proposal, m.created_at,
           m.last_run_at, m.closed_at, m.close_reason
    FROM remediations m
    JOIN repositories r ON r.id = m.repository_id
    WHERE (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
      AND (sqlc.narg(org)::text IS NULL OR lower(r.org) = lower(sqlc.narg(org)::text))
      AND (sqlc.narg(control_id)::text IS NULL OR m.control_id = sqlc.narg(control_id)::text)
      AND (sqlc.narg(kind)::text IS NULL OR m.kind = sqlc.narg(kind)::text)
      AND (sqlc.narg(state)::text IS NULL OR m.state = sqlc.narg(state)::text)
      AND (sqlc.narg(holds)::text[] IS NULL OR m.hold = ANY (sqlc.narg(holds)::text[]))
      AND (sqlc.narg(before)::timestamptz IS NULL OR m.created_at < sqlc.narg(before)::timestamptz)
) x
WHERE (NOT @has_after::bool
       OR (x.at, x.repository_id, x.control_id, x.id)
          < (@after_at::timestamptz, @after_repository_id::bigint, @after_control_id::text, @after_id::bigint))
ORDER BY x.at DESC, x.repository_id DESC, x.control_id DESC, x.id DESC
LIMIT @page_limit;

-- name: APIRepositoryRemediations :many
-- Each control's latest remediation on one repository.
SELECT DISTINCT ON (m.control_id)
       m.id, r.id AS repository_id, r.org, r.name, m.control_id, m.kind, m.state, m.intent_revision, m.branch,
       m.pr_number, m.pr_url, m.observed_head_sha, m.acknowledged_head_sha, m.hold, m.proposal, m.created_at,
       m.last_run_at, m.closed_at, m.close_reason
FROM remediations m
JOIN repositories r ON r.id = m.repository_id
WHERE m.repository_id = @repository_id
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
ORDER BY m.control_id, m.id DESC;

-- name: APIRemediationSteps :many
-- The step journals of a page of remediations, in order.
SELECT s.remediation_id, s.seq, s.kind, s.resource_key, s.committed_sha, s.pr_number, s.status, s.before, s.after,
       s.error, s.occurred_at
FROM remediation_steps s
JOIN remediations m ON m.id = s.remediation_id
JOIN repositories r ON r.id = m.repository_id
WHERE s.remediation_id = ANY (@ids::bigint[])
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
ORDER BY s.remediation_id, s.seq;

-- name: APIRemediationBacklog :one
-- Outstanding remediations over the visible orgs: what a remediator
-- still holds a slot or a recommendation for.
SELECT count(*)::int AS backlog
FROM remediations m
JOIN repositories r ON r.id = m.repository_id
WHERE m.state IN ('reserved', 'open', 'recommended', 'failed')
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]));

-- name: APIRemediationActivity :many
-- PR throughput (DESIGN-0037 OQ15, IMPL-0030 7.12): repo-guardian pull
-- requests opened (by created_at) and closed (by closed_at, merged or
-- not) per UTC day over the last day_count days, today included, every day
-- present, oldest first.
WITH prs AS MATERIALIZED (
    SELECT m.created_at, m.closed_at
    FROM remediations m
    JOIN repositories r ON r.id = m.repository_id
    WHERE m.kind = 'pull_request'
      AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
), calendar AS (
    SELECT (now() AT TIME ZONE 'UTC')::date - n AS day
    FROM generate_series(0, sqlc.arg(day_count)::int - 1) AS n
)
SELECT d.day::date AS day,
       (SELECT count(*) FROM prs WHERE (prs.created_at AT TIME ZONE 'UTC')::date = d.day)::int AS opened,
       (SELECT count(*) FROM prs WHERE (prs.closed_at AT TIME ZONE 'UTC')::date = d.day)::int AS closed
FROM calendar d
ORDER BY d.day;
