//go:build integration

package postgres_test

import (
	"slices"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/store/postgres"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/pgtest"
)

// TestControlsChain_GrantsHoldAsEachRole is IMPL-0028 tasks 6.3 and 6.5:
// 00001_core's grants run as rg_evaluator, rg_remediator and rg_all,
// never as the owner, and every cross-writer statement is refused.
func TestControlsChain_GrantsHoldAsEachRole(t *testing.T) {
	dsns := pgtest.ControlsDB(t)

	run(t, dsns.Owner, `INSERT INTO policy_versions (version) VALUES ('c:1')`)

	cases := []struct {
		name   string
		dsn    string
		sql    string
		denied bool
	}{
		// The evaluator: discovery, identity, parking, checks, rollout.
		{"evaluator discovers", dsns.Evaluator,
			`INSERT INTO repositories (org, name, provider_repo_id) VALUES ('acme', 'api', 1)`, false},
		{"evaluator renames and parks", dsns.Evaluator,
			`UPDATE repositories SET name = 'api2', active = false, park_reason = 'archived', parked_at = now(), last_error = NULL`, false},
		{"evaluator records a repository event", dsns.Evaluator,
			`INSERT INTO repository_events (repository_id, kind) SELECT id, 'parked' FROM repositories`, false},
		{"evaluator stages a check", dsns.Evaluator,
			`INSERT INTO checks (repository_id, check_key, trigger, outcome, policy_version, started_at)
			 SELECT id, 'k1', 'manual', 'pending', 'c:1', now() FROM repositories`, false},
		{"evaluator finalizes a check", dsns.Evaluator,
			`UPDATE checks SET outcome = 'success', pending_result = NULL, finished_at = now()`, false},
		{"evaluator completes a rollout", dsns.Evaluator,
			`UPDATE policy_versions SET rollout_completed_at = now()`, false},
		{"evaluator records a discovery run", dsns.Evaluator,
			`INSERT INTO service_runs (kind, outcome, started_at, finished_at) VALUES ('discovery', 'success', now(), now())`, false},
		{"evaluator prunes checks", dsns.Evaluator, `DELETE FROM checks`, false},
		{"evaluator cannot rewrite discovered_at", dsns.Evaluator, `UPDATE repositories SET discovered_at = now()`, true},
		{"evaluator cannot delete a repository", dsns.Evaluator, `DELETE FROM repositories`, true},
		{"evaluator cannot activate a policy version", dsns.Evaluator,
			`INSERT INTO policy_versions (version) VALUES ('c:2')`, true},
		{"evaluator cannot move activated_at", dsns.Evaluator, `UPDATE policy_versions SET activated_at = now()`, true},
		{"evaluator cannot rewrite repository history", dsns.Evaluator, `UPDATE repository_events SET kind = 'removed'`, true},

		// The remediator reads everything and writes only its run rows.
		{"remediator reads repositories", dsns.Remediator, `SELECT id FROM repositories`, false},
		{"remediator records a sweep", dsns.Remediator,
			`INSERT INTO service_runs (kind, outcome, started_at, finished_at) VALUES ('sweep', 'success', now(), now())`, false},
		{"remediator cannot discover", dsns.Remediator,
			`INSERT INTO repositories (org, name) VALUES ('acme', 'web')`, true},
		{"remediator cannot park", dsns.Remediator, `UPDATE repositories SET active = false`, true},
		{"remediator cannot write repository events", dsns.Remediator,
			`INSERT INTO repository_events (repository_id, kind) SELECT id, 'removed' FROM repositories`, true},
		{"remediator cannot stage a check", dsns.Remediator,
			`INSERT INTO checks (repository_id, check_key, trigger, outcome, policy_version, started_at)
			 SELECT id, 'k2', 'manual', 'pending', 'c:1', now() FROM repositories`, true},
		{"remediator cannot complete a rollout", dsns.Remediator,
			`UPDATE policy_versions SET rollout_completed_at = now()`, true},

		// 00002: resolution, and each App's own access rows (RLS).
		{"evaluator resolves", dsns.Evaluator,
			`INSERT INTO repository_policy_state (repository_id, state, max_open_prs, reopen_after, policy_version, resolved_at)
			 SELECT id, 'managed', 3, '14 days', 'c:1', now() FROM repositories;
			 INSERT INTO control_assignments (repository_id, control_id, control_version, revision, state, decision, source,
			   ordinal, requested_mode, effective_mode, mode_source, epoch, policy_version, resolved_at)
			 SELECT id, 'codeowners', 1, 'r1', 'active', 'baseline', 'enterprise', 0, 'remediate', 'remediate', 'enterprise',
			   nextval('assignment_epoch_seq'), 'c:1', now() FROM repositories`, false},
		{"evaluator records an assignment change", dsns.Evaluator,
			`INSERT INTO repository_events (repository_id, kind) SELECT id, 'assignment_changed' FROM repositories`, false},
		{"evaluator writes its installation", dsns.Evaluator,
			`INSERT INTO app_installations (app, installation_id, account_login, repository_selection, seen_at)
			 VALUES ('eval', 10, 'acme', 'all', now())`, false},
		{"evaluator cannot write the Remediation App's installation", dsns.Evaluator,
			`INSERT INTO app_installations (app, installation_id, account_login, repository_selection, seen_at)
			 VALUES ('remediate', 11, 'acme', 'all', now())`, true},
		{"remediator writes its installation and access", dsns.Remediator,
			`INSERT INTO app_installations (app, installation_id, account_login, repository_selection, seen_at)
			 VALUES ('remediate', 20, 'acme', 'selected', now());
			 INSERT INTO app_repository_access (repository_id, app, installation_id, seen_at)
			 SELECT id, 'remediate', 20, now() FROM repositories`, false},
		{"remediator takes the slot column", dsns.Remediator,
			`SELECT repository_id FROM repository_policy_state FOR UPDATE;
			 UPDATE repository_policy_state SET last_remediation_at = now()`, false},
		{"remediator cannot resolve", dsns.Remediator, `UPDATE control_assignments SET effective_mode = 'evaluate'`, true},
		{"remediator cannot rewrite policy state", dsns.Remediator, `UPDATE repository_policy_state SET state = 'excluded'`, true},

		// 00003: results are the evaluator's, the journal the remediator's.
		{"evaluator records a result", dsns.Evaluator,
			`INSERT INTO control_results (repository_id, control_id, control_version, revision, assignment_epoch, status,
			   fingerprint, intent_revision, eval_generation, evaluated_sha, evaluated_at, last_changed_at)
			 SELECT id, 'codeowners', 1, 'r1', 1, 'non_compliant', 'f1', 'i1', 1, 'sha', now(), now() FROM repositories;
			 INSERT INTO rule_results (repository_id, control_id, rule_id, status, remediate, evidence, evidence_kind, evidence_version)
			 SELECT id, 'codeowners', 'exists', 'fail', true, '{}', 'k', 1 FROM repositories;
			 INSERT INTO result_events (repository_id, control_id, control_version, revision, assignment_epoch, to_status,
			   eval_generation, occurred_at)
			 SELECT id, 'codeowners', 1, 'r1', 1, 'non_compliant', 1, now() FROM repositories`, false},
		{"remediator reserves a slot", dsns.Remediator,
			`UPDATE control_results SET remediated_generation = 1, hold = NULL;
			 INSERT INTO remediations (repository_id, control_id, assignment_epoch, operation_key, kind, state, intent_revision,
			   reserved_until, remediated_generation, created_at, last_run_at)
			 SELECT id, 'codeowners', 1, 'op1', 'pull_request', 'reserved', 'i1', now(), 1, now(), now() FROM repositories;
			 INSERT INTO remediation_steps (remediation_id, operation_key, seq, kind, status, occurred_at)
			 SELECT id, 'op1', 1, 'branch_created', 'done', now() FROM remediations`, false},
		{"evaluator observes a PR", dsns.Evaluator,
			`UPDATE remediations SET observed_head_sha = 'h', observed_at = now()`, false},
		{"evaluator cannot acknowledge a head", dsns.Evaluator,
			`UPDATE remediations SET acknowledged_head_sha = 'h'`, true},
		{"remediator cannot write the evaluator's columns", dsns.Remediator,
			`UPDATE control_results SET eval_generation = 9`, true},
		{"remediator cannot write observations", dsns.Remediator, `UPDATE remediations SET observed_at = now()`, true},
		{"nobody rewrites result history", dsns.Evaluator, `DELETE FROM result_events`, true},
		{"nobody rewrites the journal", dsns.Remediator, `UPDATE remediation_steps SET status = 'failed'`, true},
		{"both read the scheduling views", dsns.Remediator,
			`SELECT * FROM remediation_due; SELECT * FROM remediation_maintenance`, false},

		// 00004: snapshots are the evaluator's.
		{"evaluator snapshots compliance", dsns.Evaluator,
			`INSERT INTO compliance_snapshots (org, control_id, decision, snapshot_at, compliant, non_compliant, pending, stale,
			   unmeasurable_access_denied, unmeasurable_suspended, unmeasurable_conflict, not_applicable, unknown, excluded, assigned)
			 VALUES ('acme', 'codeowners', 'baseline', now(), 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1)`, false},
		{"remediator cannot snapshot", dsns.Remediator,
			`INSERT INTO compliance_snapshots (org, control_id, decision, snapshot_at, compliant, non_compliant, pending, stale,
			   unmeasurable_access_denied, unmeasurable_suspended, unmeasurable_conflict, not_applicable, unknown, excluded, assigned)
			 VALUES ('acme', 'codeowners', 'baseline', now(), 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1)`, true},

		// all holds both sets through membership.
		{"all discovers and sweeps", dsns.All,
			`INSERT INTO repositories (org, name) VALUES ('acme', 'web');
			 INSERT INTO service_runs (kind, outcome, started_at, finished_at) VALUES ('maintenance', 'success', now(), now())`, false},
	}

	for _, tc := range cases {
		err := exec(t, tc.dsn, tc.sql)
		if tc.denied && err == nil {
			t.Errorf("%s: allowed, want permission denied", tc.name)
		}

		if !tc.denied && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

// TestControlsChain_DryRunRollsBack is IMPL-0028 task 6.6: the whole
// chain applies to an empty database in one transaction that leaves
// nothing behind.
func TestControlsChain_DryRunRollsBack(t *testing.T) {
	dsns := pgtest.ControlsRoles(t, pgtest.Start(t))

	db, err := postgres.OpenDB(dsns.Owner)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	applied, err := postgres.ControlsDryRun(t.Context(), db)
	if err != nil {
		t.Fatalf("ControlsDryRun: %v", err)
	}

	var want []int64
	for v := int64(1); v <= postgres.ControlsSchemaVersion; v++ {
		want = append(want, v)
	}

	if !slices.Equal(applied, want) {
		t.Errorf("ControlsDryRun applied %v, want %v", applied, want)
	}

	var tables int
	queryRow(t, dsns.Owner, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public'`, &tables)

	if tables != 0 {
		t.Errorf("dry run left %d tables behind, want 0", tables)
	}

	// Once applied, nothing is pending.
	pgDB := pgtest.ControlsDB(t)

	db2, err := postgres.OpenDB(pgDB.Owner)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	if applied, err := postgres.ControlsDryRun(t.Context(), db2); err != nil || len(applied) != 0 {
		t.Errorf("ControlsDryRun on a migrated database = %v, %v; want nothing pending", applied, err)
	}
}

// TestControlsChain_RefusesAnRCDatabase pins that the chain cannot be
// pointed at a database holding the rc's schema: it keeps its own
// version table, so it fails on its first CREATE TABLE.
func TestControlsChain_RefusesAnRCDatabase(t *testing.T) {
	dsns := pgtest.ControlsRoles(t, pgtest.Start(t))
	run(t, dsns.Owner, `CREATE TABLE repositories (id BIGINT)`)

	db, err := postgres.OpenDB(dsns.Owner)
	if err != nil {
		t.Fatal(err)
	}

	provider, err := postgres.NewControlsMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()

	if _, err := provider.Up(t.Context()); err == nil {
		t.Error("controls chain over an existing repositories table = nil, want an error")
	}
}

// TestControlsChain_AccessRowsAreFencedByApp pins the RLS pairing
// (DESIGN-0032 § Two application roles): each App's write policy fences
// the other App's rows to zero affected rows, and the paired SELECT
// policy keeps both Apps' rows visible to both roles, so resolution sees
// the Remediation App's access.
func TestControlsChain_AccessRowsAreFencedByApp(t *testing.T) {
	dsns := pgtest.ControlsDB(t)

	run(t, dsns.Owner, `INSERT INTO repositories (org, name) VALUES ('acme', 'api');
		INSERT INTO app_installations (app, installation_id, account_login, repository_selection, seen_at)
		VALUES ('eval', 1, 'acme', 'all', now()), ('remediate', 2, 'acme', 'all', now());
		INSERT INTO app_repository_access (repository_id, app, installation_id, seen_at)
		SELECT id, 'eval', 1, now() FROM repositories UNION ALL SELECT id, 'remediate', 2, now() FROM repositories`)

	for _, dsn := range []string{dsns.Evaluator, dsns.Remediator} {
		var n int
		queryRow(t, dsn, `SELECT count(*) FROM app_repository_access`, &n)

		if n != 2 {
			t.Errorf("visible access rows = %d, want both Apps' (2)", n)
		}
	}

	var gone int
	queryRow(t, dsns.Evaluator, `WITH d AS (DELETE FROM app_repository_access WHERE app = 'remediate' RETURNING 1) SELECT count(*) FROM d`, &gone)

	if gone != 0 {
		t.Errorf("evaluator deleted %d Remediation App access rows, want 0 (fenced)", gone)
	}
}

// TestControlsChain_RecordIsNarrow is IMPL-0029 4.8 (DESIGN-0032 § Two
// application roles), run as the roles, never the owner: the remediator
// may write none of the evaluator's columns on control_results,
// control_assignments or repository_policy_state (bar
// last_remediation_at), and no role may rewrite result_events. The
// evaluator's own transactions are exercised as rg_evaluator by the
// ControlsStore integration tests.
func TestControlsChain_RecordIsNarrow(t *testing.T) {
	dsns := pgtest.ControlsDB(t)

	evaluatorOwned := map[string][]string{
		"control_results": {"status", "fingerprint", "intent_revision", "eval_generation", "control_version", "revision",
			"assignment_epoch", "evaluated_sha", "evaluated_at", "last_changed_at"},
		"control_assignments": {"control_version", "revision", "state", "decision", "baseline_control", "source",
			"source_block", "ordinal", "replaced", "excluded_by", "reason", "error", "requested_mode", "effective_mode",
			"mode_source", "mode_reason", "epoch", "policy_version", "resolved_at"},
		"repository_policy_state": {"state", "reason", "max_open_prs", "reopen_after", "policy_version", "resolved_at"},
	}

	for table, cols := range evaluatorOwned {
		for _, col := range cols {
			if err := exec(t, dsns.Remediator, `UPDATE `+table+` SET `+col+` = `+col); err == nil {
				t.Errorf("remediator updated %s.%s, want permission denied", table, col)
			}

			if err := exec(t, dsns.Evaluator, `UPDATE `+table+` SET `+col+` = `+col); err != nil {
				t.Errorf("evaluator cannot update %s.%s: %v", table, col, err)
			}
		}

		if err := exec(t, dsns.Remediator, `DELETE FROM `+table); err == nil {
			t.Errorf("remediator deleted from %s, want permission denied", table)
		}
	}

	// The remediator's own columns, so the refusals above are not vacuous.
	for _, sql := range []string{
		`UPDATE repository_policy_state SET last_remediation_at = now()`,
		`UPDATE control_results SET remediated_generation = remediated_generation, hold = hold, hold_since = hold_since`,
	} {
		if err := exec(t, dsns.Remediator, sql); err != nil {
			t.Errorf("remediator: %s: %v", sql, err)
		}
	}

	for _, dsn := range []string{dsns.Evaluator, dsns.Remediator, dsns.All} {
		for _, sql := range []string{
			`UPDATE result_events SET to_status = NULL`, `DELETE FROM result_events`, `TRUNCATE result_events`,
		} {
			if err := exec(t, dsn, sql); err == nil {
				t.Errorf("%s: allowed, want result_events append-only", sql)
			}
		}
	}
}
