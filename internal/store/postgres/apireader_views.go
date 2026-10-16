package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/sqlcdb"
)

// complianceScope maps a non-empty APIScope onto the shared compliance
// query's Scope, where no orgs means every org. Callers must handle an
// empty APIScope first: passing it through would read the whole fleet.
func complianceScope(scope store.APIScope) store.Scope {
	if scope.All() {
		return store.Scope{}
	}

	return store.Scope{Orgs: scope.Orgs()}
}

// Orgs returns every visible org's repositories and compliance rows.
func (r *APIReader) Orgs(ctx context.Context, scope store.APIScope) (view *store.OrgsView, err error) {
	defer func(start time.Time) { observeQuery("api_orgs", start, err) }(time.Now())

	view = &store.OrgsView{}
	if scope.Empty() {
		return view, nil
	}

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		if view.Repositories, err = orgRepositories(ctx, q, scope, nil); err != nil {
			return err
		}

		if view.OpenPRs, err = orgOpenPRs(ctx, q, scope); err != nil {
			return err
		}

		view.Current, err = compliance(ctx, q, complianceScope(scope))

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.Orgs: %w", err)
	}

	return view, nil
}

// orgOpenPRs counts findings with an open PR per org, keyed by the
// lower-cased org.
func orgOpenPRs(ctx context.Context, q *sqlcdb.Queries, scope store.APIScope) (map[string]int, error) {
	rows, err := q.APIOrgOpenPRs(ctx, sqlcdb.APIOrgOpenPRsParams{ScopeAll: scope.All(), ScopeOrgs: scope.Orgs()})
	if err != nil {
		return nil, fmt.Errorf("open prs: %w", err)
	}

	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[strings.ToLower(r.Org)] += int(r.Findings)
	}

	return out, nil
}

func orgRepositories(ctx context.Context, q *sqlcdb.Queries, scope store.APIScope, org *string) ([]store.OrgRepositoryCount, error) {
	rows, err := q.APIOrgRepositories(ctx, sqlcdb.APIOrgRepositoriesParams{ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(), Org: org})
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

	params := sqlcdb.APIRepositoriesParams{
		ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
		Org: optString(f.Org), Active: f.Active, ParkReason: optString(f.ParkReason), NamePrefix: optString(f.NamePrefix),
		HasAfter: f.AfterID != 0, AfterID: f.AfterID, PageLimit: pageLimit(f.Limit),
	}

	var items []store.Repository

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		rows, err := q.APIRepositories(ctx, params)
		if err != nil {
			return err
		}

		items = make([]store.Repository, 0, len(rows))
		for i := range rows {
			items = append(items, apiRepository(&rows[i]))
		}

		return nil
	})
	if err != nil {
		return p, fmt.Errorf("postgres.APIReader.Repositories: %w", err)
	}

	return page(items, f.Limit), nil
}

func apiRepository(row *sqlcdb.APIRepositoriesRow) store.Repository {
	return store.Repository{
		ID: row.ID, Org: row.Org, Name: row.Name, ProviderRepoID: row.ProviderRepoID, InstallationID: row.InstallationID,
		Active: row.Active, ParkReason: typedPtr[store.ParkReason](row.ParkReason), ParkedAt: row.ParkedAt,
		DiscoveredAt: row.DiscoveredAt, NextDueAt: row.NextDueAt, LastCheckedAt: row.LastCheckedAt,
		LastCheckOutcome: row.LastCheckOutcome, PolicyVersion: row.PolicyVersion,
	}
}

// visibleRepository reads one repository in scope, ErrNotFound when it
// is unknown or invisible.
func visibleRepository(ctx context.Context, q *sqlcdb.Queries, scope store.APIScope, id int64) (*sqlcdb.APIRepositoryRow, error) {
	row, err := q.APIRepository(ctx, sqlcdb.APIRepositoryParams{ID: id, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs()})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("repository: %w", err)
	}

	return &row, nil
}

// Repository returns one repository in scope with its last check.
// ErrNotFound when it is unknown or invisible.
func (r *APIReader) Repository(ctx context.Context, scope store.APIScope, id int64) (d *store.RepositoryDetail, err error) {
	defer func(start time.Time) { observeQuery("api_repository", start, err) }(time.Now())

	d = &store.RepositoryDetail{}

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		row, err := visibleRepository(ctx, q, scope, id)
		if err != nil {
			return err
		}

		d.Repository = apiRepository(&sqlcdb.APIRepositoriesRow{
			ID: row.ID, Org: row.Org, Name: row.Name, ProviderRepoID: row.ProviderRepoID, InstallationID: row.InstallationID,
			Active: row.Active, ParkReason: row.ParkReason, ParkedAt: row.ParkedAt, DiscoveredAt: row.DiscoveredAt,
			NextDueAt: row.NextDueAt, LastCheckedAt: row.LastCheckedAt, LastCheckOutcome: row.LastCheckOutcome,
			PolicyVersion: row.PolicyVersion,
		})
		checks, err := q.APIRepositoryChecks(ctx, sqlcdb.APIRepositoryChecksParams{
			RepositoryID: id, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(), PageLimit: 1,
		})
		if err != nil {
			return fmt.Errorf("last check: %w", err)
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

func apiCheck(row *sqlcdb.APIRepositoryChecksRow) store.Check {
	return store.Check{
		ID: row.ID, Trigger: store.Trigger(row.Trigger), Outcome: row.Outcome, PolicyVersion: row.PolicyVersion,
		StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, Error: derefOr(row.Error),
	}
}

// RepositoryChecks returns one page of a repository's checks, newest
// first. afterID zero starts at the newest. ErrNotFound when the
// repository is unknown or invisible.
func (r *APIReader) RepositoryChecks(
	ctx context.Context, scope store.APIScope, id, afterID int64, limit int,
) (p store.Page[store.Check], err error) {
	defer func(start time.Time) { observeQuery("api_repository_checks", start, err) }(time.Now())

	var items []store.Check

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		if _, err := visibleRepository(ctx, q, scope, id); err != nil {
			return err
		}

		rows, err := q.APIRepositoryChecks(ctx, sqlcdb.APIRepositoryChecksParams{
			RepositoryID: id, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
			HasAfter: afterID != 0, AfterID: afterID, PageLimit: pageLimit(limit),
		})
		if err != nil {
			return fmt.Errorf("checks: %w", err)
		}

		items = make([]store.Check, 0, len(rows))
		for i := range rows {
			items = append(items, apiCheck(&rows[i]))
		}

		return nil
	})
	if err != nil {
		return p, fmt.Errorf("postgres.APIReader.RepositoryChecks: %w", err)
	}

	return page(items, limit), nil
}

// RepositoryEvents returns one page of a repository's merged timeline,
// newest first. ErrNotFound when the repository is unknown or invisible.
func (r *APIReader) RepositoryEvents(
	ctx context.Context, scope store.APIScope, id int64, after *store.EventKey, limit int,
) (p store.Page[store.Event], err error) {
	defer func(start time.Time) { observeQuery("api_repository_events", start, err) }(time.Now())

	params := sqlcdb.APIRepositoryEventsParams{
		RepositoryID: id, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(), PageLimit: pageLimit(limit),
	}

	if after != nil {
		params.HasAfter = true
		params.AfterOccurredAt, params.AfterSource, params.AfterID = after.OccurredAt, after.Source, after.ID
	}

	var items []store.Event

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
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

func apiEvent(row *sqlcdb.APIRepositoryEventsRow) store.Event {
	return store.Event{
		Source: row.Source, ID: row.ID, OccurredAt: row.OccurredAt,
		Kind: findings.RuleKind(row.RuleKind), RuleName: row.RuleName,
		FromStatus: typedPtr[findings.Status](row.FromStatus), ToStatus: typedPtr[findings.Status](row.ToStatus),
		FromReason: typedPtr[findings.Reason](row.FromReason), ToReason: typedPtr[findings.Reason](row.ToReason),
		FromRemediation: typedPtr[findings.Remediation](row.FromRemediation),
		ToRemediation:   typedPtr[findings.Remediation](row.ToRemediation),
		RepositoryKind:  derefOr(row.Kind), Detail: row.Detail,
	}
}
