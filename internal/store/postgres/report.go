package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/store"
)

// findingsQuery lists every actionable (repo, rule) pair on an active
// repository.
//
// Ordered in SQL rather than in Go so the renderer's output is stable
// without sorting: a report regenerated from unchanged data must be
// byte-identical, or the golden-file tests fail on row order and every
// diff of a committed report is noise.
const findingsQuery = `
SELECT rs.installation_id, rs.owner, rs.repo, rs.rule_name, rs.rule_kind, rs.actionable_since
FROM rule_state rs
JOIN repo_state r
  ON r.installation_id = rs.installation_id
 AND r.owner = rs.owner
 AND r.repo = rs.repo
WHERE rs.actionable AND r.active
ORDER BY rs.owner, rs.rule_name, rs.repo`

// currentQuery is the live tally, computed identically to
// snapshotQuery.
//
// The report compares today against the last stored snapshot, and the
// two must be produced the same way or the "trend" is really a diff
// between two definitions. Keep this and snapshotQuery in step; the
// per-rule denominator in particular is the point (see snapshotQuery).
const currentQuery = `
SELECT rs.owner, rs.rule_name, count(*) FILTER (WHERE rs.actionable), count(*)
FROM rule_state rs
JOIN repo_state r
  ON r.installation_id = rs.installation_id
 AND r.owner = rs.owner
 AND r.repo = rs.repo
WHERE r.active
GROUP BY rs.owner, rs.rule_name
ORDER BY rs.owner, rs.rule_name`

// previousQuery returns the most recent snapshot row per (org, rule).
//
// DISTINCT ON rather than a max(snapshot_at) join, because a rule can
// be MISSING from the newest run while still having history. A run only
// writes a row for a rule some active repository was evaluated against,
// so a rule that was temporarily disabled, or whose every repository
// happened to be parked that day, has a gap. Keyed on a global max,
// every such rule loses its trend entirely; keyed per rule, it is
// compared against the last time it was actually measured.
//
// The cost is that different rules may be compared against different
// dates, so SnapshotAt travels with each row and the renderer prints
// it. A trend against an unstated date would be worse than no trend.
const previousQuery = `
SELECT DISTINCT ON (org, rule_name) org, rule_name, actionable_count, tracked_count, snapshot_at
FROM compliance_snapshot
ORDER BY org, rule_name, snapshot_at DESC`

// ReportData implements store.Store.
func (s *Store) ReportData(ctx context.Context) (*store.ReportData, error) {
	start := time.Now()

	data, err := s.reportDataInner(ctx)
	observeQuery("report_data", start, err)

	return data, err
}

func (s *Store) reportDataInner(ctx context.Context) (*store.ReportData, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("postgres.ReportData: begin: %w", err)
	}

	defer func() {
		_ = tx.Rollback(ctx) //nolint:errcheck // read-only tx; nothing to lose on cleanup
	}()

	var data store.ReportData

	if data.Findings, err = scanFindings(ctx, tx); err != nil {
		return nil, err
	}

	if data.Current, err = scanSnapshotRows(ctx, tx, currentQuery); err != nil {
		return nil, err
	}

	if data.Previous, err = scanSnapshotRows(ctx, tx, previousQuery); err != nil {
		return nil, err
	}

	return &data, nil
}

func scanFindings(ctx context.Context, tx pgx.Tx) ([]store.ReportFinding, error) {
	rows, err := tx.Query(ctx, findingsQuery)
	if err != nil {
		return nil, fmt.Errorf("postgres.ReportData: findings: %w", err)
	}

	defer rows.Close()

	var out []store.ReportFinding

	for rows.Next() {
		var f store.ReportFinding

		if err := rows.Scan(&f.InstallationID, &f.Owner, &f.Repo, &f.RuleName, &f.RuleKind, &f.ActionableSince); err != nil {
			return nil, fmt.Errorf("postgres.ReportData: scan finding: %w", err)
		}

		out = append(out, f)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.ReportData: iterate findings: %w", err)
	}

	return out, nil
}

// scanSnapshotRows reads (org, rule, actionable, tracked[, at]) rows.
//
// currentQuery has no snapshot_at of its own — it describes now — so
// the column is optional and Current rows carry a zero time. The
// renderer never prints it for Current; only Previous is dated.
func scanSnapshotRows(ctx context.Context, tx pgx.Tx, query string) ([]store.SnapshotRow, error) {
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres.ReportData: snapshot rows: %w", err)
	}

	defer rows.Close()

	var out []store.SnapshotRow

	for rows.Next() {
		var r store.SnapshotRow

		dest := []any{&r.Org, &r.RuleName, &r.ActionableCount, &r.TrackedCount}
		if len(rows.FieldDescriptions()) == 5 {
			dest = append(dest, &r.SnapshotAt)
		}

		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("postgres.ReportData: scan snapshot row: %w", err)
		}

		out = append(out, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres.ReportData: iterate snapshot rows: %w", err)
	}

	return out, nil
}
