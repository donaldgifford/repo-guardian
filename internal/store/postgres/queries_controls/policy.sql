-- name: ActivatePolicyVersion :one
-- DESIGN-0030 D14's upsert (INV-0022 spike 8): a version seen before
-- keeps its first_seen_at and summary and is re-activated, so a revert is
-- honoured. Re-activating the current version matches no row, so the
-- caller reads pgx.ErrNoRows as "already current" (IMPL-0028 8.4).
INSERT INTO policy_versions (version, summary) VALUES (@version, @summary)
ON CONFLICT (version) DO UPDATE SET activated_at = now()
  WHERE policy_versions.version IS DISTINCT FROM
    (SELECT pv.version FROM policy_versions pv ORDER BY pv.activated_at DESC, pv.version DESC LIMIT 1)
RETURNING (xmax = 0)::bool AS first_seen;

-- name: CurrentActivation :one
SELECT version, activated_at FROM policy_versions
ORDER BY activated_at DESC, version DESC
LIMIT 1;
