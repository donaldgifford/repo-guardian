-- API installations (DESIGN-0027, IMPL-0025 Phase 15), scoped by the
-- account login, which is the org. Scoping as in api_summary.sql.

-- name: APIInstallations :many
SELECT i.installation_id, i.org, i.suspended_at, i.removed_at, i.rate_limit, i.rate_remaining, i.rate_reset_at, i.rate_observed_at
FROM (SELECT installation_id, account_login::text AS org, suspended_at, removed_at, rate_limit, rate_remaining,
             rate_reset_at, rate_observed_at
      FROM installations) i
WHERE (@scope_all::bool OR lower(i.org) = ANY (@scope_orgs::text[]))
  AND (NOT @has_after::bool OR i.installation_id > @after_id::bigint)
ORDER BY i.installation_id
LIMIT @page_limit;
