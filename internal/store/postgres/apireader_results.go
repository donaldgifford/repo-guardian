package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

// Results returns one page of recorded control results over the visible
// orgs (IMPL-0030 7.7), with the filter's total on the first page,
// counted in the same read-only transaction (OQ16).
func (r *APIReader) Results(ctx context.Context, scope store.APIScope, f *store.ResultFilter) (p store.ResultPage, err error) {
	defer func(start time.Time) { observeQuery("api_results", start, err) }(time.Now())

	if scope.Empty() {
		if f.After == nil {
			zero := 0
			p.Total = &zero
		}

		return p, nil
	}

	params := controlsdb.APIResultsParams{
		ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(), Org: optString(f.Org), ControlID: optString(f.ControlID),
		Decision: optString(f.Decision), RuleID: optString(f.RuleID), Bucket: optString(f.Bucket),
		Descending: f.Descending, PageLimit: pageLimit(f.Limit),
	}

	if a := f.After; a != nil {
		params.HasAfter = true
		params.AfterAt, params.AfterRepositoryID, params.AfterControlID = a.At, a.RepositoryID, a.ControlID
	}

	var items []store.ResultRow

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		rows, err := q.APIResults(ctx, params)
		if err != nil {
			return fmt.Errorf("results: %w", err)
		}

		items = make([]store.ResultRow, 0, len(rows))
		for i := range rows {
			row := &rows[i]
			items = append(items, store.ResultRow{
				RepositoryID: row.RepositoryID, Org: row.Org, Repository: row.Name, ControlID: row.ControlID,
				ControlVersion: int(row.ControlVersion), Decision: row.Decision, Status: row.Status, Bucket: row.Bucket,
				FailingRules: row.FailingRules, Reason: row.Reason, LastChangedAt: row.LastChangedAt,
			})
		}

		if f.After != nil {
			return nil
		}

		total, err := q.APIResultsTotal(ctx, controlsdb.APIResultsTotalParams{
			ScopeAll: params.ScopeAll, ScopeOrgs: params.ScopeOrgs, Org: params.Org, ControlID: params.ControlID,
			Decision: params.Decision, RuleID: params.RuleID, Bucket: params.Bucket,
		})
		if err != nil {
			return fmt.Errorf("results total: %w", err)
		}

		n := int(total)
		p.Total = &n

		return nil
	})
	if err != nil {
		return store.ResultPage{}, fmt.Errorf("postgres.APIReader.Results: %w", err)
	}

	p.Page = page(items, f.Limit)

	return p, nil
}
