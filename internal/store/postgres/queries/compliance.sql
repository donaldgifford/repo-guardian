-- The shared compliance query (DESIGN-0025, IMPL-0025 Phase 8). The
-- report, the snapshot writer and the API all read these counts, so the
-- three can never disagree about a percentage.
--
-- compliant_percent = compliant / (compliant + non_compliant) over active
-- repositories, floored (never rounded) to one decimal: 999 of 1000 reads
-- 99.9, not 100. not_applicable and unknown are reported beside it, never
-- in it. An empty denominator is NULL: a rule that applies nowhere is
-- unmeasured, not 100% compliant.
--
-- orgs scopes the result; lower-case the names, an empty array means every
-- org.

-- name: ComplianceByRule :many
SELECT r.org, f.rule_kind, f.rule_name,
       count(*) FILTER (WHERE f.status = 'compliant')::int AS compliant,
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
  AND (cardinality(sqlc.arg(orgs)::text[]) = 0 OR lower(r.org) = ANY (sqlc.arg(orgs)::text[]))
GROUP BY r.org, f.rule_kind, f.rule_name
ORDER BY r.org, f.rule_kind, f.rule_name;

-- name: FailingFindings :many
-- Non-compliant findings on active repositories, ordered by org, rule,
-- then repository so an unchanged database renders an identical report.
SELECT r.installation_id, r.org, r.name, f.rule_kind, f.rule_name, f.reason, f.remediation,
       f.status_since, coalesce(f.evidence -> 'pr' ->> 'url', '')::text AS pr_url
FROM findings f
JOIN repositories r ON r.id = f.repository_id
WHERE r.active AND f.status = 'non_compliant'
  AND (cardinality(sqlc.arg(orgs)::text[]) = 0 OR lower(r.org) = ANY (sqlc.arg(orgs)::text[]))
ORDER BY r.org, f.rule_name, r.name, f.rule_kind;

-- name: LatestComplianceSnapshots :many
-- The most recent snapshot per (org, kind, rule): what the report's
-- trend compares against.
SELECT DISTINCT ON (org, rule_kind, rule_name)
       org, rule_kind, rule_name, snapshot_at, compliant, non_compliant, not_applicable, unknown
FROM compliance_snapshots
WHERE cardinality(sqlc.arg(orgs)::text[]) = 0 OR lower(org) = ANY (sqlc.arg(orgs)::text[])
ORDER BY org, rule_kind, rule_name, snapshot_at DESC;
