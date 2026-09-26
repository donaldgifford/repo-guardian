package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/findings"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/sqlcdb"
)

// oldestFailures is how many longest-failing findings a rule view shows.
const oldestFailures = 10

// complianceScope maps a non-empty APIScope onto the shared compliance
// query's Scope, where no orgs means every org. Callers must handle an
// empty APIScope first: passing it through would read the whole fleet.
func complianceScope(scope store.APIScope) store.Scope {
	if scope.All() {
		return store.Scope{}
	}

	return store.Scope{Orgs: scope.Orgs()}
}

// snapshots reads the latest snapshot per (org, rule) in scope.
func snapshots(ctx context.Context, q *sqlcdb.Queries, scope store.Scope) ([]store.ComplianceSnapshot, error) {
	rows, err := q.LatestComplianceSnapshots(ctx, scopeOrgs(scope))
	if err != nil {
		return nil, fmt.Errorf("snapshots: %w", err)
	}

	out := make([]store.ComplianceSnapshot, 0, len(rows))
	for _, sn := range rows {
		out = append(out, store.ComplianceSnapshot{
			ComplianceCount: store.ComplianceCount{
				Org: sn.Org, Kind: findings.RuleKind(sn.RuleKind), RuleName: sn.RuleName,
				Compliant: int(sn.Compliant), NonCompliant: int(sn.NonCompliant),
				NotApplicable: int(sn.NotApplicable), Unknown: int(sn.Unknown),
			},
			SnapshotAt: sn.SnapshotAt,
		})
	}

	return out, nil
}

// Rules returns the shared compliance rows and latest snapshots in scope.
func (r *APIReader) Rules(ctx context.Context, scope store.APIScope) (rep *store.ComplianceReport, err error) {
	defer func(start time.Time) { observeQuery("api_rules", start, err) }(time.Now())

	rep = &store.ComplianceReport{}
	if scope.Empty() {
		return rep, nil
	}

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		if rep.Current, err = compliance(ctx, q, complianceScope(scope)); err != nil {
			return err
		}

		rep.Previous, err = snapshots(ctx, q, complianceScope(scope))

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.Rules: %w", err)
	}

	return rep, nil
}

// Rule returns one rule across the orgs in scope. ErrNotFound when no
// visible repository has a finding for it.
func (r *APIReader) Rule(
	ctx context.Context, scope store.APIScope, kind findings.RuleKind, name string, staleBefore time.Time,
) (view *store.RuleView, err error) {
	defer func(start time.Time) { observeQuery("api_rule", start, err) }(time.Now())

	if scope.Empty() {
		return nil, store.ErrNotFound
	}

	view = &store.RuleView{}

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		rows, err := compliance(ctx, q, complianceScope(scope))
		if err != nil {
			return err
		}

		for i := range rows {
			if rows[i].Kind == kind && rows[i].RuleName == name {
				view.Current = append(view.Current, rows[i])
			}
		}

		if len(view.Current) == 0 {
			return store.ErrNotFound
		}

		prev, err := snapshots(ctx, q, complianceScope(scope))
		if err != nil {
			return err
		}

		for i := range prev {
			if prev[i].Kind == kind && prev[i].RuleName == name {
				view.Previous = append(view.Previous, prev[i])
			}
		}

		return ruleDrillDown(ctx, q, scope, view, staleBefore)
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.Rule: %w", err)
	}

	return view, nil
}

// ruleDrillDown adds the rule's reasons and oldest failures to view,
// whose Current rows name the rule.
func ruleDrillDown(ctx context.Context, q *sqlcdb.Queries, scope store.APIScope, view *store.RuleView, staleBefore time.Time) error {
	kind, name := string(view.Current[0].Kind), view.Current[0].RuleName

	reasons, err := q.APIRuleReasons(ctx, sqlcdb.APIRuleReasonsParams{
		RuleKind: kind, RuleName: name, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
	})
	if err != nil {
		return fmt.Errorf("reasons: %w", err)
	}

	for _, rr := range reasons {
		view.Reasons = append(view.Reasons, store.FindingReasonCount{Reason: findings.Reason(rr.Reason), Count: int(rr.Findings)})
	}

	oldest, err := q.APIOldestFailures(ctx, sqlcdb.APIOldestFailuresParams{
		StaleBefore: staleBefore, RuleKind: kind, RuleName: name,
		ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(), PageLimit: oldestFailures,
	})
	if err != nil {
		return fmt.Errorf("oldest failures: %w", err)
	}

	for i := range oldest {
		view.Oldest = append(view.Oldest, apiFinding((*sqlcdb.APIFindingsRow)(&oldest[i])))
	}

	return nil
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

		view.Current, err = compliance(ctx, q, complianceScope(scope))

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.Orgs: %w", err)
	}

	return view, nil
}

// Org returns one org in scope. ErrNotFound when the org is invisible
// or has no repositories; the two are indistinguishable by design.
func (r *APIReader) Org(ctx context.Context, scope store.APIScope, org string) (view *store.OrgView, err error) {
	defer func(start time.Time) { observeQuery("api_org", start, err) }(time.Now())

	if scope.Empty() {
		return nil, store.ErrNotFound
	}

	view = &store.OrgView{}

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		if view.Repositories, err = orgRepositories(ctx, q, scope, &org); err != nil {
			return err
		}

		if len(view.Repositories) == 0 {
			return store.ErrNotFound
		}

		// The scoped query above proved the org visible, so reading the
		// shared query for exactly that org cannot widen the scope.
		view.Org = view.Repositories[0].Org
		one := store.Scope{Orgs: []string{strings.ToLower(org)}}

		if view.Current, err = compliance(ctx, q, one); err != nil {
			return err
		}

		if view.Previous, err = snapshots(ctx, q, one); err != nil {
			return err
		}

		na, err := q.APIOrgNotApplicable(ctx, sqlcdb.APIOrgNotApplicableParams{Org: org, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs()})
		if err != nil {
			return fmt.Errorf("not applicable: %w", err)
		}

		for _, rr := range na {
			view.NotApplicable = append(view.NotApplicable, store.FindingReasonCount{Reason: findings.Reason(rr.Reason), Count: int(rr.Findings)})
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.Org: %w", err)
	}

	return view, nil
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

// Findings returns one page of findings in scope.
func (r *APIReader) Findings(ctx context.Context, scope store.APIScope, f *store.FindingFilter) (p store.Page[store.APIFinding], err error) {
	defer func(start time.Time) { observeQuery("api_findings", start, err) }(time.Now())

	params := sqlcdb.APIFindingsParams{
		StaleBefore: f.StaleBefore, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
		Status: optString(f.Status), Reason: optString(f.Reason), Remediation: optString(f.Remediation),
		Org: optString(f.Org), RuleKind: optString(f.Kind), RuleName: optString(f.RuleName),
		SinceBefore: f.SinceBefore, PrStale: f.PRStale, PageLimit: pageLimit(f.Limit),
	}

	if f.After != nil {
		params.HasAfter = true
		params.AfterRepositoryID, params.AfterRuleKind, params.AfterRuleName = f.After.RepositoryID, string(f.After.Kind), f.After.RuleName
	}

	var items []store.APIFinding

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		rows, err := q.APIFindings(ctx, params)
		if err != nil {
			return err
		}

		items = make([]store.APIFinding, 0, len(rows))
		for i := range rows {
			items = append(items, apiFinding(&rows[i]))
		}

		return nil
	})
	if err != nil {
		return p, fmt.Errorf("postgres.APIReader.Findings: %w", err)
	}

	return page(items, f.Limit), nil
}

func apiFinding(row *sqlcdb.APIFindingsRow) store.APIFinding {
	return store.APIFinding{
		RepositoryID: row.RepositoryID, Org: row.Org, Repository: row.Name,
		Kind: findings.RuleKind(row.RuleKind), RuleName: row.RuleName,
		Status: findings.Status(row.Status), Reason: findings.Reason(derefOr(row.Reason)),
		Remediation: findings.Remediation(row.Remediation),
		StatusSince: row.StatusSince, LastEvaluatedAt: row.LastEvaluatedAt,
		PRStale: row.PrStale, Evidence: row.Evidence,
	}
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

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}

	n := int(*v)

	return &n
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

// Repository returns one repository in scope with its installation,
// last check and findings. ErrNotFound when it is unknown or invisible.
func (r *APIReader) Repository(
	ctx context.Context, scope store.APIScope, id int64, staleBefore time.Time,
) (d *store.RepositoryDetail, err error) {
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
		d.Installation = store.InstallationStatus{
			InstallationID: row.InstallationID, Account: row.AccountLogin, SuspendedAt: row.SuspendedAt, RemovedAt: row.RemovedAt,
			RateLimit: intPtr(row.RateLimit), RateRemaining: intPtr(row.RateRemaining),
			RateResetAt: row.RateResetAt, RateObservedAt: row.RateObservedAt,
		}

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

		rows, err := q.APIRepositoryFindings(ctx, sqlcdb.APIRepositoryFindingsParams{
			StaleBefore: staleBefore, RepositoryID: id, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
		})
		if err != nil {
			return fmt.Errorf("findings: %w", err)
		}

		d.Findings = make([]store.APIFinding, 0, len(rows))
		for i := range rows {
			d.Findings = append(d.Findings, apiFinding((*sqlcdb.APIFindingsRow)(&rows[i])))
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

// ComplianceHistory returns one page of stored snapshots in scope.
func (r *APIReader) ComplianceHistory(
	ctx context.Context, scope store.APIScope, f *store.HistoryFilter,
) (p store.Page[store.ComplianceSnapshot], err error) {
	defer func(start time.Time) { observeQuery("api_compliance_history", start, err) }(time.Now())

	params := sqlcdb.APIComplianceHistoryParams{
		ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
		Org: optString(f.Org), RuleKind: optString(f.Kind), RuleName: optString(f.RuleName),
		FromAt: f.From, ToAt: f.To, PageLimit: pageLimit(f.Limit),
	}

	if f.After != nil {
		params.HasAfter = true
		params.AfterSnapshotAt, params.AfterOrg = f.After.SnapshotAt, f.After.Org
		params.AfterRuleKind, params.AfterRuleName = string(f.After.Kind), f.After.RuleName
	}

	var items []store.ComplianceSnapshot

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		rows, err := q.APIComplianceHistory(ctx, params)
		if err != nil {
			return err
		}

		items = make([]store.ComplianceSnapshot, 0, len(rows))
		for _, sn := range rows {
			items = append(items, store.ComplianceSnapshot{
				ComplianceCount: store.ComplianceCount{
					Org: sn.Org, Kind: findings.RuleKind(sn.RuleKind), RuleName: sn.RuleName,
					Compliant: int(sn.Compliant), NonCompliant: int(sn.NonCompliant),
					NotApplicable: int(sn.NotApplicable), Unknown: int(sn.Unknown),
				},
				SnapshotAt: sn.SnapshotAt,
			})
		}

		return nil
	})
	if err != nil {
		return p, fmt.Errorf("postgres.APIReader.ComplianceHistory: %w", err)
	}

	return page(items, f.Limit), nil
}

// Installations returns one page of installations in scope.
func (r *APIReader) Installations(
	ctx context.Context, scope store.APIScope, afterID int64, limit int,
) (p store.Page[store.InstallationStatus], err error) {
	defer func(start time.Time) { observeQuery("api_installations", start, err) }(time.Now())

	var items []store.InstallationStatus

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		rows, err := q.APIInstallations(ctx, sqlcdb.APIInstallationsParams{
			ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(), HasAfter: afterID != 0, AfterID: afterID, PageLimit: pageLimit(limit),
		})
		if err != nil {
			return err
		}

		items = make([]store.InstallationStatus, 0, len(rows))
		for _, row := range rows {
			items = append(items, store.InstallationStatus{
				InstallationID: row.InstallationID, Account: row.Org, SuspendedAt: row.SuspendedAt, RemovedAt: row.RemovedAt,
				RateLimit: intPtr(row.RateLimit), RateRemaining: intPtr(row.RateRemaining),
				RateResetAt: row.RateResetAt, RateObservedAt: row.RateObservedAt,
			})
		}

		return nil
	})
	if err != nil {
		return p, fmt.Errorf("postgres.APIReader.Installations: %w", err)
	}

	return page(items, limit), nil
}

// Policy returns the newest policy version. ErrNotFound before the
// first version is recorded.
func (r *APIReader) Policy(ctx context.Context) (pol *store.CurrentPolicy, err error) {
	defer func(start time.Time) { observeQuery("api_policy", start, err) }(time.Now())

	err = r.readOnly(ctx, func(q *sqlcdb.Queries) error {
		row, err := q.CurrentPolicyVersion(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}

		if err != nil {
			return err
		}

		pol = &store.CurrentPolicy{Version: row.Version, FirstSeenAt: row.FirstSeenAt, RolloutCompletedAt: row.RolloutCompletedAt}
		if err := json.Unmarshal(row.Summary, &pol.Summary); err != nil {
			return fmt.Errorf("policy summary: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.Policy: %w", err)
	}

	return pol, nil
}
