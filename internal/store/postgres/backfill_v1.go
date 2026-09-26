package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/pressly/goose/v3"
)

// MetaBackfilledV1At is the v2_meta key recording that the v1 backfill
// ran. A second run is a no-op.
const MetaBackfilledV1At = "backfilled_v1_at"

// MetaBootstrapPending is the v2_meta key the backfill sets so the
// worker starts BootstrapWorkflow (IMPL-0025 OQ15): migrate needs no
// Temporal credentials. The workflow deletes it when it completes.
const MetaBootstrapPending = "bootstrap_pending"

// BackfillReport is what the v1 backfill did, or would do under
// migrate --dry-run.
type BackfillReport struct {
	Ran bool `json:"ran"`

	// Seed orders the never-checked repositories across the freshness
	// window. It is logged so a spread can be reproduced.
	Seed string `json:"seed,omitempty"`

	Installations int `json:"installations"`
	Repositories  int `json:"repositories"`
	NeverChecked  int `json:"never_checked"`
	Findings      int `json:"findings"`
	Events        int `json:"events"`
	Snapshots     int `json:"snapshots"`

	// MultiOwner lists installations v1 saw under more than one owner,
	// as "id: kept (dropped, ...)". The most recently checked owner wins.
	MultiOwner []string `json:"multi_owner,omitempty"`

	// Collisions lists repo_state rows dropped because another row
	// differs only by case; the most recently checked one is kept.
	Collisions []string `json:"collisions,omitempty"`

	// ParkReasons is the park-reason histogram of inactive rows.
	ParkReasons map[string]int `json:"park_reasons,omitempty"`
}

// backfillV1Migration is 00003_backfill_v1 (DESIGN-0025 § v1 → v2
// migration). It is a no-op unless 00001 adopted a v1 database.
func backfillV1Migration() *goose.Migration {
	return goose.NewGoMigration(3,
		&goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
			rep, err := BackfillV1(ctx, tx, strconv.FormatInt(time.Now().UnixNano(), 10))
			if err != nil {
				return err
			}

			if rep.Ran {
				slog.Default().InfoContext(ctx, "v1 backfill complete",
					"seed", rep.Seed, "installations", rep.Installations, "repositories", rep.Repositories,
					"never_checked", rep.NeverChecked, "findings", rep.Findings, "snapshots", rep.Snapshots,
					"park_reasons", rep.ParkReasons, "multi_owner", rep.MultiOwner, "collisions", rep.Collisions)
			}

			return nil
		}},
		// Down is a no-op: 00002's down drops every table the backfill
		// wrote, and v1's tables were never touched.
		&goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM v2_meta WHERE key IN ($1, $2)`, MetaBackfilledV1At, MetaBootstrapPending)

			return err
		}},
	)
}

// BackfillV1 copies v1's repo_state, rule_state and compliance_snapshot
// into the v2 tables inside tx. It runs only on an adopted database and
// only once; otherwise it returns a report with Ran false. seed orders
// never-checked repositories across the freshness window carried by
// ctx (WithBackfillFreshness).
func BackfillV1(ctx context.Context, tx *sql.Tx, seed string) (*BackfillReport, error) {
	rep := &BackfillReport{}

	var adopted, done bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM v2_meta WHERE key = $1), EXISTS (SELECT 1 FROM v2_meta WHERE key = $2)`,
		MetaAdoptedV1At, MetaBackfilledV1At).Scan(&adopted, &done); err != nil {
		return nil, fmt.Errorf("backfill: reading v2_meta: %w", err)
	}

	if !adopted || done {
		return rep, nil
	}

	rep.Ran, rep.Seed = true, seed
	freshness := backfillFreshness(ctx).Seconds()

	steps := []struct {
		name string
		run  func() error
	}{
		{"installations", func() error { return backfillInstallations(ctx, tx, rep) }},
		{"repositories", func() error { return backfillRepositories(ctx, tx, rep, freshness, seed) }},
		{"findings", func() error { return backfillFindings(ctx, tx, rep) }},
		{"snapshots", func() error { return backfillSnapshots(ctx, tx, rep) }},
		{"marker", func() error {
			_, err := tx.ExecContext(ctx, `INSERT INTO v2_meta (key, value) VALUES ($1, $2), ($3, $2)`,
				MetaBackfilledV1At, seed, MetaBootstrapPending)

			return err
		}},
	}

	for _, s := range steps {
		if err := s.run(); err != nil {
			return nil, fmt.Errorf("backfill %s: %w", s.name, err)
		}
	}

	return rep, nil
}

// latestRepoState ranks repo_state rows most recently checked first.
const latestRepoState = `last_checked_at DESC NULLS LAST, installation_id, owner, repo`

func backfillInstallations(ctx context.Context, tx *sql.Tx, rep *BackfillReport) error {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO installations (installation_id, account_login)
		SELECT DISTINCT ON (installation_id) installation_id, owner
		FROM repo_state
		ORDER BY installation_id, `+latestRepoState)
	if err != nil {
		return err
	}

	if rep.Installations, err = affected(res); err != nil {
		return err
	}

	rep.MultiOwner, err = queryStrings(ctx, tx, `
		SELECT i.installation_id || ': ' || i.account_login || ' (' || string_agg(DISTINCT rs.owner, ', ') || ')'
		FROM installations i
		JOIN repo_state rs ON rs.installation_id = i.installation_id AND rs.owner <> i.account_login
		GROUP BY i.installation_id, i.account_login
		ORDER BY i.installation_id`)

	return err
}

func backfillRepositories(ctx context.Context, tx *sql.Tx, rep *BackfillReport, freshness float64, seed string) error {
	rep.Collisions = nil

	var err error
	if rep.Collisions, err = queryStrings(ctx, tx, `
		SELECT installation_id || '/' || owner || '/' || repo
		FROM (
			SELECT *, row_number() OVER (PARTITION BY lower(owner), lower(repo) ORDER BY `+latestRepoState+`) AS rank
			FROM repo_state
		) ranked
		WHERE rank > 1
		ORDER BY 1`); err != nil {
		return err
	}

	// Park reasons are v1's own reconstruction (postgres.go
	// unmeasurableQuery). Never-checked rows are spread evenly across
	// one freshness window in an order fixed by seed.
	res, err := tx.ExecContext(ctx, `
		WITH kept AS (
			SELECT DISTINCT ON (lower(owner), lower(repo)) *
			FROM repo_state
			ORDER BY lower(owner), lower(repo), `+latestRepoState+`
		), spread AS (
			SELECT installation_id, owner, repo,
			       row_number() OVER (ORDER BY md5($2 || '/' || installation_id || '/' || owner || '/' || repo)) AS rn,
			       count(*) OVER () AS n
			FROM kept
			WHERE last_checked_at IS NULL
		)
		INSERT INTO repositories (org, name, installation_id, active, park_reason, parked_at, next_due_at,
		                          last_checked_at, last_check_outcome, last_error, policy_version, catalog_parse_ok)
		SELECT k.owner, k.repo, k.installation_id, k.active,
		       CASE
		           WHEN k.active THEN NULL
		           WHEN k.last_check_status = 'error' THEN 'access_denied'
		           WHEN k.last_check_status = 'skipped' AND k.last_error IN ('archived', 'fork') THEN k.last_error
		           ELSE 'unknown'
		       END,
		       CASE WHEN k.active THEN NULL ELSE coalesce(k.last_checked_at, now()) END,
		       CASE
		           WHEN k.last_checked_at IS NOT NULL THEN k.last_checked_at + make_interval(secs => $1)
		           ELSE now() + make_interval(secs => $1 * (s.rn - 0.5) / s.n)
		       END,
		       k.last_checked_at, k.last_check_status, k.last_error, 'v1:' || k.policy_version, k.catalog_parse_ok
		FROM kept k
		LEFT JOIN spread s USING (installation_id, owner, repo)`, freshness, seed)
	if err != nil {
		return err
	}

	if rep.Repositories, err = affected(res); err != nil {
		return err
	}

	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM repositories WHERE last_checked_at IS NULL`).Scan(&rep.NeverChecked); err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, `SELECT park_reason, count(*) FROM repositories WHERE NOT active GROUP BY park_reason`)
	if err != nil {
		return err
	}
	defer rows.Close() //nolint:errcheck // read-only; rows.Err is checked

	rep.ParkReasons = map[string]int{}

	for rows.Next() {
		var (
			reason string
			n      int
		)

		if err := rows.Scan(&reason, &n); err != nil {
			return err
		}

		rep.ParkReasons[reason] = n
	}

	return rows.Err()
}

// backfillFindings migrates rule_state for the kept repositories and
// starts every finding's history with one created event.
func backfillFindings(ctx context.Context, tx *sql.Tx, rep *BackfillReport) error {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO findings (repository_id, rule_kind, rule_name, status, reason, evidence,
		                      status_since, last_evaluated_at, policy_version)
		SELECT r.id, rs.rule_kind, rs.rule_name,
		       CASE WHEN rs.actionable THEN 'non_compliant' ELSE 'compliant' END,
		       CASE WHEN rs.actionable THEN 'migrated_from_v1' END,
		       CASE WHEN rs.actionable THEN jsonb_build_object('v1_actionable_since', rs.actionable_since)
		            ELSE '{}'::jsonb END,
		       CASE WHEN rs.actionable THEN coalesce(rs.actionable_since, rs.updated_at) ELSE rs.updated_at END,
		       rs.updated_at, 'v1:' || rs.policy_version
		FROM rule_state rs
		JOIN repositories r ON r.installation_id = rs.installation_id AND r.org = rs.owner AND r.name = rs.repo`)
	if err != nil {
		return err
	}

	if rep.Findings, err = affected(res); err != nil {
		return err
	}

	res, err = tx.ExecContext(ctx, `
		INSERT INTO finding_events (repository_id, rule_kind, rule_name, to_status, to_reason,
		                            to_remediation, evidence, policy_version)
		SELECT repository_id, rule_kind, rule_name, status, 'migrated_from_v1', remediation, evidence, policy_version
		FROM findings`)
	if err != nil {
		return err
	}

	rep.Events, err = affected(res)

	return err
}

// backfillSnapshots copies compliance history. v1 snapshots have no
// kind, so it comes from rule_state by name, defaulting to file.
func backfillSnapshots(ctx context.Context, tx *sql.Tx, rep *BackfillReport) error {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO compliance_snapshots (org, rule_kind, rule_name, snapshot_at, compliant, non_compliant,
		                                  not_applicable, unknown)
		SELECT cs.org,
		       coalesce((SELECT min(rs.rule_kind) FROM rule_state rs WHERE rs.rule_name = cs.rule_name), 'file'),
		       cs.rule_name, cs.snapshot_at, cs.tracked_count - cs.actionable_count, cs.actionable_count, 0, 0
		FROM compliance_snapshot cs`)
	if err != nil {
		return err
	}

	rep.Snapshots, err = affected(res)

	return err
}

func affected(res sql.Result) (int, error) {
	n, err := res.RowsAffected()

	return int(n), err
}

func queryStrings(ctx context.Context, tx *sql.Tx, query string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck // read-only; rows.Err is checked

	var out []string

	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}

		out = append(out, s)
	}

	return out, rows.Err()
}
