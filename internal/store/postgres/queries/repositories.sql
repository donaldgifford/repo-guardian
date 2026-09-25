-- name: GetRepository :one
SELECT * FROM repositories WHERE id = $1;

-- name: ListActiveRepositories :many
SELECT * FROM repositories
WHERE active AND id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(page_limit);

-- name: LockRepositoryByName :one
SELECT * FROM repositories
WHERE provider = $1 AND host = $2 AND lower(org) = lower(sqlc.arg(org)) AND lower(name) = lower(sqlc.arg(name))
FOR UPDATE;

-- name: LockRepository :one
SELECT * FROM repositories WHERE id = $1 FOR UPDATE;

-- name: LockRepositoryByProviderID :one
SELECT * FROM repositories
WHERE provider = $1 AND host = $2 AND provider_repo_id = sqlc.arg(provider_repo_id)
FOR UPDATE;

-- name: InsertRepository :one
INSERT INTO repositories (provider, host, org, name, provider_repo_id, installation_id, next_due_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT DO NOTHING
RETURNING id;

-- name: ReactivateRepository :exec
-- The only statement that sets active = true (INV-0015): discovery is
-- the sole un-parker.
UPDATE repositories
SET active = true, park_reason = NULL, parked_at = NULL
WHERE id = $1;

-- name: UpdateRepositoryIdentity :exec
UPDATE repositories
SET org = sqlc.arg(org),
    name = sqlc.arg(name),
    installation_id = sqlc.arg(installation_id),
    provider_repo_id = coalesce(provider_repo_id, sqlc.narg(provider_repo_id))
WHERE id = sqlc.arg(id);

-- name: UpdateRepositoryAfterCheck :exec
UPDATE repositories
SET last_checked_at = sqlc.arg(checked_at),
    last_check_outcome = 'success',
    last_error = NULL,
    policy_version = sqlc.arg(policy_version),
    catalog_parse_ok = sqlc.narg(catalog_parse_ok)
WHERE id = sqlc.arg(id);

-- name: UpdateRepositoryAfterError :exec
UPDATE repositories
SET last_checked_at = sqlc.arg(checked_at),
    last_check_outcome = 'error',
    last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(id);

-- name: ParkRepository :execrows
-- Parks an active repository. Zero rows means it is missing or already
-- parked, which keeps a retried Park from writing a second event.
UPDATE repositories
SET active = false, park_reason = sqlc.arg(park_reason), parked_at = now()
WHERE id = sqlc.arg(id) AND active;

-- name: ParkInstallationRepositories :many
UPDATE repositories
SET active = false, park_reason = 'installation_removed', parked_at = now()
WHERE installation_id = $1 AND active
RETURNING id;
