-- name: FindRepositoryByProviderID :one
SELECT id, org, name, active, park_reason FROM repositories
WHERE provider = 'github' AND host = @host AND provider_repo_id = @provider_repo_id;

-- name: FindRepositoryByName :one
SELECT id, org, name, active, park_reason FROM repositories
WHERE provider = 'github' AND host = @host AND lower(org) = lower(@org) AND lower(name) = lower(@name);

-- name: GetRepository :one
SELECT id, org, name, active, park_reason FROM repositories WHERE id = @id;

-- name: InsertRepository :one
INSERT INTO repositories (host, org, name, provider_repo_id) VALUES (@host, @org, @name, @provider_repo_id)
RETURNING id;

-- name: UpdateRepositoryIdentity :exec
UPDATE repositories SET org = @org, name = @name, provider_repo_id = @provider_repo_id WHERE id = @id;

-- name: UnparkRepository :exec
-- Discovery is the only un-parker (INV-0015).
UPDATE repositories SET active = true, park_reason = NULL, parked_at = NULL WHERE id = @id;

-- name: ParkRepository :execrows
UPDATE repositories SET active = false, park_reason = @park_reason, parked_at = now()
WHERE id = @id AND (active OR park_reason IS DISTINCT FROM @park_reason);

-- name: InsertRepositoryEvent :exec
INSERT INTO repository_events (repository_id, kind, detail) VALUES (@repository_id, @kind, @detail);

-- name: UpsertAppInstallation :exec
INSERT INTO app_installations (app, installation_id, account_login, suspended_at, repository_selection, seen_at)
VALUES (@app, @installation_id, @account_login, @suspended_at, @repository_selection, now())
ON CONFLICT (app, installation_id) DO UPDATE SET
    account_login = EXCLUDED.account_login,
    suspended_at = EXCLUDED.suspended_at,
    repository_selection = EXCLUDED.repository_selection,
    seen_at = EXCLUDED.seen_at;

-- name: UpsertAppRepositoryAccess :exec
INSERT INTO app_repository_access (repository_id, app, installation_id, seen_at)
VALUES (@repository_id, @app, @installation_id, now())
ON CONFLICT (repository_id, app) DO UPDATE SET installation_id = EXCLUDED.installation_id, seen_at = EXCLUDED.seen_at;

-- name: RepositoryAccess :many
-- Both Apps' access to one repository, with each installation's
-- suspension (DESIGN-0030 D15).
SELECT a.app, i.suspended_at FROM app_repository_access a
JOIN app_installations i ON (i.app, i.installation_id) = (a.app, a.installation_id)
WHERE a.repository_id = @repository_id;
