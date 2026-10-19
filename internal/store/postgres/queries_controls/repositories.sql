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
INSERT INTO app_installations (app, installation_id, account_login, account_type, suspended_at, repository_selection, seen_at)
VALUES (@app, @installation_id, @account_login, @account_type, @suspended_at, @repository_selection, now())
ON CONFLICT (app, installation_id) DO UPDATE SET
    account_login = EXCLUDED.account_login,
    account_type = EXCLUDED.account_type,
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

-- name: ListMissingEvalRepositories :many
-- The installation's active repositories a complete listing did not see.
SELECT r.id FROM repositories r
JOIN app_repository_access a ON a.repository_id = r.id AND a.app = 'eval'
WHERE a.installation_id = @installation_id AND r.active
  -- A nil slice arrives as NULL, and id = ANY (NULL) is NULL, not false.
  AND NOT (r.id = ANY (coalesce(@seen::bigint[], ARRAY[]::bigint[])))
ORDER BY r.id;

-- name: InsertServiceRun :exec
INSERT INTO service_runs (kind, outcome, started_at, finished_at, detail)
VALUES (@kind, @outcome, @started_at, @finished_at, @detail);

-- name: ListActiveRepositoryPage :many
-- One keyset page of active repositories with the Evaluation App's
-- installation and the policy state, for the rollout's passes.
SELECT r.id, a.installation_id, ps.state AS policy_state
FROM repositories r
LEFT JOIN app_repository_access a ON a.repository_id = r.id AND a.app = 'eval'
LEFT JOIN repository_policy_state ps ON ps.repository_id = r.id
WHERE r.active AND r.id > @after_id
ORDER BY r.id
LIMIT @page_size;

-- name: FindRepositoryRef :one
-- A known repository by provider id, without touching it: a push must
-- leave a parked repository parked.
SELECT r.id, r.active, a.installation_id, ps.state AS policy_state
FROM repositories r
LEFT JOIN app_repository_access a ON a.repository_id = r.id AND a.app = 'eval'
LEFT JOIN repository_policy_state ps ON ps.repository_id = r.id
WHERE r.provider = 'github' AND r.host = @host AND r.provider_repo_id = @provider_repo_id;

-- name: ListOrgRepositories :many
SELECT id FROM repositories
WHERE provider = 'github' AND host = @host AND lower(org) = lower(@org) AND active
ORDER BY id;

-- name: GetRepositoryRef :one
SELECT r.id, r.active, a.installation_id, ps.state AS policy_state
FROM repositories r
LEFT JOIN app_repository_access a ON a.repository_id = r.id AND a.app = 'eval'
LEFT JOIN repository_policy_state ps ON ps.repository_id = r.id
WHERE r.id = @id;

-- name: SetNextDue :exec
-- The evaluation loop's next due time (IMPL-0030 2.8, INV-0023 F3); NULL
-- clears it.
UPDATE repositories SET next_due_at = sqlc.narg(next_due_at)::timestamptz WHERE id = @id;
