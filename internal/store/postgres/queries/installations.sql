-- name: UpsertInstallation :exec
INSERT INTO installations (installation_id, provider, host, account_login, suspended_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (installation_id) DO UPDATE
SET account_login = excluded.account_login,
    suspended_at = excluded.suspended_at,
    removed_at = NULL,
    updated_at = now();

-- name: MarkInstallationRemoved :execrows
UPDATE installations SET removed_at = $2, updated_at = now() WHERE installation_id = $1;

-- name: UpdateInstallationRate :exec
-- Keeps the newest observation: an older one never wins.
UPDATE installations
SET rate_limit = sqlc.arg(rate_limit),
    rate_remaining = sqlc.arg(rate_remaining),
    rate_reset_at = sqlc.arg(rate_reset_at),
    rate_observed_at = sqlc.arg(rate_observed_at),
    updated_at = now()
WHERE installation_id = sqlc.arg(installation_id)
  AND (rate_observed_at IS NULL OR rate_observed_at < sqlc.arg(rate_observed_at));
