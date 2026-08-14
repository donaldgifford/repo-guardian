package postgres

import (
	"context"
	"fmt"
	"time"
)

// snapshotQuery appends one row per (org, rule) to compliance_snapshot.
//
// The denominator is deliberately PER-RULE, not per-org. rule_state
// holds a row for every rule actually evaluated against a repository —
// satisfied ones included — so count(*) is "repos this rule applies
// to" while the FILTER is "repos it applies to and fails on". That is
// the honest ratio for a scoped rule, and it closes the gap recorded
// as the task 2.2 follow-up: the org-wide denominator on the posture
// gauges reports a rule scoped to 10 of 100 repos with 5 failures as
// 5% rather than 50%. The gauges cannot fix this without a new series;
// the snapshot table already has a per-rule row to hold it.
//
// The repo_state join applies the same active filter as the posture
// query, for the same reason: a parked repository is one nobody can
// measure, so it belongs in neither half of a compliance ratio.
//
// ON CONFLICT DO NOTHING makes a retry after a partial failure safe.
const snapshotQuery = `
INSERT INTO compliance_snapshot (org, rule_name, actionable_count, tracked_count, snapshot_at)
SELECT rs.owner,
       rs.rule_name,
       count(*) FILTER (WHERE rs.actionable),
       count(*),
       $1
FROM rule_state rs
JOIN repo_state r
  ON r.installation_id = rs.installation_id
 AND r.owner = rs.owner
 AND r.repo = rs.repo
WHERE r.active
GROUP BY rs.owner, rs.rule_name
ON CONFLICT (org, rule_name, snapshot_at) DO NOTHING`

// InsertComplianceSnapshot implements store.Store.
func (s *Store) InsertComplianceSnapshot(ctx context.Context, at time.Time) (int, error) {
	start := time.Now()

	rows, err := s.insertComplianceSnapshotInner(ctx, at)
	observeQuery("insert_compliance_snapshot", start, err)

	return rows, err
}

func (s *Store) insertComplianceSnapshotInner(ctx context.Context, at time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, snapshotQuery, at)
	if err != nil {
		return 0, fmt.Errorf("postgres.InsertComplianceSnapshot: %w", err)
	}

	return int(tag.RowsAffected()), nil
}
