-- API checks (DESIGN-0027, IMPL-0025 Phase 15). Scoping as in
-- api_summary.sql. Pages are keyed by id, newest first.

-- name: APIRepositoryChecks :many
SELECT c.id, c.trigger, c.outcome, c.policy_version, c.started_at, c.finished_at, c.error
FROM checks c
JOIN repositories r ON r.id = c.repository_id
WHERE c.repository_id = @repository_id
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
  AND (NOT @has_after::bool OR c.id < @after_id::bigint)
ORDER BY c.id DESC
LIMIT @page_limit;
