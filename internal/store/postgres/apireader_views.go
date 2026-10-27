package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

// Orgs returns every visible org's repositories, compliance rows and
// open pull requests.
func (r *APIReader) Orgs(ctx context.Context, scope store.APIScope) (view *store.OrgsView, err error) {
	defer func(start time.Time) { observeQuery("api_orgs", start, err) }(time.Now())

	view = &store.OrgsView{}
	// The compliance query reads every org for an empty org list, so an
	// empty APIScope must stop here.
	if scope.Empty() {
		return view, nil
	}

	var orgs []string
	if !scope.All() {
		orgs = scope.Orgs()
	}

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		if view.Repositories, err = orgRepositories(ctx, q, scope); err != nil {
			return err
		}

		if view.OpenPRs, err = orgOpenPRs(ctx, q, scope); err != nil {
			return err
		}

		view.Compliance, err = controlCompliance(ctx, q, orgs, store.LineageAll)

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.Orgs: %w", err)
	}

	return view, nil
}

// orgOpenPRs counts open pull requests per org, keyed by the lower-cased
// org.
func orgOpenPRs(ctx context.Context, q *controlsdb.Queries, scope store.APIScope) (map[string]int, error) {
	rows, err := q.APIOrgOpenPRs(ctx, controlsdb.APIOrgOpenPRsParams{ScopeAll: scope.All(), ScopeOrgs: scope.Orgs()})
	if err != nil {
		return nil, fmt.Errorf("open prs: %w", err)
	}

	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[strings.ToLower(r.Org)] += int(r.PullRequests)
	}

	return out, nil
}

func orgRepositories(ctx context.Context, q *controlsdb.Queries, scope store.APIScope) ([]store.OrgRepositoryCount, error) {
	rows, err := q.APIOrgRepositories(ctx, controlsdb.APIOrgRepositoriesParams{ScopeAll: scope.All(), ScopeOrgs: scope.Orgs()})
	if err != nil {
		return nil, fmt.Errorf("repositories: %w", err)
	}

	out := make([]store.OrgRepositoryCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, store.OrgRepositoryCount{
			Org: row.Org, Active: row.Active, ParkReason: store.ParkReason(row.ParkReason), Count: int(row.Repositories),
		})
	}

	return out, nil
}

// pageLimit asks for one row past the page so More needs no count.
func pageLimit(limit int) int32 {
	return int32(limit + 1) //nolint:gosec // the API bounds limit to 200
}

// page trims the extra row pageLimit asked for.
func page[T any](items []T, limit int) store.Page[T] {
	if len(items) > limit {
		return store.Page[T]{Items: items[:limit], More: true}
	}

	return store.Page[T]{Items: items}
}

func optString[T ~string](v T) *string {
	if v == "" {
		return nil
	}

	s := string(v)

	return &s
}

// Repositories returns one page of repositories in scope.
func (r *APIReader) Repositories(
	ctx context.Context, scope store.APIScope, f *store.RepositoryFilter,
) (p store.Page[store.Repository], err error) {
	defer func(start time.Time) { observeQuery("api_repositories", start, err) }(time.Now())

	params := controlsdb.APIRepositoriesParams{
		ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
		Org: optString(f.Org), Active: f.Active, ParkReason: optString(f.ParkReason), PolicyState: optString(f.PolicyState),
		NamePrefix: optString(f.NamePrefix),
		HasAfter:   f.AfterID != 0, AfterID: f.AfterID, PageLimit: pageLimit(f.Limit),
	}

	var items []store.Repository

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		rows, err := q.APIRepositories(ctx, params)
		if err != nil {
			return err
		}

		items = make([]store.Repository, 0, len(rows))
		for i := range rows {
			items = append(items, apiRepository(&rows[i]))
		}

		return withPostures(ctx, q, scope, items)
	})
	if err != nil {
		return p, fmt.Errorf("postgres.APIReader.Repositories: %w", err)
	}

	return page(items, f.Limit), nil
}

func apiRepository(row *controlsdb.APIRepositoriesRow) store.Repository {
	return store.Repository{
		ID: row.ID, Org: row.Org, Name: row.Name, ProviderRepoID: row.ProviderRepoID,
		Active: row.Active, ParkReason: typedPtr[store.ParkReason](row.ParkReason), ParkedAt: row.ParkedAt,
		DiscoveredAt: row.DiscoveredAt, NextDueAt: row.NextDueAt, LastEvaluatedAt: unlessEpoch(row.LastEvaluatedAt),
		PolicyState: derefOr(row.PolicyState), PolicyReason: derefOr(row.PolicyReason),
		Evaluable: row.Evaluable, Remediable: row.Remediable,
	}
}

// withPostures fills each repository's Compliance in one grouped query
// (IMPL-0030 7.8); a repository with no assignment keeps the zero
// posture, whose percent and coverage are nil.
func withPostures(ctx context.Context, q *controlsdb.Queries, scope store.APIScope, items []store.Repository) error {
	if len(items) == 0 {
		return nil
	}

	ids := make([]int64, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].ID)
	}

	rows, err := q.APIRepositoryPostures(ctx, controlsdb.APIRepositoryPosturesParams{
		Ids: ids, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
	})
	if err != nil {
		return fmt.Errorf("repository postures: %w", err)
	}

	byID := make(map[int64]store.ControlPosture, len(rows))

	for i := range rows {
		row := &rows[i]
		p := store.ControlPosture{
			Compliant: int(row.Compliant), NonCompliant: int(row.NonCompliant), Pending: int(row.Pending), Stale: int(row.Stale),
			UnmeasurableAccessDenied: int(row.UnmeasurableAccessDenied), UnmeasurableSuspended: int(row.UnmeasurableSuspended),
			UnmeasurableConflict: int(row.UnmeasurableConflict), NotApplicable: int(row.NotApplicable), Unknown: int(row.Unknown),
			Excluded: int(row.Excluded), Assigned: int(row.Assigned),
		}

		if p.Percent, err = numericPtr(row.Percent); err != nil {
			return err
		}

		if p.Coverage, err = numericPtr(row.Coverage); err != nil {
			return err
		}

		byID[row.RepositoryID] = p
	}

	for i := range items {
		items[i].Compliance = byID[items[i].ID]
	}

	return nil
}

// unlessEpoch reads the queries' "never" (the Unix epoch) as nil.
func unlessEpoch(t time.Time) *time.Time {
	if t.Unix() == 0 {
		return nil
	}

	return &t
}

// visibleRepository reads one repository in scope, ErrNotFound when it
// is unknown or invisible.
func visibleRepository(ctx context.Context, q *controlsdb.Queries, scope store.APIScope, id int64) (*controlsdb.APIRepositoryRow, error) {
	row, err := q.APIRepository(ctx, controlsdb.APIRepositoryParams{ID: id, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs()})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("repository: %w", err)
	}

	return &row, nil
}

// Repository returns one repository in scope with its last evaluation.
// ErrNotFound when it is unknown or invisible.
func (r *APIReader) Repository(ctx context.Context, scope store.APIScope, id int64) (d *store.RepositoryDetail, err error) {
	defer func(start time.Time) { observeQuery("api_repository", start, err) }(time.Now())

	d = &store.RepositoryDetail{}

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		row, err := visibleRepository(ctx, q, scope, id)
		if err != nil {
			return err
		}

		d.Repository = apiRepository((*controlsdb.APIRepositoriesRow)(row))

		one := []store.Repository{d.Repository}
		if err := withPostures(ctx, q, scope, one); err != nil {
			return err
		}

		d.Repository = one[0]

		checks, err := q.APIRepositoryEvaluations(ctx, controlsdb.APIRepositoryEvaluationsParams{
			RepositoryID: id, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(), PageLimit: 1,
		})
		if err != nil {
			return fmt.Errorf("last evaluation: %w", err)
		}

		if len(checks) > 0 {
			c := apiCheck(&checks[0])
			d.LastCheck = &c
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.Repository: %w", err)
	}

	return d, nil
}

func apiCheck(row *controlsdb.APIRepositoryEvaluationsRow) store.Check {
	return store.Check{
		ID: row.ID, Trigger: row.Trigger, Outcome: row.Outcome, PolicyVersion: row.PolicyVersion,
		StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, Error: derefOr(row.Error),
	}
}

// RepositoryEvaluations returns one page of a repository's evaluation
// log, newest first. afterID zero starts at the newest. ErrNotFound when
// the repository is unknown or invisible.
func (r *APIReader) RepositoryEvaluations(
	ctx context.Context, scope store.APIScope, id, afterID int64, limit int,
) (p store.Page[store.Check], err error) {
	defer func(start time.Time) { observeQuery("api_repository_evaluations", start, err) }(time.Now())

	var items []store.Check

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		if _, err := visibleRepository(ctx, q, scope, id); err != nil {
			return err
		}

		rows, err := q.APIRepositoryEvaluations(ctx, controlsdb.APIRepositoryEvaluationsParams{
			RepositoryID: id, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
			HasAfter: afterID != 0, AfterID: afterID, PageLimit: pageLimit(limit),
		})
		if err != nil {
			return fmt.Errorf("evaluations: %w", err)
		}

		items = make([]store.Check, 0, len(rows))
		for i := range rows {
			items = append(items, apiCheck(&rows[i]))
		}

		return nil
	})
	if err != nil {
		return p, fmt.Errorf("postgres.APIReader.RepositoryEvaluations: %w", err)
	}

	return page(items, limit), nil
}

// RepositoryEvents returns one page of a repository's merged timeline,
// newest first. ErrNotFound when the repository is unknown or invisible.
func (r *APIReader) RepositoryEvents(
	ctx context.Context, scope store.APIScope, id int64, after *store.EventKey, limit int,
) (p store.Page[store.Event], err error) {
	defer func(start time.Time) { observeQuery("api_repository_events", start, err) }(time.Now())

	params := controlsdb.APIRepositoryEventsParams{
		RepositoryID: id, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(), PageLimit: pageLimit(limit),
	}

	if after != nil {
		params.HasAfter = true
		params.AfterOccurredAt, params.AfterSource, params.AfterID = after.OccurredAt, after.Source, after.ID
	}

	var items []store.Event

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		if _, err := visibleRepository(ctx, q, scope, id); err != nil {
			return err
		}

		rows, err := q.APIRepositoryEvents(ctx, params)
		if err != nil {
			return fmt.Errorf("events: %w", err)
		}

		items = make([]store.Event, 0, len(rows))
		for i := range rows {
			items = append(items, apiEvent(&rows[i]))
		}

		return nil
	})
	if err != nil {
		return p, fmt.Errorf("postgres.APIReader.RepositoryEvents: %w", err)
	}

	return page(items, limit), nil
}

func apiEvent(row *controlsdb.APIRepositoryEventsRow) store.Event {
	return store.Event{
		Source: row.Source, ID: row.ID, OccurredAt: row.OccurredAt,
		ControlID: row.ControlID, RuleID: row.RuleID, FromStatus: row.FromStatus, ToStatus: row.ToStatus,
		Kind: derefOr(row.Kind), Detail: row.Detail,
	}
}

// typedPtr converts a nullable text column to a nullable string type.
func typedPtr[T ~string](v *string) *T {
	if v == nil {
		return nil
	}

	t := T(*v)

	return &t
}

// derefOr returns *v, or "" for nil.
func derefOr(v *string) string {
	if v == nil {
		return ""
	}

	return *v
}
