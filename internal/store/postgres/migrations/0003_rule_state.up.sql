-- IMPL-0023 Phase 1 / DESIGN-0022 §Data Model. Additive only.
--
-- rule_state records the outcome of every rule evaluation, written in
-- the same worker write-back that already persists repo_state. One row
-- per (installation, owner, repo, rule). actionable_since is the
-- "missing since 2026-06-14" the report needs: set on the false->true
-- transition, cleared on true->false, preserved on true->true. The
-- transition CASE lives in the upsert (see postgres.go), not here.
--
-- rule_kind is one of 'file' | 'setting' | 'branch_protection'.
-- Branch-protection rules are included from day one: they have no
-- mismatch signal at all today (INV-0013 Finding B), so this closes
-- that hole rather than porting an existing metric.

CREATE TABLE rule_state (
    installation_id  BIGINT      NOT NULL,
    owner            TEXT        NOT NULL,
    repo             TEXT        NOT NULL,
    rule_name        TEXT        NOT NULL,
    rule_kind        TEXT        NOT NULL,
    actionable       BOOLEAN     NOT NULL,
    actionable_since TIMESTAMPTZ,
    policy_version   TEXT        NOT NULL DEFAULT '',
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (installation_id, owner, repo, rule_name)
);

-- Posture query support: count actionable per (owner, rule). Partial
-- because the posture exporter only ever filters on actionable rows;
-- the tracked count is a plain sequential aggregate over the same
-- GROUP BY and does not need its own index.
CREATE INDEX idx_rule_state_actionable
    ON rule_state (owner, rule_name) WHERE actionable;

-- compliance_snapshot gives quarter-over-quarter history independent
-- of Prometheus retention. Written by the leader-gated
-- compliance-snapshot handler as INSERT ... SELECT from the posture
-- query. Volume is ~orgs x rules rows per day (~120/day at target
-- scale), so no retention machinery ships initially.
CREATE TABLE compliance_snapshot (
    org              TEXT        NOT NULL,
    rule_name        TEXT        NOT NULL,
    actionable_count INT         NOT NULL,
    tracked_count    INT         NOT NULL,
    snapshot_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (org, rule_name, snapshot_at)
);

-- Per-repo, not per-rule: whether this repo's catalog-info.yaml parsed
-- on the last check. NULL means no catalog rule was evaluated, which is
-- distinct from "evaluated and failed to parse" (false).
ALTER TABLE repo_state ADD COLUMN catalog_parse_ok BOOLEAN;
