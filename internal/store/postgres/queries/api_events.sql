-- API timeline (DESIGN-0027, IMPL-0025 Phase 15): finding and
-- repository events merged with UNION ALL, newest first, keyed by
-- (occurred_at, source, id). Scoping as in api_summary.sql.

-- name: APIRepositoryEvents :many
SELECT e.source, e.id, e.occurred_at, coalesce(e.rule_kind, '')::text AS rule_kind, coalesce(e.rule_name, '')::text AS rule_name, e.from_status, e.to_status,
       e.from_reason, e.to_reason, e.from_remediation, e.to_remediation, e.kind, e.detail
FROM (
    SELECT 'finding'::text AS source, fe.id, fe.occurred_at, fe.rule_kind, fe.rule_name,
           fe.from_status, fe.to_status, fe.from_reason, fe.to_reason, fe.from_remediation, fe.to_remediation,
           NULL::text AS kind, NULL::jsonb AS detail
    FROM finding_events fe
    WHERE fe.repository_id = @repository_id
    UNION ALL
    SELECT 'repository'::text, re.id, re.occurred_at, NULL::text, NULL::text,
           NULL::text, NULL::text, NULL::text, NULL::text, NULL::text, NULL::text,
           re.kind, re.detail
    FROM repository_events re
    WHERE re.repository_id = @repository_id
) e
JOIN repositories r ON r.id = @repository_id
WHERE (@scope_all::bool OR lower(r.org) = ANY (@scope_orgs::text[]))
  AND (NOT @has_after::bool
       OR (e.occurred_at, e.source, e.id) < (@after_occurred_at::timestamptz, @after_source::text, @after_id::bigint))
ORDER BY e.occurred_at DESC, e.source DESC, e.id DESC
LIMIT @page_limit;
