-- IMPL-0011 Phase 2 / DESIGN-0012 §Data Model.
-- repo_state captures the last reconcile attempt per
-- (installation_id, owner, repo). Two indexes drive the sweep:
-- freshness (last_checked_at NULLS FIRST) and policy-version mismatch.

CREATE TABLE repo_state (
    installation_id   BIGINT  NOT NULL,
    owner             TEXT    NOT NULL,
    repo              TEXT    NOT NULL,
    last_checked_at   TIMESTAMP WITH TIME ZONE,
    last_check_status TEXT    NOT NULL DEFAULT 'pending',
    last_error        TEXT,
    policy_version    TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (installation_id, owner, repo)
);

CREATE INDEX idx_repo_state_freshness
    ON repo_state(last_checked_at NULLS FIRST);

CREATE INDEX idx_repo_state_policy_version
    ON repo_state(policy_version);
