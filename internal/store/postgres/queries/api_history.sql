-- API compliance history (DESIGN-0027, IMPL-0025 Phase 15): stored
-- snapshots, oldest first, keyed by (snapshot_at, org, kind, rule).
-- Scoping as in api_summary.sql.

-- name: APIComplianceHistory :many
SELECT s.org, s.rule_kind, s.rule_name, s.snapshot_at, s.compliant, s.non_compliant, s.not_applicable, s.unknown
FROM compliance_snapshots s
WHERE (@scope_all::bool OR lower(s.org) = ANY (@scope_orgs::text[]))
  AND (sqlc.narg(org)::text IS NULL OR lower(s.org) = lower(sqlc.narg(org)::text))
  AND (sqlc.narg(rule_kind)::text IS NULL OR s.rule_kind = sqlc.narg(rule_kind)::text)
  AND (sqlc.narg(rule_name)::text IS NULL OR s.rule_name = sqlc.narg(rule_name)::text)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR s.snapshot_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR s.snapshot_at < sqlc.narg(to_at)::timestamptz)
  AND (NOT @has_after::bool
       OR (s.snapshot_at, s.org, s.rule_kind, s.rule_name)
          > (@after_snapshot_at::timestamptz, @after_org::text, @after_rule_kind::text, @after_rule_name::text))
ORDER BY s.snapshot_at, s.org, s.rule_kind, s.rule_name
LIMIT @page_limit;

-- Aggregates (IMPL-0031 7.1): the same rows summed per snapshot_at
-- (fleet) or per (snapshot_at, org) (org), with the percent floored to
-- one decimal in SQL exactly as ComplianceByRule floors it, so the API
-- never recomputes a percentage in Go. The kind/rule filters still
-- apply, which gives one rule's fleet series.

-- name: APIComplianceHistoryFleet :many
SELECT s.snapshot_at,
       sum(s.compliant)::int AS compliant,
       sum(s.non_compliant)::int AS non_compliant,
       sum(s.not_applicable)::int AS not_applicable,
       sum(s.unknown)::int AS unknown,
       CASE WHEN sum(s.compliant) + sum(s.non_compliant) = 0 THEN NULL
            ELSE (floor(sum(s.compliant) * 1000.0 / (sum(s.compliant) + sum(s.non_compliant))) / 10)::numeric
       END AS compliant_percent
FROM compliance_snapshots s
WHERE (@scope_all::bool OR lower(s.org) = ANY (@scope_orgs::text[]))
  AND (sqlc.narg(org)::text IS NULL OR lower(s.org) = lower(sqlc.narg(org)::text))
  AND (sqlc.narg(rule_kind)::text IS NULL OR s.rule_kind = sqlc.narg(rule_kind)::text)
  AND (sqlc.narg(rule_name)::text IS NULL OR s.rule_name = sqlc.narg(rule_name)::text)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR s.snapshot_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR s.snapshot_at < sqlc.narg(to_at)::timestamptz)
  AND (NOT @has_after::bool OR s.snapshot_at > @after_snapshot_at::timestamptz)
GROUP BY s.snapshot_at
ORDER BY s.snapshot_at
LIMIT @page_limit;

-- name: APIComplianceHistoryByOrg :many
SELECT s.snapshot_at, s.org,
       sum(s.compliant)::int AS compliant,
       sum(s.non_compliant)::int AS non_compliant,
       sum(s.not_applicable)::int AS not_applicable,
       sum(s.unknown)::int AS unknown,
       CASE WHEN sum(s.compliant) + sum(s.non_compliant) = 0 THEN NULL
            ELSE (floor(sum(s.compliant) * 1000.0 / (sum(s.compliant) + sum(s.non_compliant))) / 10)::numeric
       END AS compliant_percent
FROM compliance_snapshots s
WHERE (@scope_all::bool OR lower(s.org) = ANY (@scope_orgs::text[]))
  AND (sqlc.narg(org)::text IS NULL OR lower(s.org) = lower(sqlc.narg(org)::text))
  AND (sqlc.narg(rule_kind)::text IS NULL OR s.rule_kind = sqlc.narg(rule_kind)::text)
  AND (sqlc.narg(rule_name)::text IS NULL OR s.rule_name = sqlc.narg(rule_name)::text)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR s.snapshot_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR s.snapshot_at < sqlc.narg(to_at)::timestamptz)
  AND (NOT @has_after::bool
       OR (s.snapshot_at, s.org) > (@after_snapshot_at::timestamptz, @after_org::text))
GROUP BY s.snapshot_at, s.org
ORDER BY s.snapshot_at, s.org
LIMIT @page_limit;
