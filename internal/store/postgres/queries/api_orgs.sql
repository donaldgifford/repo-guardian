-- API org list (DESIGN-0027, IMPL-0025 Phase 15). The per-(org, rule)
-- counts come from the shared ComplianceByRule query in compliance.sql;
-- these add what it does not carry. IMPL-0029 7.4 re-cuts /orgs onto the
-- controls compliance query.

-- name: APIOrgRepositories :many
-- Repositories in scope per org by active state and park reason.
SELECT r.org::text AS org, r.active, coalesce(r.park_reason, '')::text AS park_reason, count(*)::int AS repositories
FROM repositories r
WHERE (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
  AND (sqlc.narg(org)::text IS NULL OR lower(r.org) = lower(sqlc.narg(org)::text))
GROUP BY r.org, r.active, r.park_reason
ORDER BY lower(r.org), r.active DESC, park_reason;

-- name: APIOrgOpenPRs :many
-- Findings with an open repo-guardian PR per org, over active
-- repositories like /findings?remediation=pr_open (IMPL-0031 7.5).
SELECT r.org::text AS org, count(*)::int AS findings
FROM findings f
JOIN repositories r ON r.id = f.repository_id
WHERE r.active AND f.remediation = 'pr_open'
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
GROUP BY r.org
ORDER BY lower(r.org);

