-- name: InsertPolicyVersion :execrows
INSERT INTO policy_versions (version, summary) VALUES ($1, $2)
ON CONFLICT (version) DO NOTHING;

-- name: CompletePolicyRollout :exec
UPDATE policy_versions SET rollout_completed_at = now()
WHERE version = $1 AND rollout_completed_at IS NULL;
