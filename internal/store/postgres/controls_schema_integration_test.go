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

	if want := []int64{postgres.ControlsSchemaVersion}; !slices.Equal(applied, want) {
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
