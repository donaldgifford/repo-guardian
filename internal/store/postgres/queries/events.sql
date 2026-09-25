-- name: InsertFindingEvent :exec
INSERT INTO finding_events (
    repository_id, rule_kind, rule_name, check_id,
    from_status, to_status, from_reason, to_reason,
    from_remediation, to_remediation, evidence, policy_version, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: ListFindingEventsByCheck :many
SELECT * FROM finding_events WHERE check_id = $1 ORDER BY id;

-- name: InsertRepositoryEvent :exec
INSERT INTO repository_events (repository_id, kind, detail) VALUES ($1, $2, $3);
