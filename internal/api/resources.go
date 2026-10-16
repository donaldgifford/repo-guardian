package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// Page sizes (DESIGN-0027 § Pagination).
const (
	defaultLimit = 50
	maxLimit     = 200
)

// pageSize resolves ?limit=.
func pageSize(limit *int) (int, error) {
	if limit == nil {
		return defaultLimit, nil
	}

	if *limit < 1 || *limit > maxLimit {
		return 0, statusError(http.StatusBadRequest, "limit must be from 1 to 200")
	}

	return *limit, nil
}

// readErr answers an unknown or invisible resource 404.
func readErr(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return statusError(http.StatusNotFound, "")
	}

	return err
}

// cursorIn decodes ?cursor= into key under filters, answering 400.
func cursorIn(c *string, key, filters any) (bool, error) {
	ok, err := decodeCursor(c, key, filters)
	if errors.Is(err, errCursor) || errors.Is(err, errCursorFilters) {
		return false, statusError(http.StatusBadRequest, err.Error())
	}

	return ok, err
}

// nextCursor encodes the cursor after the last item of a page with
// more rows, or nil on the last page.
func nextCursor[T any](p store.Page[T], key func(*T) any, filters any) (*string, error) {
	if !p.More || len(p.Items) == 0 {
		return nil, nil //nolint:nilnil // no next page
	}

	c, err := encodeCursor(key(&p.Items[len(p.Items)-1]), filters)
	if err != nil {
		return nil, err
	}

	return &c, nil
}

// ListOrgs returns every visible org.
func (s *server) ListOrgs(ctx context.Context, _ gen.ListOrgsRequestObject) (gen.ListOrgsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	view, err := s.opts.Reader.Orgs(ctx, p.Visible)
	if err != nil {
		return nil, err
	}

	totals := orgTotals(view.Repositories, view.Current)
	out := gen.ListOrgs200JSONResponse{Items: make([]gen.OrgSummary, 0, len(totals))}

	for i := range totals {
		t := &totals[i]
		parked := 0

		for _, pc := range t.parked {
			parked += pc.Count
		}

		out.Items = append(out.Items, gen.OrgSummary{
			Org: t.org, Tracked: t.tracked, Parked: parked, Counts: counts(t.counts), CompliantPercent: store.CompliantPercent(t.counts),
			OpenPrs: view.OpenPRs[strings.ToLower(t.org)],
		})
	}

	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// repositoryFilters is what a repositories cursor is bound to.
type repositoryFilters struct {
	Org        string `json:"org,omitempty"`
	Active     *bool  `json:"active,omitempty"`
	ParkReason string `json:"park_reason,omitempty"`
	Q          string `json:"q,omitempty"`
}

type idKey struct {
	ID int64 `json:"id"`
}

// ListRepositories returns one page of repositories over the visible orgs.
func (s *server) ListRepositories(ctx context.Context, req gen.ListRepositoriesRequestObject) (gen.ListRepositoriesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	q := req.Params

	limit, err := pageSize(q.Limit)
	if err != nil {
		return nil, err
	}

	filters := repositoryFilters{Org: deref(q.Org), Active: q.Active, ParkReason: deref(q.ParkReason), Q: deref(q.Q)}
	f := store.RepositoryFilter{
		Org: filters.Org, Active: q.Active, ParkReason: store.ParkReason(filters.ParkReason), NamePrefix: filters.Q, Limit: limit,
	}

	var key idKey
	if ok, err := cursorIn(q.Cursor, &key, filters); err != nil {
		return nil, err
	} else if ok {
		f.AfterID = key.ID
	}

	page, err := s.opts.Reader.Repositories(ctx, p.Visible, &f)
	if err != nil {
		return nil, err
	}

	out := gen.ListRepositories200JSONResponse{Items: make([]gen.Repository, 0, len(page.Items))}
	for i := range page.Items {
		out.Items = append(out.Items, repositoryOut(&page.Items[i]))
	}

	out.NextCursor, err = nextCursor(page, func(it *store.Repository) any { return idKey{ID: it.ID} }, filters)

	return out, err
}

// GetRepository returns one visible repository.
func (s *server) GetRepository(ctx context.Context, req gen.GetRepositoryRequestObject) (gen.GetRepositoryResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	d, err := s.opts.Reader.Repository(ctx, p.Visible, req.Id)
	if err != nil {
		return nil, readErr(err)
	}

	out := gen.GetRepository200JSONResponse{Repository: repositoryOut(&d.Repository)}

	if d.LastCheck != nil {
		c := checkOut(d.LastCheck)
		out.LastCheck = &c
	}

	return out, nil
}

// repositoryScoped binds a per-repository cursor to its repository.
type repositoryScoped struct {
	RepositoryID int64 `json:"repository_id"`
}

// ListRepositoryChecks returns one page of a repository's checks.
func (s *server) ListRepositoryChecks(
	ctx context.Context, req gen.ListRepositoryChecksRequestObject,
) (gen.ListRepositoryChecksResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	limit, err := pageSize(req.Params.Limit)
	if err != nil {
		return nil, err
	}

	filters := repositoryScoped{RepositoryID: req.Id}

	var key idKey
	if _, err := cursorIn(req.Params.Cursor, &key, filters); err != nil {
		return nil, err
	}

	page, err := s.opts.Reader.RepositoryChecks(ctx, p.Visible, req.Id, key.ID, limit)
	if err != nil {
		return nil, readErr(err)
	}

	out := gen.ListRepositoryChecks200JSONResponse{Items: make([]gen.Check, 0, len(page.Items))}
	for i := range page.Items {
		out.Items = append(out.Items, checkOut(&page.Items[i]))
	}

	out.NextCursor, err = nextCursor(page, func(it *store.Check) any { return idKey{ID: it.ID} }, filters)

	return out, err
}

type eventKey struct {
	OccurredAt time.Time `json:"at"`
	Source     string    `json:"s"`
	ID         int64     `json:"id"`
}

// ListRepositoryEvents returns one page of a repository's timeline.
func (s *server) ListRepositoryEvents(
	ctx context.Context, req gen.ListRepositoryEventsRequestObject,
) (gen.ListRepositoryEventsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	limit, err := pageSize(req.Params.Limit)
	if err != nil {
		return nil, err
	}

	filters := repositoryScoped{RepositoryID: req.Id}

	var (
		key   eventKey
		after *store.EventKey
	)

	if ok, err := cursorIn(req.Params.Cursor, &key, filters); err != nil {
		return nil, err
	} else if ok {
		after = &store.EventKey{OccurredAt: key.OccurredAt, Source: key.Source, ID: key.ID}
	}

	page, err := s.opts.Reader.RepositoryEvents(ctx, p.Visible, req.Id, after, limit)
	if err != nil {
		return nil, readErr(err)
	}

	out := gen.ListRepositoryEvents200JSONResponse{Items: make([]gen.Event, 0, len(page.Items))}

	for i := range page.Items {
		e, err := eventOut(&page.Items[i])
		if err != nil {
			return nil, err
		}

		out.Items = append(out.Items, e)
	}

	out.NextCursor, err = nextCursor(page, func(it *store.Event) any {
		return eventKey{OccurredAt: it.OccurredAt, Source: it.Source, ID: it.ID}
	}, filters)

	return out, err
}
