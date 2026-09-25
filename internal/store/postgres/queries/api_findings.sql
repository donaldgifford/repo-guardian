-- API findings (DESIGN-0027, IMPL-0025 Phase 15). Scoping as in
-- api_summary.sql. Pages are keyed by the findings primary key.
--
-- pr_stale: the finding's repo-guardian PR is open and was created
-- before @stale_before (now minus stale_after).

-- name: APIFindings :many
SELECT f.repository_id, r.org, r.name, f.rule_kind, f.rule_name, f.status, f.reason, f.remediation,
       f.evidence, f.status_since, f.last_evaluated_at,
       coalesce(f.remediation = 'pr_open'
                AND (f.evidence -> 'pr' ->> 'created_at')::timestamptz < @stale_before::timestamptz, false)::bool AS pr_stale
FROM findings f
JOIN repositories r ON r.id = f.repository_id
WHERE r.active
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
  AND (sqlc.narg(status)::text IS NULL OR f.status = sqlc.narg(status)::text)
  AND (sqlc.narg(reason)::text IS NULL OR f.reason = sqlc.narg(reason)::text)
  AND (sqlc.narg(remediation)::text IS NULL OR f.remediation = sqlc.narg(remediation)::text)
  AND (sqlc.narg(org)::text IS NULL OR lower(r.org) = lower(sqlc.narg(org)::text))
  AND (sqlc.narg(rule_kind)::text IS NULL OR f.rule_kind = sqlc.narg(rule_kind)::text)
  AND (sqlc.narg(rule_name)::text IS NULL OR f.rule_name = sqlc.narg(rule_name)::text)
  AND (sqlc.narg(since_before)::timestamptz IS NULL OR f.status_since < sqlc.narg(since_before)::timestamptz)
  AND (sqlc.narg(pr_stale)::bool IS NULL
       OR coalesce(f.remediation = 'pr_open'
                   AND (f.evidence -> 'pr' ->> 'created_at')::timestamptz < @stale_before::timestamptz, false)
          = sqlc.narg(pr_stale)::bool)
  AND (NOT @has_after::bool
       OR (f.repository_id, f.rule_kind, f.rule_name) > (@after_repository_id::bigint, @after_rule_kind::text, @after_rule_name::text))
ORDER BY f.repository_id, f.rule_kind, f.rule_name
LIMIT @page_limit;

-- name: APIOldestFailures :many
-- One rule's longest-failing findings in scope.
SELECT f.repository_id, r.org, r.name, f.rule_kind, f.rule_name, f.status, f.reason, f.remediation,
       f.evidence, f.status_since, f.last_evaluated_at,
       coalesce(f.remediation = 'pr_open'
                AND (f.evidence -> 'pr' ->> 'created_at')::timestamptz < @stale_before::timestamptz, false)::bool AS pr_stale
FROM findings f
JOIN repositories r ON r.id = f.repository_id
WHERE r.active AND f.status = 'non_compliant' AND f.rule_kind = @rule_kind AND f.rule_name = @rule_name
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
ORDER BY f.status_since, f.repository_id
LIMIT @page_limit;

-- name: APIRepositoryFindings :many
-- Every finding on one repository in scope.
SELECT f.repository_id, r.org, r.name, f.rule_kind, f.rule_name, f.status, f.reason, f.remediation,
       f.evidence, f.status_since, f.last_evaluated_at,
       coalesce(f.remediation = 'pr_open'
                AND (f.evidence -> 'pr' ->> 'created_at')::timestamptz < @stale_before::timestamptz, false)::bool AS pr_stale
FROM findings f
JOIN repositories r ON r.id = f.repository_id
WHERE f.repository_id = @repository_id
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
ORDER BY f.rule_kind, f.rule_name;
