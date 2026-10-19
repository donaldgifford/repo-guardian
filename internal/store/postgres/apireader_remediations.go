package postgres

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

// branchHolds are the control_results holds set before a pull request
// exists; /remediations?hold= lists them as rows of their own (IMPL-0030
// OQ20).
var branchHolds = []string{store.HoldPRCap, store.HoldForeignBranch, store.HoldStaleBranch, store.HoldConflict}

// Remediations returns one page of remediations over the visible orgs,
// newest first, with each row's step journal.
func (r *APIReader) Remediations(
	ctx context.Context, scope store.APIScope, f *store.RemediationFilter,
) (p store.Page[store.RemediationView], err error) {
	defer func(start time.Time) { observeQuery("api_remediations", start, err) }(time.Now())

	if scope.Empty() {
		return p, nil
	}

	params := controlsdb.APIRemediationsParams{
		IncludeHeld: includeHeld(f), Holds: f.Holds, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
		Org: optString(f.Org), ControlID: optString(f.ControlID), Before: f.Before,
		Kind: optString(f.Kind), State: optString(f.State), PageLimit: pageLimit(f.Limit),
	}

	if a := f.After; a != nil {
		params.HasAfter = true
		params.AfterAt, params.AfterRepositoryID, params.AfterControlID, params.AfterID = a.At, a.RepositoryID, a.ControlID, a.ID
	}

	var items []store.RemediationView

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		rows, err := q.APIRemediations(ctx, params)
		if err != nil {
			return fmt.Errorf("remediations: %w", err)
		}

		items = make([]store.RemediationView, 0, len(rows))
		for i := range rows {
			items = append(items, remediationView(&rows[i]))
		}

		return withSteps(ctx, q, scope, items)
	})
	if err != nil {
		return p, fmt.Errorf("postgres.APIReader.Remediations: %w", err)
	}

	return page(items, f.Limit), nil
}

// includeHeld reports whether a filter lists the held results: only a
// branch hold asks for them, and only a pull-request, stateless filter
// can match a row with no remediation behind it.
func includeHeld(f *store.RemediationFilter) bool {
	if f.State != "" || (f.Kind != "" && f.Kind != store.RemediationPullRequest) {
		return false
	}

	return slices.ContainsFunc(f.Holds, func(h string) bool { return slices.Contains(branchHolds, h) })
}

func remediationView(row *controlsdb.APIRemediationsRow) store.RemediationView {
	v := store.RemediationView{
		ID: row.ID, RepositoryID: row.RepositoryID, Org: row.Org, Repository: row.Name, ControlID: row.ControlID,
		Kind: row.Kind, State: derefOr(row.State), IntentRevision: row.IntentRevision, Branch: derefOr(row.Branch),
		PRURL: derefOr(row.PrUrl), ObservedHeadSHA: derefOr(row.ObservedHeadSha),
		AcknowledgedHeadSHA: derefOr(row.AcknowledgedHeadSha), Hold: derefOr(row.Hold), HoldSince: row.HoldSince,
		Proposal: row.Proposal, CreatedAt: row.At, LastRunAt: row.LastRunAt, ClosedAt: row.ClosedAt,
		CloseReason: derefOr(row.CloseReason),
	}

	if row.PrNumber != nil {
		v.PRNumber = int(*row.PrNumber)
	}

	return v
}

// repositoryRemediations returns each control's latest remediation on
// one repository, with its steps, keyed by control.
func repositoryRemediations(
	ctx context.Context, q *controlsdb.Queries, scope store.APIScope, id int64,
) (map[string]*store.RemediationView, error) {
	rows, err := q.APIRepositoryRemediations(ctx, controlsdb.APIRepositoryRemediationsParams{
		RepositoryID: id, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
	})
	if err != nil {
		return nil, fmt.Errorf("remediations: %w", err)
	}

	items := make([]store.RemediationView, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		v := store.RemediationView{
			ID: row.ID, RepositoryID: row.RepositoryID, Org: row.Org, Repository: row.Name, ControlID: row.ControlID,
			Kind: row.Kind, State: row.State, IntentRevision: row.IntentRevision, Branch: derefOr(row.Branch),
			PRURL: derefOr(row.PrUrl), ObservedHeadSHA: derefOr(row.ObservedHeadSha),
			AcknowledgedHeadSHA: derefOr(row.AcknowledgedHeadSha), Hold: derefOr(row.Hold), Proposal: row.Proposal,
			CreatedAt: row.CreatedAt, LastRunAt: &row.LastRunAt, ClosedAt: row.ClosedAt, CloseReason: derefOr(row.CloseReason),
		}

		if row.PrNumber != nil {
			v.PRNumber = int(*row.PrNumber)
		}

		items = append(items, v)
	}

	if err := withSteps(ctx, q, scope, items); err != nil {
		return nil, err
	}

	out := make(map[string]*store.RemediationView, len(items))
	for i := range items {
		out[items[i].ControlID] = &items[i]
	}

	return out, nil
}

// withSteps attaches each remediation's step journal in one query; a
// held result (ID zero) has none.
func withSteps(ctx context.Context, q *controlsdb.Queries, scope store.APIScope, items []store.RemediationView) error {
	ids := make([]int64, 0, len(items))

	for i := range items {
		if items[i].ID != 0 {
			ids = append(ids, items[i].ID)
		}
	}

	if len(ids) == 0 {
		return nil
	}

	rows, err := q.APIRemediationSteps(ctx, controlsdb.APIRemediationStepsParams{
		Ids: ids, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
	})
	if err != nil {
		return fmt.Errorf("remediation steps: %w", err)
	}

	byID := make(map[int64][]store.RemediationStep, len(ids))

	for i := range rows {
		s := &rows[i]
		step := store.RemediationStep{
			RemediationID: s.RemediationID, Seq: int(s.Seq), Kind: s.Kind, ResourceKey: derefOr(s.ResourceKey),
			CommittedSHA: derefOr(s.CommittedSha), Status: s.Status, Before: s.Before, After: s.After,
			Error: derefOr(s.Error), OccurredAt: s.OccurredAt,
		}

		if s.PrNumber != nil {
			step.PRNumber = int(*s.PrNumber)
		}

		byID[s.RemediationID] = append(byID[s.RemediationID], step)
	}

	for i := range items {
		items[i].Steps = byID[items[i].ID]
	}

	return nil
}
