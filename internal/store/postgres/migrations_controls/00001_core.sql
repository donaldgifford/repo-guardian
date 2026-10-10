-- DESIGN-0032 § Data model, 00001_core: the surviving v2 tables in their
-- final shape, on a fresh database (D27). Nothing here reads or migrates
-- the rc line's schema.
--
-- Roles (D31): the owner runs this chain and owns every object; it holds
-- no CREATEROLE. rg_evaluator and rg_remediator are provisioned by the
-- chart or the operator beforehand, and this chain only grants to them,
-- so a missing role fails the migration rather than leaving a writer
-- without its grants. Surrogate keys are identity columns, which need no
-- sequence grant.
--
-- 00002 to 00004 are reserved for IMPL-0029 (00002_controls_policy,
-- 00003_controls_results) and IMPL-0030 (00004_compliance).

-- +goose Up
CREATE TABLE repositories (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider         TEXT NOT NULL DEFAULT 'github',
    host             TEXT NOT NULL DEFAULT 'github.com',
    org              TEXT NOT NULL,
    name             TEXT NOT NULL,
    provider_repo_id BIGINT,
    active           BOOLEAN NOT NULL DEFAULT true,
    park_reason      TEXT CHECK (park_reason IN
                       ('access_denied', 'archived', 'fork', 'removed', 'installation_removed', 'suspended', 'unknown')),
    parked_at        TIMESTAMPTZ,
    discovered_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    next_due_at      TIMESTAMPTZ,
    last_checked_at  TIMESTAMPTZ,
    last_error       TEXT
);

CREATE UNIQUE INDEX repositories_name_key
    ON repositories (provider, host, lower(org), lower(name));
CREATE UNIQUE INDEX repositories_provider_id_key
    ON repositories (provider, host, provider_repo_id)
    WHERE provider_repo_id IS NOT NULL;
CREATE INDEX repositories_org ON repositories (lower(org)) WHERE active;

CREATE TABLE repository_events (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN
                    ('discovered', 'renamed', 'transferred', 'parked', 'unparked', 'removed', 'suspended', 'unsuspended')),
    detail        JSONB NOT NULL DEFAULT '{}',
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX repository_events_repo ON repository_events (repository_id, occurred_at DESC);

-- Rollout follows the version activated most recently, so a revert to an
-- earlier version is honoured (DESIGN-0030 D14). Activation is the
-- migrate Job's, as the owner; the evaluator only completes rollouts.
CREATE TABLE policy_versions (
    version              TEXT PRIMARY KEY,
    first_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    activated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    rollout_completed_at TIMESTAMPTZ,
    -- Controls, assignments and scope; never template bodies. Read by the
    -- API's policy view (DESIGN-0027).
    summary              JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX policy_versions_activated ON policy_versions (activated_at DESC);

CREATE TABLE checks (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repository_id  BIGINT NOT NULL REFERENCES repositories ON DELETE CASCADE,
    -- Idempotency key from the control plane (workflow id / run id /
    -- iteration). A retried record step finds its row already final.
    check_key      TEXT NOT NULL UNIQUE,
    trigger        TEXT NOT NULL CHECK (trigger IN
                     ('schedule', 'webhook', 'push', 'discovery', 'policy_rollout', 'pr_event', 'manual')),
    outcome        TEXT NOT NULL CHECK (outcome IN ('pending', 'success', 'error', 'skipped')),
    error          TEXT,
    -- Per control: status, fingerprint, rule results and resource reads,
    -- staged until the record step commits them; NULLed when finalized.
    pending_result JSONB,
    policy_version TEXT NOT NULL,
    started_at     TIMESTAMPTZ NOT NULL,
    finished_at    TIMESTAMPTZ
);

CREATE INDEX checks_repo_time ON checks (repository_id, finished_at DESC);
CREATE INDEX checks_finished ON checks (finished_at);

CREATE TABLE service_runs (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- discovery, policy_rollout and snapshot are the evaluator's; sweep,
    -- maintenance and remediation_discovery (the Remediation App's
    -- discovery) the remediator's.
    kind        TEXT NOT NULL CHECK (kind IN
                  ('discovery', 'snapshot', 'policy_rollout', 'sweep', 'maintenance', 'remediation_discovery')),
    outcome     TEXT NOT NULL CHECK (outcome IN ('success', 'error')),
    started_at  TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    detail      JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX service_runs_kind_time ON service_runs (kind, finished_at DESC);

-- The grant matrix for these tables (DESIGN-0032 § Two application
-- roles). Both roles read every table; writes are by column.
GRANT SELECT ON repositories, repository_events, policy_versions, checks, service_runs
    TO rg_evaluator, rg_remediator;

-- Discovery, identity and parking.
GRANT INSERT ON repositories TO rg_evaluator;
GRANT UPDATE (provider, host, org, name, provider_repo_id, active, park_reason, parked_at, next_due_at, last_error)
    ON repositories TO rg_evaluator;

GRANT INSERT ON repository_events TO rg_evaluator;

GRANT UPDATE (rollout_completed_at) ON policy_versions TO rg_evaluator;

-- Staging, finalisation and the retention prune.
GRANT INSERT, DELETE ON checks TO rg_evaluator;
GRANT UPDATE (outcome, error, pending_result, policy_version, finished_at) ON checks TO rg_evaluator;

GRANT INSERT ON service_runs TO rg_evaluator, rg_remediator;

-- The read-only API role is operator-provisioned, as on the rc line.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'repoguardian_ro') THEN
        GRANT SELECT ON repositories, repository_events, policy_versions, checks, service_runs TO repoguardian_ro;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO repoguardian_ro;
    ELSE
        RAISE NOTICE 'role repoguardian_ro does not exist; skipping read-only grants';
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'repoguardian_ro') THEN
        ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE SELECT ON TABLES FROM repoguardian_ro;
    END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE IF EXISTS service_runs;
DROP TABLE IF EXISTS checks;
DROP TABLE IF EXISTS policy_versions;
DROP TABLE IF EXISTS repository_events;
DROP TABLE IF EXISTS repositories;
