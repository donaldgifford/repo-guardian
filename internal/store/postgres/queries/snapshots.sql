-- name: InsertComplianceSnapshot :execrows
-- One row per (org, kind, rule) over active repositories. Idempotent on
-- the primary key, so a retried snapshot writes nothing new.
INSERT INTO compliance_snapshots (
    org, rule_kind, rule_name, snapshot_at, compliant, non_compliant, not_applicable, unknown
)
SELECT r.org, f.rule_kind, f.rule_name, sqlc.arg(snapshot_at)::timestamptz,
       count(*) FILTER (WHERE f.status = 'compliant')::int,
       count(*) FILTER (WHERE f.status = 'non_compliant')::int,
       count(*) FILTER (WHERE f.status = 'not_applicable')::int,
       count(*) FILTER (WHERE f.status = 'unknown')::int
FROM findings f
JOIN repositories r ON r.id = f.repository_id
WHERE r.active
GROUP BY r.org, f.rule_kind, f.rule_name
ON CONFLICT DO NOTHING;
