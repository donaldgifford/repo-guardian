-- name: InsertComplianceSnapshotRows :execrows
-- Writes rows computed by ComplianceByRule, so a snapshot holds exactly
-- the counts the report showed. Idempotent on the primary key, so a
-- retried snapshot writes nothing new.
INSERT INTO compliance_snapshots (
    org, rule_kind, rule_name, snapshot_at, compliant, non_compliant, not_applicable, unknown
)
-- Parallel unnest in the select list zips the equal-length arrays;
-- sqlc cannot type the multi-argument FROM unnest(...) form.
SELECT unnest(sqlc.arg(orgs)::text[]), unnest(sqlc.arg(rule_kinds)::text[]), unnest(sqlc.arg(rule_names)::text[]),
       sqlc.arg(snapshot_at)::timestamptz,
       unnest(sqlc.arg(compliant)::int[]), unnest(sqlc.arg(non_compliant)::int[]),
       unnest(sqlc.arg(not_applicable)::int[]), unnest(sqlc.arg(unknown)::int[])
ON CONFLICT DO NOTHING;
