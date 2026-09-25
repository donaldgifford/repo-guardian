-- name: InsertPolicyVersion :execrows
INSERT INTO policy_versions (version, summary) VALUES ($1, $2)
ON CONFLICT (version) DO NOTHING;

-- name: CompletePolicyRollout :exec
UPDATE policy_versions SET rollout_completed_at = now()
WHERE version = $1 AND rollout_completed_at IS NULL;

-- name: CurrentPolicyVersion :one
-- The newest policy version. Global: the API's /policy is not org-scoped
-- because the policy is one document for the whole fleet.
SELECT version, first_seen_at, rollout_completed_at, summary
FROM policy_versions
ORDER BY first_seen_at DESC, version DESC
LIMIT 1;
