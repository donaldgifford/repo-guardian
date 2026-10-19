package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

// ControlCompliance runs the controls compliance query for orgs (empty:
// every org) under lineage (store.Lineage*).
func (s *ControlsStore) ControlCompliance(ctx context.Context, orgs []string, lineage string) ([]store.ControlPosture, error) {
	return controlCompliance(ctx, s.q, orgs, lineage)
}

// controlCompliance runs the compliance query on q, so the API reader's
// read-only transactions share the store's conversion.
func controlCompliance(ctx context.Context, q *controlsdb.Queries, orgs []string, lineage string) ([]store.ControlPosture, error) {
	rows, err := q.ControlCompliance(ctx, controlsdb.ControlComplianceParams{Orgs: lowerAll(orgs), Lineage: lineage})
	if err != nil {
		return nil, fmt.Errorf("control compliance: %w", err)
	}

	out := make([]store.ControlPosture, 0, len(rows))

	for i := range rows {
		r := &rows[i]

		p := store.ControlPosture{
			Org: r.Org, ControlID: r.ControlID, Decision: r.Decision,
			Compliant: int(r.Compliant), NonCompliant: int(r.NonCompliant), Pending: int(r.Pending), Stale: int(r.Stale),
			UnmeasurableAccessDenied: int(r.UnmeasurableAccessDenied), UnmeasurableSuspended: int(r.UnmeasurableSuspended),
			UnmeasurableConflict: int(r.UnmeasurableConflict), NotApplicable: int(r.NotApplicable), Unknown: int(r.Unknown),
			Excluded: int(r.Excluded), Assigned: int(r.Assigned),
		}

		if p.Percent, err = numericPtr(r.Percent); err != nil {
			return nil, err
		}

		if p.Coverage, err = numericPtr(r.Coverage); err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	return out, nil
}

// ControlExclusions is the exclusions report for orgs (empty: every org).
func (s *ControlsStore) ControlExclusions(ctx context.Context, orgs []string) ([]store.Exclusion, error) {
	rows, err := s.q.ControlExclusions(ctx, lowerAll(orgs))
	if err != nil {
		return nil, fmt.Errorf("control exclusions: %w", err)
	}

	out := make([]store.Exclusion, 0, len(rows))
	for _, r := range rows {
		out = append(out, store.Exclusion{
			Org: r.Org, Repository: r.Name, ControlID: r.ControlID, State: r.State, Reason: r.Reason, ExcludedBy: r.ExcludedBy,
		})
	}

	return out, nil
}

// FloorPercent evaluates the SQL percentage function, so the Go mirror
// store.CompliantPercent can be held to it.
func (s *ControlsStore) FloorPercent(ctx context.Context, num, den int64) (*float64, error) {
	n, err := s.q.FloorPercent(ctx, controlsdb.FloorPercentParams{Num: num, Den: den})
	if err != nil {
		return nil, fmt.Errorf("floor percent: %w", err)
	}

	return numericPtr(n)
}

// lowerAll lower-cases org names for the queries' case-insensitive scope;
// it returns an empty, non-nil slice so an unscoped call binds '{}'.
func lowerAll(orgs []string) []string {
	out := make([]string, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, strings.ToLower(o))
	}

	return out
}

// InsertComplianceSnapshot writes one compliance_snapshots row per
// ControlCompliance row (every lineage, every org) stamped at, reading
// and writing in one transaction, and returns how many it wrote.
func (s *ControlsStore) InsertComplianceSnapshot(ctx context.Context, at time.Time) (int, error) {
	var n int

	err := s.inTx(ctx, func(q *controlsdb.Queries) error {
		rows, err := q.ControlCompliance(ctx, controlsdb.ControlComplianceParams{Orgs: []string{}, Lineage: store.LineageAll})
		if err != nil {
			return err
		}

		for i := range rows {
			r := &rows[i]
			if err := q.InsertComplianceSnapshot(ctx, controlsdb.InsertComplianceSnapshotParams{
				Org: r.Org, ControlID: r.ControlID, Decision: r.Decision, SnapshotAt: at,
				Compliant: r.Compliant, NonCompliant: r.NonCompliant, Pending: r.Pending, Stale: r.Stale,
				UnmeasurableAccessDenied: r.UnmeasurableAccessDenied, UnmeasurableSuspended: r.UnmeasurableSuspended,
				UnmeasurableConflict: r.UnmeasurableConflict, NotApplicable: r.NotApplicable, Unknown: r.Unknown,
				Excluded: r.Excluded, Assigned: r.Assigned, Percent: r.Percent, Coverage: r.Coverage,
			}); err != nil {
				return err
			}
		}

		n = len(rows)

		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("compliance snapshot: %w", err)
	}

	return n, nil
}

// numericPtr converts a nullable NUMERIC percentage: NULL is nil.
func numericPtr(n pgtype.Numeric) (*float64, error) {
	if !n.Valid {
		return nil, nil //nolint:nilnil // NULL percent: unmeasured
	}

	f, err := n.Float64Value()
	if err != nil {
		return nil, fmt.Errorf("compliance percent: %w", err)
	}

	return &f.Float64, nil
}

// PruneChecks deletes the finished checks older than before and returns
// how many it deleted (INV-0023 F7.2). A repository's last evaluation is
// repositories.last_checked_at, which the prune leaves alone.
func (s *ControlsStore) PruneChecks(ctx context.Context, before time.Time) (int64, error) {
	n, err := s.q.PruneChecks(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("prune checks: %w", err)
	}

	return n, nil
}
