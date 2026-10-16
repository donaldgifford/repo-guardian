-- DESIGN-0032 § Data model, 00003_controls_results (IMPL-0029 4.2, OQ7):
-- evaluation results, the remediation journal, the two views the
-- remediator schedules from, and the grant matrix.
--
-- Surrogate keys are identity columns rather than the design sketch's
-- BIGSERIAL, so neither role needs a sequence grant (DESIGN-0032 § Two
-- application roles).

-- +goose Up
CREATE TABLE control_results (
    repository_id         BIGINT NOT NULL REFERENCES repositories ON DELETE CASCADE,
    control_id            TEXT NOT NULL,
    control_version       INT NOT NULL,
    revision              TEXT NOT NULL,
    assignment_epoch      BIGINT NOT NULL,
    status                TEXT NOT NULL CHECK (status IN ('compliant', 'non_compliant', 'unknown', 'not_applicable')),
    fingerprint           TEXT NOT NULL,
    -- sha256(epoch, generation, apply, effective mode, remediable, revision):
    -- what remediation should now do.
    intent_revision       TEXT NOT NULL,
    eval_generation       BIGINT NOT NULL,
    -- Remediation's alone (record-is-narrow).
    remediated_generation BIGINT NOT NULL DEFAULT 0,
    hold                  TEXT CHECK (hold IN ('pr_cap', 'foreign_branch', 'stale_branch', 'conflict', 'permission')),
    hold_since            TIMESTAMPTZ,
    evaluated_sha         TEXT NOT NULL,
    evaluated_at          TIMESTAMPTZ NOT NULL,
    last_changed_at       TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (repository_id, control_id)
);

CREATE TABLE rule_results (
    repository_id    BIGINT NOT NULL,
    control_id       TEXT NOT NULL,
    rule_id          TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('pass', 'fail', 'error', 'unknown', 'not_applicable')),
    remediate        BOOLEAN NOT NULL,
    -- Bounded at 16 KiB per rule (DESIGN-0031 AR-0031-09).
    evidence         JSONB NOT NULL,
    evidence_kind    TEXT NOT NULL,
    evidence_version SMALLINT NOT NULL,
    PRIMARY KEY (repository_id, control_id, rule_id),
    FOREIGN KEY (repository_id, control_id) REFERENCES control_results ON DELETE CASCADE
);

-- Append-only by grant. to_status is NULL when the result is withdrawn
-- (DESIGN-0030 D6); rule_id is NULL for a control-status transition.
CREATE TABLE result_events (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repository_id    BIGINT NOT NULL REFERENCES repositories ON DELETE CASCADE,
    control_id       TEXT NOT NULL,
    control_version  INT NOT NULL,
    revision         TEXT NOT NULL,
    assignment_epoch BIGINT NOT NULL,
    rule_id          TEXT,
    from_status      TEXT,
    to_status        TEXT,
    eval_generation  BIGINT NOT NULL,
    occurred_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX result_events_repo ON result_events (repository_id, occurred_at DESC);

-- One durable operation per PR, recommendation or direct apply.
CREATE TABLE remediations (
    id                    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repository_id         BIGINT NOT NULL REFERENCES repositories ON DELETE CASCADE,
    control_id            TEXT NOT NULL,
    assignment_epoch      BIGINT NOT NULL,
    operation_key         TEXT NOT NULL UNIQUE,
    kind                  TEXT NOT NULL CHECK (kind IN ('pull_request', 'api')),
    state                 TEXT NOT NULL CHECK (state IN (
                              'reserved', 'open', 'merged', 'closed_compliant', 'closed_by_user', 'closed_withdrawn',
                              'recommended', 'superseded', 'withdrawn', 'resolved', 'applied', 'failed')),
    intent_revision       TEXT NOT NULL,
    base_sha              TEXT,
    branch                TEXT,
    expected_head         TEXT,
    reserved_until        TIMESTAMPTZ,
    pr_number             INT,
    pr_url                TEXT,
    observed_head_sha     TEXT,
    observed_at           TIMESTAMPTZ,
    acknowledged_head_sha TEXT,
    proposal              JSONB,
    remediated_generation BIGINT NOT NULL,
    closed_generation     BIGINT,
    next_eligible_at      TIMESTAMPTZ,
    hold                  TEXT CHECK (hold IN ('permission')),
    created_at            TIMESTAMPTZ NOT NULL,
    last_run_at           TIMESTAMPTZ NOT NULL,
    closed_at             TIMESTAMPTZ,
    close_reason          TEXT,
    CHECK (kind <> 'pull_request' OR state IN ('reserved', 'open', 'merged', 'closed_compliant', 'closed_by_user', 'closed_withdrawn')),
    CHECK (kind <> 'api' OR state IN ('recommended', 'superseded', 'withdrawn', 'resolved', 'applied', 'failed')),
    CHECK (state <> 'reserved' OR reserved_until IS NOT NULL)
);

-- At most one slot-holding or resumable row per repository and control.
CREATE UNIQUE INDEX remediations_one_open
    ON remediations (repository_id, control_id) WHERE state IN ('reserved', 'open', 'recommended', 'failed');
-- The rows the PR cap counts.
CREATE INDEX remediations_slots
    ON remediations (repository_id) WHERE state IN ('reserved', 'open');

-- Append-only journal of every external step, written as each completes.
CREATE TABLE remediation_steps (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    remediation_id BIGINT NOT NULL REFERENCES remediations ON DELETE CASCADE,
    operation_key  TEXT NOT NULL,
    seq            INT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('branch_created', 'branch_updated', 'committed', 'reverted',
                                                 'pr_created', 'pr_updated', 'pr_rebased', 'pr_closed', 'api_write')),
    resource_key   TEXT,
    blob_sha       TEXT,
    committed_sha  TEXT,
    pr_number      INT,
    status         TEXT NOT NULL CHECK (status IN ('done', 'failed')),
    before         JSONB,
    after          JSONB,
    error          TEXT,
    occurred_at    TIMESTAMPTZ NOT NULL,
    UNIQUE (remediation_id, seq)
);

CREATE TABLE remediation_events (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    remediation_id BIGINT NOT NULL REFERENCES remediations ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    detail         JSONB NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL
);

-- needs_remediation as SQL: new remediation only.
CREATE VIEW remediation_due AS
SELECT cr.repository_id, cr.control_id, ca.ordinal
FROM control_results cr
JOIN control_assignments ca
  ON (ca.repository_id, ca.control_id) = (cr.repository_id, cr.control_id)
 AND ca.state = 'active' AND ca.epoch = cr.assignment_epoch
JOIN repository_policy_state ps ON ps.repository_id = cr.repository_id
LEFT JOIN LATERAL (
    SELECT r.* FROM remediations r
    WHERE (r.repository_id, r.control_id) = (cr.repository_id, cr.control_id)
    ORDER BY r.id DESC LIMIT 1
) r ON true
WHERE ps.state = 'managed'
  AND ca.effective_mode = 'remediate'
  AND cr.status = 'non_compliant'
  AND EXISTS (SELECT 1 FROM rule_results rr
              WHERE (rr.repository_id, rr.control_id) = (cr.repository_id, cr.control_id)
                AND rr.status = 'fail' AND rr.remediate)
  AND CASE
        WHEN r.id IS NULL
          OR r.state IN ('merged', 'closed_compliant', 'closed_withdrawn', 'superseded', 'withdrawn', 'resolved', 'applied')
             THEN cr.eval_generation > cr.remediated_generation
               OR cr.intent_revision IS DISTINCT FROM r.intent_revision
        WHEN r.state = 'open'
             THEN cr.eval_generation > cr.remediated_generation
               OR cr.intent_revision IS DISTINCT FROM r.intent_revision
               OR r.observed_head_sha IS DISTINCT FROM r.acknowledged_head_sha
        WHEN r.state = 'closed_by_user'
             THEN cr.eval_generation > r.closed_generation OR r.next_eligible_at <= now()
        WHEN r.state = 'failed'
             THEN true
        ELSE false
      END;

-- The lifecycle of durable rows, independent of a current failing result
-- (DESIGN-0032 AR-0032-04).
CREATE VIEW remediation_maintenance AS
SELECT m.* FROM (
    SELECT r.id AS remediation_id, r.repository_id, r.control_id, r.kind, r.state,
           CASE
             WHEN r.state = 'reserved' AND r.reserved_until < now()                                 THEN 'expire'
             WHEN ca.repository_id IS NULL OR ps.state <> 'managed'                                 THEN 'withdraw'
             WHEN r.state = 'recommended' AND cr.intent_revision IS DISTINCT FROM r.intent_revision THEN 'supersede'
             WHEN cr.status = 'compliant'                                                           THEN 'close_compliant'
           END AS action
    FROM remediations r
    LEFT JOIN control_assignments ca
           ON (ca.repository_id, ca.control_id) = (r.repository_id, r.control_id) AND ca.state = 'active'
    LEFT JOIN repository_policy_state ps ON ps.repository_id = r.repository_id
    LEFT JOIN control_results cr
           ON (cr.repository_id, cr.control_id) = (r.repository_id, r.control_id)
    WHERE r.state IN ('reserved', 'open', 'recommended')
) m
WHERE m.action IS NOT NULL;

-- Remediator expiry deletes only a reservation; every other row is kept.
ALTER TABLE remediations ENABLE ROW LEVEL SECURITY;

CREATE POLICY remediations_read ON remediations FOR SELECT TO rg_evaluator, rg_remediator USING (true);
CREATE POLICY remediations_observe ON remediations FOR UPDATE TO rg_evaluator USING (true) WITH CHECK (true);
CREATE POLICY remediations_insert ON remediations FOR INSERT TO rg_remediator WITH CHECK (true);
CREATE POLICY remediations_update ON remediations FOR UPDATE TO rg_remediator USING (true) WITH CHECK (true);
CREATE POLICY remediations_expire ON remediations FOR DELETE TO rg_remediator USING (state = 'reserved');

-- The grant matrix (DESIGN-0032 § Two application roles), verbatim.
GRANT SELECT ON control_results, rule_results, result_events, remediations, remediation_steps, remediation_events,
    remediation_due, remediation_maintenance TO rg_evaluator, rg_remediator;

GRANT INSERT, DELETE ON control_results TO rg_evaluator;
GRANT UPDATE (status, fingerprint, intent_revision, eval_generation, control_version, revision, assignment_epoch,
    evaluated_sha, evaluated_at, last_changed_at) ON control_results TO rg_evaluator;
GRANT UPDATE (remediated_generation, hold, hold_since) ON control_results TO rg_remediator;

GRANT INSERT, UPDATE, DELETE ON rule_results TO rg_evaluator;

GRANT INSERT ON result_events TO rg_evaluator;

GRANT UPDATE (state, closed_at, close_reason, closed_generation, next_eligible_at, observed_head_sha, observed_at)
    ON remediations TO rg_evaluator;
GRANT INSERT, DELETE ON remediations TO rg_remediator;
GRANT UPDATE (repository_id, control_id, assignment_epoch, operation_key, kind, state, intent_revision, base_sha,
    branch, expected_head, reserved_until, pr_number, pr_url, acknowledged_head_sha, proposal, remediated_generation,
    hold, created_at, last_run_at, closed_at, close_reason) ON remediations TO rg_remediator;

GRANT INSERT ON remediation_steps TO rg_remediator;

GRANT INSERT ON remediation_events TO rg_evaluator, rg_remediator;

-- Append-only: the owner is a separate role and neither application role
-- was granted these, so the revokes change nothing today; they state the
-- invariant where a later GRANT ALL would otherwise undo it.
REVOKE UPDATE, DELETE, TRUNCATE ON result_events, remediation_steps, remediation_events FROM rg_evaluator, rg_remediator;

-- +goose Down
DROP VIEW IF EXISTS remediation_maintenance;
DROP VIEW IF EXISTS remediation_due;
DROP TABLE IF EXISTS remediation_events;
DROP TABLE IF EXISTS remediation_steps;
DROP TABLE IF EXISTS remediations;
DROP TABLE IF EXISTS result_events;
DROP TABLE IF EXISTS rule_results;
DROP TABLE IF EXISTS control_results;
