package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

// ControlCompliance runs the controls compliance query for orgs (empty:
// every org) under lineage (store.Lineage*).
func (s *ControlsStore) ControlCompliance(ctx context.Context, orgs []string, lineage string) ([]store.ControlPosture, error) {
	rows, err := s.q.ControlCompliance(ctx, controlsdb.ControlComplianceParams{Orgs: lowerAll(orgs), Lineage: lineage})
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
