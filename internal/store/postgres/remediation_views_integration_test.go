//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
)

// remediationRow inserts the control's latest remediation in state, with
// the result's own epoch and intent revision and generation 1; extra
// overrides columns (SQL expressions).
func remediationRow(kind, state string, extra map[string]string) string {
	cols := map[string]string{
		"reserved_until": "now() + interval '1 hour'", "observed_head_sha": "'h1'", "acknowledged_head_sha": "'h1'",
		"closed_generation": "1", "next_eligible_at": "now() + interval '1 day'", "intent_revision": "intent_revision",
	}
	for k, v := range extra {
		cols[k] = v
	}

	return fmt.Sprintf(`INSERT INTO remediations (repository_id, control_id, assignment_epoch, operation_key, kind, state,
    remediated_generation, created_at, last_run_at, reserved_until, observed_head_sha, acknowledged_head_sha,
    closed_generation, next_eligible_at, intent_revision)
SELECT repository_id, control_id, assignment_epoch, 'op-'||gen_random_uuid(), '%s', '%s', 1, now(), now(), %s, %s, %s, %s, %s, %s
FROM control_results`, kind, state, cols["reserved_until"], cols["observed_head_sha"], cols["acknowledged_head_sha"],
		cols["closed_generation"], cols["next_eligible_at"], cols["intent_revision"])
}

// viewCase is one row of a view truth table: SQL applied to the base
// fixture inside a rolled-back owner transaction, then the view read.
type viewCase struct {
	name  string
	setup []string
	want  string
}

// runViewTable applies each case in its own rolled-back transaction as
// the owner and compares what read returns.
func runViewTable(t *testing.T, dsn string, cases []viewCase, read string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := conn.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()

			for _, q := range tc.setup {
				if _, err := tx.Exec(t.Context(), q); err != nil {
					t.Fatalf("%v\n%s", err, q)
				}
			}

			var got string
			if err := tx.QueryRow(t.Context(), read).Scan(&got); errors.Is(err, pgx.ErrNoRows) {
				got = ""
			} else if err != nil {
				t.Fatal(err)
			}

			if got != tc.want {
				t.Errorf("%s = %q, want %q", read, got, tc.want)
			}
		})
	}
}

// TestRemediationDue_TruthTable is IMPL-0030 1.10 (DESIGN-0032 Testing
// Strategy): remediation_due over policy state, assignment state and
// epoch, effective mode, status, remediable, generations (the first
// evaluation's 1 > 0 included), intent revision, latest state, observed
// versus acknowledged head, closed_generation and next_eligible_at. The
// base fixture is a managed repository whose codeowners result is
// non_compliant at generation 1 with a failing remediable rule and no
// remediation yet.
func TestRemediationDue_TruthTable(t *testing.T) {
	f := newRemediationFixture(t)
	epoch := epochOf(t, f.dsns.Owner, f.repoID)

	stage(t, f.eval, f.repoID, "k1", epoch, "f1", "non_compliant")

	if _, err := f.eval.RecordEvaluation(t.Context(), "k1"); err != nil {
		t.Fatal(err)
	}

	const caught = "UPDATE control_results SET remediated_generation = 1"

	cases := []viewCase{
		{name: "first evaluation 1 > 0", want: "codeowners"},
		{name: "repository excluded", setup: []string{`UPDATE repository_policy_state SET state = 'excluded'`}},
		{name: "repository parked", setup: []string{`UPDATE repository_policy_state SET state = 'parked'`}},
		{name: "assignment excluded", setup: []string{`UPDATE control_assignments SET state = 'excluded'`}},
		{name: "assignment epoch moved", setup: []string{`UPDATE control_assignments SET epoch = epoch + 1`}},
		{name: "effective mode evaluate", setup: []string{
			`UPDATE control_assignments SET effective_mode = 'evaluate', mode_reason = 'remediation_app_no_access'`,
		}},
		{name: "status compliant", setup: []string{`UPDATE control_results SET status = 'compliant'`}},
		{name: "status unknown", setup: []string{`UPDATE control_results SET status = 'unknown'`}},
		{name: "not remediable", setup: []string{`UPDATE rule_results SET remediate = false`}},
		{name: "rule passes", setup: []string{`UPDATE rule_results SET status = 'pass'`}},
		{name: "remediated, merged, same intent", setup: []string{caught, remediationRow("pull_request", "merged", nil)}},
		{name: "remediated, merged, intent moved", want: "codeowners", setup: []string{
			caught, remediationRow("pull_request", "merged", map[string]string{"intent_revision": "'old'"}),
		}},
		{name: "generation advanced past a merge", want: "codeowners", setup: []string{remediationRow("pull_request", "merged", nil)}},
		{name: "open, nothing moved", setup: []string{caught, remediationRow("pull_request", "open", nil)}},
		{name: "open, head pushed", want: "codeowners", setup: []string{
			caught, remediationRow("pull_request", "open", map[string]string{"observed_head_sha": "'h2'"}),
		}},
		{name: "open, generation advanced", want: "codeowners", setup: []string{remediationRow("pull_request", "open", nil)}},
		{name: "open, intent moved", want: "codeowners", setup: []string{
			caught, remediationRow("pull_request", "open", map[string]string{"intent_revision": "'old'"}),
		}},
		{name: "closed by user, waiting", setup: []string{caught, remediationRow("pull_request", "closed_by_user", nil)}},
		{name: "closed by user, generation past closed_generation", want: "codeowners", setup: []string{
			caught, remediationRow("pull_request", "closed_by_user", nil), `UPDATE control_results SET eval_generation = 2`,
		}},
		{name: "closed by user, next_eligible_at passed", want: "codeowners", setup: []string{
			caught, remediationRow("pull_request", "closed_by_user", map[string]string{"next_eligible_at": "now() - interval '1 minute'"}),
		}},
		{name: "failed resumes", want: "codeowners", setup: []string{caught, remediationRow("api", "failed", nil)}},
		{name: "reserved", setup: []string{remediationRow("pull_request", "reserved", nil)}},
		{name: "recommended", setup: []string{remediationRow("api", "recommended", nil)}},
		{name: "applied, generation advanced", want: "codeowners", setup: []string{remediationRow("api", "applied", nil)}},
	}

	runViewTable(t, f.dsns.Owner, cases, `SELECT control_id FROM remediation_due`)
}

// TestRemediationMaintenance_Actions pins remediation_maintenance's
// actions (IMPL-0030 1.3), including 00005's withdraw on an evaluate
// downgrade.
func TestRemediationMaintenance_Actions(t *testing.T) {
	f := newRemediationFixture(t)
	epoch := epochOf(t, f.dsns.Owner, f.repoID)

	stage(t, f.eval, f.repoID, "k1", epoch, "f1", "non_compliant")

	if _, err := f.eval.RecordEvaluation(t.Context(), "k1"); err != nil {
		t.Fatal(err)
	}

	open := remediationRow("pull_request", "open", nil)

	cases := []viewCase{
		{name: "open, nothing to do", setup: []string{open}},
		{name: "reservation expired", want: "expire", setup: []string{
			remediationRow("pull_request", "reserved", map[string]string{"reserved_until": "now() - interval '1 minute'"}),
		}},
		{name: "reservation live", setup: []string{remediationRow("pull_request", "reserved", nil)}},
		{name: "assignment withdrawn", want: "withdraw", setup: []string{open, `UPDATE control_assignments SET state = 'excluded'`}},
		{name: "repository unmanaged", want: "withdraw", setup: []string{open, `UPDATE repository_policy_state SET state = 'excluded'`}},
		{name: "downgraded to evaluate", want: "withdraw", setup: []string{
			open, `UPDATE control_assignments SET effective_mode = 'evaluate', mode_reason = 'remediation_app_suspended'`,
		}},
		{name: "recommendation superseded", want: "supersede", setup: []string{
			remediationRow("api", "recommended", map[string]string{"intent_revision": "'old'"}),
		}},
		{name: "now compliant", want: "close_compliant", setup: []string{open, `UPDATE control_results SET status = 'compliant'`}},
		{name: "merged is not maintained", setup: []string{remediationRow("pull_request", "merged", nil)}},
	}

	runViewTable(t, f.dsns.Owner, cases, `SELECT action FROM remediation_maintenance`)
}
