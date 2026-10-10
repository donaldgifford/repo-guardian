//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
)

// NOTE: IMPL-0028 task 0.11 (INV-0022 spike 6). A cut-down controls
// schema with DESIGN-0032's grant matrix, exercised as the two
// application roles. Phase 6 replaces it with the real 00001-00004 chain
// and the full grant suite.
const spikeRolesDDL = `
CREATE TABLE app_repository_access (
    repository_id BIGINT NOT NULL,
    app           TEXT   NOT NULL CHECK (app IN ('eval', 'remediate')),
    PRIMARY KEY (repository_id, app)
);
ALTER TABLE app_repository_access ENABLE ROW LEVEL SECURITY;
CREATE POLICY eval_rows ON app_repository_access TO rg_evaluator USING (app = 'eval');
CREATE POLICY remediate_rows ON app_repository_access TO rg_remediator USING (app = 'remediate');

CREATE TABLE repository_policy_state (
    repository_id       BIGINT PRIMARY KEY,
    state               TEXT NOT NULL,
    last_remediation_at TIMESTAMPTZ
);

CREATE TABLE control_results (
    repository_id         BIGINT NOT NULL,
    control_id            TEXT   NOT NULL,
    status                TEXT   NOT NULL,
    eval_generation       BIGINT NOT NULL DEFAULT 1,
    remediated_generation BIGINT,
    hold                  TEXT,
    PRIMARY KEY (repository_id, control_id)
);

CREATE TABLE rule_results (
    repository_id BIGINT NOT NULL,
    control_id    TEXT   NOT NULL,
    rule_id       TEXT   NOT NULL,
    status        TEXT   NOT NULL,
    PRIMARY KEY (repository_id, control_id, rule_id),
    FOREIGN KEY (repository_id, control_id) REFERENCES control_results ON DELETE CASCADE
);

CREATE TABLE remediations (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    repository_id     BIGINT NOT NULL,
    control_id        TEXT   NOT NULL,
    state             TEXT   NOT NULL,
    observed_head_sha TEXT
);

CREATE TABLE remediation_steps (
    id             BIGSERIAL PRIMARY KEY,
    remediation_id BIGINT NOT NULL,
    step           TEXT   NOT NULL
);

GRANT SELECT ON ALL TABLES IN SCHEMA public TO rg_evaluator, rg_remediator;

GRANT INSERT, UPDATE, DELETE ON app_repository_access TO rg_evaluator, rg_remediator;
GRANT INSERT, UPDATE, DELETE ON repository_policy_state TO rg_evaluator;
GRANT UPDATE (last_remediation_at) ON repository_policy_state TO rg_remediator;
GRANT INSERT, DELETE, UPDATE (status, eval_generation) ON control_results TO rg_evaluator;
GRANT UPDATE (remediated_generation, hold) ON control_results TO rg_remediator;
GRANT INSERT, UPDATE ON rule_results TO rg_evaluator;
GRANT UPDATE (state, observed_head_sha) ON remediations TO rg_evaluator;
GRANT INSERT, DELETE, UPDATE (state) ON remediations TO rg_remediator;
GRANT INSERT ON remediation_steps TO rg_remediator;
`

func TestSpike_ControlsRoles(t *testing.T) {
	dsns := pgtest.ControlsRoles(t, pgtest.Start(t))

	run(t, dsns.Owner, spikeRolesDDL)
	run(t, dsns.Owner, `
INSERT INTO app_repository_access VALUES (1, 'eval'), (1, 'remediate');
INSERT INTO repository_policy_state VALUES (1, 'managed', NULL);
INSERT INTO control_results (repository_id, control_id, status) VALUES (1, 'codeowners', 'non_compliant');
INSERT INTO rule_results VALUES (1, 'codeowners', 'exists', 'non_compliant');`)

	cases := []struct {
		name   string
		dsn    string
		sql    string
		denied bool
	}{
		{
			"evaluator upserts its columns", dsns.Evaluator,
			`INSERT INTO control_results (repository_id, control_id, status) VALUES (1, 'codeowners', 'compliant')
			 ON CONFLICT (repository_id, control_id) DO UPDATE SET status = excluded.status, eval_generation = control_results.eval_generation + 1`, false,
		},
		{
			"evaluator cannot touch the remediator's columns", dsns.Evaluator,
			`UPDATE control_results SET hold = 'pr_cap'`, true,
		},
		{
			"remediator updates its columns", dsns.Remediator,
			`UPDATE control_results SET remediated_generation = 1, hold = NULL`, false,
		},
		{
			"remediator cannot save a whole row", dsns.Remediator,
			`UPDATE control_results SET status = 'compliant', hold = NULL`, true,
		},
		{
			"remediator row-locks policy state with one column grant", dsns.Remediator,
			`SELECT 1 FROM repository_policy_state WHERE repository_id = 1 FOR UPDATE`, false,
		},
		{
			"remediator inserts into an identity-keyed table without sequence USAGE", dsns.Remediator,
			`INSERT INTO remediations (repository_id, control_id, state) VALUES (1, 'codeowners', 'reserved')`, false,
		},
		{
			"remediator inserts into a BIGSERIAL table without sequence USAGE", dsns.Remediator,
			`INSERT INTO remediation_steps (remediation_id, step) VALUES (1, 'commit')`, true,
		},
		{
			"remediator cannot rewrite an append-only step", dsns.Remediator,
			`UPDATE remediation_steps SET step = 'x'`, true,
		},
		{
			"withdrawal cascades to rule_results without DELETE there", dsns.Evaluator,
			`DELETE FROM control_results WHERE control_id = 'codeowners'`, false,
		},
		{
			"all writes both Apps' rows", dsns.All,
			`UPDATE app_repository_access SET repository_id = 1`, false,
		},
	}

	for _, tc := range cases {
		err := exec(t, tc.dsn, tc.sql)
		if tc.denied && err == nil {
			t.Errorf("%s: allowed, want permission denied", tc.name)
		}

		if !tc.denied && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}

		t.Logf("%s: err=%v", tc.name, err)
	}

	var left int
	queryRow(t, dsns.Owner, `SELECT count(*) FROM rule_results`, &left)
	t.Logf("rule_results after withdrawal: %d", left)

	// Resolution needs the Remediation App's access (`remediable`) while
	// running as the evaluator. The FOR ALL policy above hides it.
	var seen int
	queryRow(t, dsns.Evaluator, `SELECT count(*) FROM app_repository_access WHERE repository_id = 1`, &seen)
	t.Logf("evaluator sees %d of 2 access rows under the design's policies", seen)

	run(t, dsns.Owner, `
CREATE POLICY read_all ON app_repository_access FOR SELECT TO rg_evaluator, rg_remediator USING (true);`)
	queryRow(t, dsns.Evaluator, `SELECT count(*) FROM app_repository_access WHERE repository_id = 1`, &seen)
	t.Logf("evaluator sees %d of 2 access rows with a FOR SELECT USING (true) policy", seen)

	if err := exec(t, dsns.Evaluator, `UPDATE app_repository_access SET repository_id = 1 WHERE app = 'remediate'`); err != nil {
		t.Errorf("evaluator update of remediate rows: %v", err)
	}

	var changed int
	queryRow(t, dsns.Owner, `SELECT count(*) FROM app_repository_access WHERE app = 'remediate'`, &changed)
	t.Logf("remediate rows after the evaluator's update attempt: %d (RLS filters the update to zero rows, no error)", changed)
}

func run(t *testing.T, dsn, sql string) {
	t.Helper()

	if err := exec(t, dsn, sql); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
}

func exec(t *testing.T, dsn, sql string) error {
	t.Helper()

	ctx := context.Background()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, sql)

	return err
}

func queryRow(t *testing.T, dsn, sql string, dest any) {
	t.Helper()

	ctx := context.Background()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if err := conn.QueryRow(ctx, sql).Scan(dest); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}
