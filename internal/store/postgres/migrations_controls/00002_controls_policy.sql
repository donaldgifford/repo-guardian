-- DESIGN-0030 § Data model and DESIGN-0032 § Two application roles,
-- 00002_controls_policy (IMPL-0029 4.1): what resolution writes per
-- repository, and the per-App access facts it reads.
--
-- IMPL-0028 created neither app_installations nor app_repository_access,
-- so they are created here.

-- +goose Up

-- Assignment and state changes are on the repository timeline (DESIGN-0030
-- § Data model).
ALTER TABLE repository_events DROP CONSTRAINT repository_events_kind_check;
ALTER TABLE repository_events ADD CONSTRAINT repository_events_kind_check CHECK (kind IN
    ('discovered', 'renamed', 'transferred', 'parked', 'unparked', 'removed', 'suspended', 'unsuspended',
     'assignment_changed'));

-- Epochs fence results and writes (D14). They come from one sequence, so
-- an assignment withdrawn and re-added can never reuse an epoch an old
-- run still holds (DESIGN-0032 AR-0032-06).
CREATE SEQUENCE assignment_epoch_seq AS BIGINT;

-- Why a repository has, or has not, assignments; rewritten on resolution.
CREATE TABLE repository_policy_state (
    repository_id       BIGINT PRIMARY KEY REFERENCES repositories ON DELETE CASCADE,
    state               TEXT NOT NULL CHECK (state IN ('managed', 'excluded', 'unmanaged', 'parked')),
    reason              TEXT,
    max_open_prs        INT NOT NULL,
    reopen_after        INTERVAL NOT NULL,
    -- The one column rg_remediator may UPDATE, which is what its
    -- SELECT ... FOR UPDATE on the row needs (DESIGN-0032 AR-0032-05).
    last_remediation_at TIMESTAMPTZ,
    policy_version      TEXT NOT NULL REFERENCES policy_versions (version),
    resolved_at         TIMESTAMPTZ NOT NULL
);

-- The resolved assignments, one row per repository and control name: the
-- resource-ownership invariant as a key (DESIGN-0030 D12).
CREATE TABLE control_assignments (
    repository_id    BIGINT NOT NULL REFERENCES repositories ON DELETE CASCADE,
    control_id       TEXT NOT NULL,
    control_version  INT NOT NULL,
    revision         TEXT NOT NULL,
    state            TEXT NOT NULL CHECK (state IN ('active', 'excluded', 'conflict')),
    decision         TEXT NOT NULL CHECK (decision IN ('baseline', 'replaced', 'org_added', 'excluded')),
    baseline_control TEXT,
    source           TEXT NOT NULL,
    source_block     TEXT,
    -- Remediation priority (DESIGN-0032 AR-0032-05).
    ordinal          INT NOT NULL,
    replaced         TEXT,
    excluded_by      TEXT,
    reason           TEXT,
    error            TEXT,
    requested_mode   TEXT NOT NULL CHECK (requested_mode IN ('evaluate', 'remediate')),
    effective_mode   TEXT NOT NULL CHECK (effective_mode IN ('evaluate', 'remediate')),
    mode_source      TEXT NOT NULL,
    mode_reason      TEXT CHECK (mode_reason IN ('remediation_app_no_access', 'remediation_app_suspended')),
    epoch            BIGINT NOT NULL,
    policy_version   TEXT NOT NULL REFERENCES policy_versions (version),
    resolved_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (repository_id, control_id),
    CHECK (state <> 'conflict' OR error IS NOT NULL)
);

CREATE INDEX control_assignments_active ON control_assignments (control_id) WHERE state = 'active';

-- Per-App installation facts and repository membership (DESIGN-0030 D15),
-- each App refreshing its own rows through its own discovery and
-- lifecycle events.
CREATE TABLE app_installations (
    app                  TEXT NOT NULL CHECK (app IN ('eval', 'remediate')),
    installation_id      BIGINT NOT NULL,
    account_login        TEXT NOT NULL,
    suspended_at         TIMESTAMPTZ,
    repository_selection TEXT NOT NULL CHECK (repository_selection IN ('all', 'selected')),
    seen_at              TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (app, installation_id)
);

CREATE TABLE app_repository_access (
    repository_id   BIGINT NOT NULL REFERENCES repositories ON DELETE CASCADE,
    app             TEXT NOT NULL CHECK (app IN ('eval', 'remediate')),
    installation_id BIGINT NOT NULL,
    seen_at         TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (repository_id, app),
    FOREIGN KEY (app, installation_id) REFERENCES app_installations ON DELETE CASCADE
);

-- Each App writes only its own rows, and both read every row: a policy
-- declared without a command is FOR ALL and would filter reads too,
-- hiding the Remediation App's access from resolution, so every write
-- policy is paired with a FOR SELECT USING (true). A write the policy
-- fences affects zero rows without an error. The owner bypasses RLS.
ALTER TABLE app_installations ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_repository_access ENABLE ROW LEVEL SECURITY;

CREATE POLICY app_installations_read ON app_installations FOR SELECT TO rg_evaluator, rg_remediator USING (true);
CREATE POLICY app_installations_eval ON app_installations TO rg_evaluator
    USING (app = 'eval') WITH CHECK (app = 'eval');
CREATE POLICY app_installations_remediate ON app_installations TO rg_remediator
    USING (app = 'remediate') WITH CHECK (app = 'remediate');

CREATE POLICY app_repository_access_read ON app_repository_access FOR SELECT TO rg_evaluator, rg_remediator USING (true);
CREATE POLICY app_repository_access_eval ON app_repository_access TO rg_evaluator
    USING (app = 'eval') WITH CHECK (app = 'eval');
CREATE POLICY app_repository_access_remediate ON app_repository_access TO rg_remediator
    USING (app = 'remediate') WITH CHECK (app = 'remediate');

-- The grant matrix (DESIGN-0032 § Two application roles).
GRANT SELECT ON repository_policy_state, control_assignments, app_installations, app_repository_access
    TO rg_evaluator, rg_remediator;

GRANT INSERT, UPDATE, DELETE ON repository_policy_state, control_assignments TO rg_evaluator;
GRANT UPDATE (last_remediation_at) ON repository_policy_state TO rg_remediator;
GRANT USAGE ON SEQUENCE assignment_epoch_seq TO rg_evaluator;

GRANT INSERT, UPDATE, DELETE ON app_installations, app_repository_access TO rg_evaluator, rg_remediator;

-- +goose Down
DROP TABLE IF EXISTS app_repository_access;
DROP TABLE IF EXISTS app_installations;
DROP TABLE IF EXISTS control_assignments;
DROP TABLE IF EXISTS repository_policy_state;
DROP SEQUENCE IF EXISTS assignment_epoch_seq;

DELETE FROM repository_events WHERE kind = 'assignment_changed';
ALTER TABLE repository_events DROP CONSTRAINT repository_events_kind_check;
ALTER TABLE repository_events ADD CONSTRAINT repository_events_kind_check CHECK (kind IN
    ('discovered', 'renamed', 'transferred', 'parked', 'unparked', 'removed', 'suspended', 'unsuspended'));
