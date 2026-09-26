-- API rule and org drill-downs (DESIGN-0027, IMPL-0025 Phase 15). The
-- per-(org, rule) counts come from the shared ComplianceByRule query in
-- compliance.sql; these add what it does not carry. Scoping as in
-- api_summary.sql.

-- name: APIRuleReasons :many
-- Why one rule is failing or not applicable in scope, most common first.
SELECT coalesce(f.reason, '')::text AS reason, count(*)::int AS findings
FROM findings f
JOIN repositories r ON r.id = f.repository_id
WHERE r.active AND f.rule_kind = @rule_kind AND f.rule_name = @rule_name
  AND f.status IN ('non_compliant', 'not_applicable') AND f.reason IS NOT NULL
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
GROUP BY f.reason
ORDER BY findings DESC, reason;

-- name: APIOrgRepositories :many
-- Repositories in scope per org by active state and park reason.
SELECT r.org::text AS org, r.active, coalesce(r.park_reason, '')::text AS park_reason, count(*)::int AS repositories
FROM repositories r
WHERE (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
  AND (sqlc.narg(org)::text IS NULL OR lower(r.org) = lower(sqlc.narg(org)::text))
GROUP BY r.org, r.active, r.park_reason
ORDER BY lower(r.org), r.active DESC, park_reason;

-- name: APIOrgNotApplicable :many
-- Not-applicable findings in one org by reason.
SELECT coalesce(f.reason, '')::text AS reason, count(*)::int AS findings
FROM findings f
JOIN repositories r ON r.id = f.repository_id
WHERE r.active AND f.status = 'not_applicable' AND lower(r.org) = lower(@org::text)
  AND (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
GROUP BY f.reason
ORDER BY findings DESC, reason;
