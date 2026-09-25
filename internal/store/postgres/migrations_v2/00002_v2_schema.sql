-- DESIGN-0025 § Data model: the v2 findings schema.
--
-- v1's tables (repo_state, rule_state, compliance_snapshot,
-- schema_migrations) are never touched here; they stay for the rollback
-- window until the v2.1 drop migration (DESIGN-0025 OQ6).

-- +goose Up
CREATE TABLE installations (
    installation_id  BIGINT PRIMARY KEY,
    provider         TEXT NOT NULL DEFAULT 'github',
    host             TEXT NOT NULL DEFAULT 'github.com',
    account_login    TEXT NOT NULL,
    suspended_at     TIMESTAMPTZ,
    removed_at       TIMESTAMPTZ,
    rate_limit       INT,
    rate_remaining   INT,
    rate_reset_at    TIMESTAMPTZ,
    rate_observed_at TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE repositories (
    id                 BIGSERIAL PRIMARY KEY,
    provider           TEXT NOT NULL DEFAULT 'github',
    host               TEXT NOT NULL DEFAULT 'github.com',
    org                TEXT NOT NULL,
    name               TEXT NOT NULL,
    provider_repo_id   BIGINT,
    installation_id    BIGINT NOT NULL REFERENCES installations,
    active             BOOLEAN NOT NULL DEFAULT true,
    park_reason        TEXT CHECK (park_reason IN
                         ('access_denied', 'archived', 'fork', 'removed', 'installation_removed', 'unknown')),
    parked_at          TIMESTAMPTZ,
    discovered_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    next_due_at        TIMESTAMPTZ,
    last_checked_at    TIMESTAMPTZ,
    last_check_outcome TEXT NOT NULL DEFAULT 'pending'
                         CHECK (last_check_outcome IN ('pending', 'success', 'error', 'skipped')),
    last_error         TEXT,
    policy_version     TEXT NOT NULL DEFAULT '',
    catalog_parse_ok   BOOLEAN
);

CREATE UNIQUE INDEX repositories_name_key
    ON repositories (provider, host, lower(org), lower(name));
CREATE UNIQUE INDEX repositories_provider_id_key
    ON repositories (provider, host, provider_repo_id)
    WHERE provider_repo_id IS NOT NULL;
CREATE INDEX repositories_installation ON repositories (installation_id) WHERE active;
CREATE INDEX repositories_org ON repositories (lower(org)) WHERE active;

CREATE TABLE findings (
    repository_id     BIGINT NOT NULL REFERENCES repositories ON DELETE CASCADE,
    rule_kind         TEXT NOT NULL CHECK (rule_kind IN ('file', 'setting', 'branch_protection')),
    rule_name         TEXT NOT NULL,
    status            TEXT NOT NULL
                        CHECK (status IN ('compliant', 'non_compliant', 'not_applicable', 'unknown')),
    reason            TEXT,
    remediation       TEXT NOT NULL DEFAULT 'none'
                        CHECK (remediation IN ('none', 'pr_open', 'foreign_pr', 'applied', 'dry_run', 'disabled')),
    evidence          JSONB NOT NULL DEFAULT '{}',
    evidence_version  SMALLINT NOT NULL DEFAULT 1,
    status_since      TIMESTAMPTZ NOT NULL,
    last_evaluated_at TIMESTAMPTZ NOT NULL,
    policy_version    TEXT NOT NULL,
    PRIMARY KEY (repository_id, rule_kind, rule_name)
);

CREATE INDEX findings_rule_status ON findings (rule_kind, rule_name, status);
CREATE INDEX findings_failing ON findings (status_since) WHERE status = 'non_compliant';

CREATE TABLE checks (
    id             BIGSERIAL PRIMARY KEY,
    repository_id  BIGINT NOT NULL REFERENCES repositories ON DELETE CASCADE,
    -- Idempotency key from the control plane (workflow id / run id /
    -- iteration). A retried RecordCheck finds its row already final.
    check_key      TEXT NOT NULL UNIQUE,
    trigger        TEXT NOT NULL CHECK (trigger IN
                     ('schedule', 'webhook', 'push', 'discovery', 'policy_rollout', 'bootstrap')),
    outcome        TEXT NOT NULL CHECK (outcome IN ('pending', 'success', 'error', 'skipped')),
    error          TEXT,
    -- Outcome set handed from CheckRepo to RecordCheck (DESIGN-0026
    -- OQ17); NULLed when the check is finalized.
    pending_result JSONB,
    policy_version TEXT NOT NULL,
    started_at     TIMESTAMPTZ NOT NULL,
    finished_at    TIMESTAMPTZ
);

CREATE INDEX checks_repo_time ON checks (repository_id, finished_at DESC);
CREATE INDEX checks_finished ON checks (finished_at);

-- Append-only by convention and by grant (see the grant block below).
CREATE TABLE finding_events (
    id               BIGSERIAL PRIMARY KEY,
    repository_id    BIGINT NOT NULL REFERENCES repositories ON DELETE CASCADE,
    rule_kind        TEXT NOT NULL,
    rule_name        TEXT NOT NULL,
    check_id         BIGINT REFERENCES checks ON DELETE SET NULL,
    from_status      TEXT,
    to_status        TEXT,
    from_reason      TEXT,
    to_reason        TEXT,
    from_remediation TEXT,
    to_remediation   TEXT,
    evidence         JSONB NOT NULL DEFAULT '{}',
    policy_version   TEXT NOT NULL,
    occurred_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX finding_events_repo ON finding_events (repository_id, occurred_at DESC);
CREATE INDEX finding_events_time ON finding_events (occurred_at DESC);
CREATE INDEX finding_events_check ON finding_events (check_id);

CREATE TABLE repository_events (
    id            BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN
                    ('discovered', 'renamed', 'transferred', 'parked', 'unparked', 'removed')),
    detail        JSONB NOT NULL DEFAULT '{}',
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX repository_events_repo ON repository_events (repository_id, occurred_at DESC);

CREATE TABLE policy_versions (
    version              TEXT PRIMARY KEY,
    first_seen_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    rollout_completed_at TIMESTAMPTZ,
    -- Rules with kind, name, description, scope and check mode; never
    -- template bodies. Read by the API's policy view (DESIGN-0027).
    summary              JSONB NOT NULL DEFAULT '{}'
);

CREATE TABLE service_runs (
    id          BIGSERIAL PRIMARY KEY,
    kind        TEXT NOT NULL CHECK (kind IN ('discovery', 'snapshot', 'policy_rollout', 'bootstrap')),
    outcome     TEXT NOT NULL CHECK (outcome IN ('success', 'error')),
    started_at  TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    detail      JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX service_runs_kind_time ON service_runs (kind, finished_at DESC);

CREATE TABLE compliance_snapshots (
    org            TEXT NOT NULL,
    rule_kind      TEXT NOT NULL,
    rule_name      TEXT NOT NULL,
    snapshot_at    TIMESTAMPTZ NOT NULL,
    compliant      INT NOT NULL,
    non_compliant  INT NOT NULL,
    not_applicable INT NOT NULL,
    unknown        INT NOT NULL,
    PRIMARY KEY (org, rule_kind, rule_name, snapshot_at)
);

-- +goose Down
DROP TABLE IF EXISTS compliance_snapshots;
DROP TABLE IF EXISTS service_runs;
DROP TABLE IF EXISTS policy_versions;
DROP TABLE IF EXISTS repository_events;
DROP TABLE IF EXISTS finding_events;
DROP TABLE IF EXISTS checks;
DROP TABLE IF EXISTS findings;
DROP TABLE IF EXISTS repositories;
DROP TABLE IF EXISTS installations;
