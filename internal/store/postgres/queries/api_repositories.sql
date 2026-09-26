-- API repositories (DESIGN-0027, IMPL-0025 Phase 15). Scoping as in
-- api_summary.sql. Pages are keyed by id.

-- name: APIRepositories :many
SELECT r.id, r.org, r.name, r.provider_repo_id, r.installation_id, r.active, r.park_reason, r.parked_at,
       r.discovered_at, r.next_due_at, r.last_checked_at, r.last_check_outcome, r.policy_version
FROM repositories r
WHERE (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
  AND (sqlc.narg(org)::text IS NULL OR lower(r.org) = lower(sqlc.narg(org)::text))
  AND (sqlc.narg(active)::bool IS NULL OR r.active = sqlc.narg(active)::bool)
  AND (sqlc.narg(park_reason)::text IS NULL OR r.park_reason = sqlc.narg(park_reason)::text)
  AND (sqlc.narg(name_prefix)::text IS NULL
       OR left(lower(r.name), length(sqlc.narg(name_prefix)::text)) = lower(sqlc.narg(name_prefix)::text))
  AND (NOT @has_after::bool OR r.id > @after_id::bigint)
ORDER BY r.id
LIMIT @page_limit;

-- name: APIRepository :one
-- One repository in scope with its installation.
SELECT r.id, r.org, r.name, r.provider_repo_id, r.installation_id, r.active, r.park_reason, r.parked_at,
       r.discovered_at, r.next_due_at, r.last_checked_at, r.last_check_outcome, r.policy_version,
       i.account_login, i.suspended_at, i.removed_at, i.rate_limit, i.rate_remaining, i.rate_reset_at, i.rate_observed_at
FROM repositories r
JOIN installations i ON i.installation_id = r.installation_id
WHERE r.id = @id
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]));
