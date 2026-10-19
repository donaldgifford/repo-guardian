package api

import (
	"context"
	"net/http"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// resultFilters is what a results cursor is bound to; the sort is part
// of it, since a cursor from one direction cannot page the other.
type resultFilters struct {
	Bucket   string `json:"bucket,omitempty"`
	Control  string `json:"control,omitempty"`
	Rule     string `json:"rule,omitempty"`
	Org      string `json:"org,omitempty"`
	Decision string `json:"decision,omitempty"`
	Sort     string `json:"sort,omitempty"`
}

type resultKey struct {
	At           time.Time `json:"at"`
	RepositoryID int64     `json:"r"`
	ControlID    string    `json:"c"`
}

// ListResults returns one page of recorded control results over the
// visible orgs (IMPL-0030 7.7), with the total on the first page.
func (s *server) ListResults(ctx context.Context, req gen.ListResultsRequestObject) (gen.ListResultsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	q := req.Params

	limit, err := pageSize(q.Limit)
	if err != nil {
		return nil, err
	}

	filters := resultFilters{
		Bucket: deref(q.Bucket), Control: deref(q.Control), Rule: deref(q.Rule), Org: deref(q.Org), Decision: deref(q.Decision),
		Sort: string(gen.SinceAsc),
	}

	if q.Sort != nil {
		filters.Sort = string(*q.Sort)
	}

	if filters.Sort != string(gen.SinceAsc) && filters.Sort != string(gen.SinceDesc) {
		return nil, statusError(http.StatusBadRequest, "sort must be since_asc or since_desc")
	}

	f := store.ResultFilter{
		Bucket: filters.Bucket, ControlID: filters.Control, Org: filters.Org, Decision: filters.Decision, RuleID: filters.Rule,
		Descending: filters.Sort == string(gen.SinceDesc), Limit: limit,
	}

	var key resultKey
	if ok, err := cursorIn(q.Cursor, &key, filters); err != nil {
		return nil, err
	} else if ok {
		f.After = &store.ResultKey{At: key.At, RepositoryID: key.RepositoryID, ControlID: key.ControlID}
	}

	page, err := s.opts.Reader.Results(ctx, p.Visible, &f)
	if err != nil {
		return nil, err
	}

	out := gen.ListResults200JSONResponse{Items: make([]gen.ResultRow, 0, len(page.Items)), Total: page.Total}

	for i := range page.Items {
		r := &page.Items[i]
		out.Items = append(out.Items, gen.ResultRow{
			RepositoryId: r.RepositoryID, Org: r.Org, Repository: r.Repository, ControlId: r.ControlID,
			ControlVersion: r.ControlVersion, Decision: r.Decision, Status: r.Status, Bucket: r.Bucket,
			FailingRules: nonNil(r.FailingRules), Reason: optStr(r.Reason), LastChangedAt: r.LastChangedAt,
		})
	}

	out.NextCursor, err = nextCursor(page.Page, func(it *store.ResultRow) any {
		return resultKey{At: it.LastChangedAt, RepositoryID: it.RepositoryID, ControlID: it.ControlID}
	}, filters)

	return out, err
}
