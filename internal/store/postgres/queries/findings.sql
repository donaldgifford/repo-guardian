-- name: LockFindings :many
SELECT * FROM findings
WHERE repository_id = $1
ORDER BY rule_kind, rule_name
FOR UPDATE;

-- name: UpsertFinding :exec
INSERT INTO findings (
    repository_id, rule_kind, rule_name, status, reason, remediation,
    evidence, evidence_version, status_since, last_evaluated_at, policy_version
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (repository_id, rule_kind, rule_name) DO UPDATE
SET status = excluded.status,
    reason = excluded.reason,
    remediation = excluded.remediation,
    evidence = excluded.evidence,
    evidence_version = excluded.evidence_version,
    status_since = excluded.status_since,
    last_evaluated_at = excluded.last_evaluated_at,
    policy_version = excluded.policy_version;

-- name: DeleteFinding :exec
DELETE FROM findings WHERE repository_id = $1 AND rule_kind = $2 AND rule_name = $3;
