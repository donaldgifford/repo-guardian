package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
	"github.com/donaldgifford/repo-guardian/internal/store/postgres/controlsdb"
)

// complianceOrgs maps a non-empty APIScope to the compliance query's org
// list, where empty means every org. Callers must stop on scope.Empty()
// first, or an empty scope would read the fleet.
func complianceOrgs(scope store.APIScope) []string {
	if scope.All() {
		return nil
	}

	return scope.Orgs()
}

// Summary returns the fleet totals over the visible orgs.
func (r *APIReader) Summary(ctx context.Context, scope store.APIScope, now time.Time) (view *store.SummaryView, err error) {
	defer func(start time.Time) { observeQuery("api_summary", start, err) }(time.Now())

	view = &store.SummaryView{}
	if scope.Empty() {
		return view, nil
	}

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		if view.Repositories, err = orgRepositories(ctx, q, scope); err != nil {
			return err
		}

		prs, err := orgOpenPRs(ctx, q, scope)
		if err != nil {
			return err
		}

		for _, n := range prs {
			view.OpenPRs += n
		}

		if view.Compliance, err = controlCompliance(ctx, q, complianceOrgs(scope), store.LineageAll); err != nil {
			return err
		}

		since := now.Add(-24 * time.Hour)

		ev, err := q.APISummaryEvaluations(ctx, controlsdb.APISummaryEvaluationsParams{
			Since: &since, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
		})
		if err != nil {
			return fmt.Errorf("evaluations: %w", err)
		}

		view.Evaluated, view.LastEvaluatedAt = int(ev.Evaluated), unlessEpoch(ev.LastEvaluatedAt)

		backlog, err := q.APIRemediationBacklog(ctx, controlsdb.APIRemediationBacklogParams{
			ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(),
		})
		if err != nil {
			return fmt.Errorf("remediation backlog: %w", err)
		}

		view.RemediationBacklog = int(backlog)

		ages, err := q.APIFindingAge(ctx, controlsdb.APIFindingAgeParams{ScopeAll: scope.All(), ScopeOrgs: scope.Orgs()})
		if err != nil {
			return fmt.Errorf("finding age: %w", err)
		}

		byBucket := make(map[string]int, len(ages))
		for _, a := range ages {
			byBucket[a.Bucket] = int(a.Findings)
		}

		view.FindingAge = store.AgeCounts(store.FindingAgeBuckets, byBucket)

		prAges, err := q.APIOpenPRAge(ctx, controlsdb.APIOpenPRAgeParams{ScopeAll: scope.All(), ScopeOrgs: scope.Orgs()})
		if err != nil {
			return fmt.Errorf("open pull request age: %w", err)
		}

		byBucket = make(map[string]int, len(prAges))
		for _, a := range prAges {
			byBucket[a.Bucket] = int(a.PullRequests)
		}

		view.OpenPRsByAge = store.AgeCounts(store.OpenPRAgeBuckets, byBucket)

		active, err := q.CurrentActivation(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("policy version: %w", err)
		}

		view.PolicyVersion = active.Version

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.Summary: %w", err)
	}

	return view, nil
}

// Controls returns the compliance rows and assigned versions over the
// visible orgs, for controlID alone when it is set, with the active
// policy's catalogue.
func (r *APIReader) Controls(ctx context.Context, scope store.APIScope, controlID string) (view *store.ControlsView, err error) {
	defer func(start time.Time) { observeQuery("api_controls", start, err) }(time.Now())

	view = &store.ControlsView{}
	if scope.Empty() {
		return view, nil
	}

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		rows, err := controlCompliance(ctx, q, complianceOrgs(scope), store.LineageAll)
		if err != nil {
			return err
		}

		for i := range rows {
			if controlID == "" || rows[i].ControlID == controlID {
				view.Compliance = append(view.Compliance, rows[i])
			}
		}

		versions, err := q.APIControlVersions(ctx, controlsdb.APIControlVersionsParams{
			ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(), ControlID: optString(controlID),
		})
		if err != nil {
			return fmt.Errorf("versions: %w", err)
		}

		for _, v := range versions {
			view.Versions = append(view.Versions, store.ControlVersion{
				Org: v.Org, ControlID: v.ControlID, Decision: v.Decision,
				Version: int(v.ControlVersion), Revision: v.Revision, Repositories: int(v.Repositories),
			})
		}

		p, err := activePolicy(ctx, q)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}

		if err != nil {
			return err
		}

		view.Catalogue = p.Summary.Controls

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.Controls: %w", err)
	}

	return view, nil
}

// Org returns one visible org's repositories, compliance and exclusions.
// ErrNotFound when the org is invisible or has no repositories. The
// scoped repository count proves visibility before the shared compliance
// and exclusion queries read exactly that org.
func (r *APIReader) Org(ctx context.Context, scope store.APIScope, org string) (view *store.OrgView, err error) {
	defer func(start time.Time) { observeQuery("api_org", start, err) }(time.Now())

	if scope.Empty() {
		return nil, store.ErrNotFound
	}

	view = &store.OrgView{}

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		repos, err := orgRepositories(ctx, q, scope)
		if err != nil {
			return err
		}

		for _, c := range repos {
			if strings.EqualFold(c.Org, org) {
				view.Repositories = append(view.Repositories, c)
			}
		}

		if len(view.Repositories) == 0 {
			return store.ErrNotFound
		}

		only := []string{strings.ToLower(org)}

		if view.Compliance, err = controlCompliance(ctx, q, only, store.LineageAll); err != nil {
			return err
		}

		ex, err := q.ControlExclusions(ctx, only)
		if err != nil {
			return fmt.Errorf("exclusions: %w", err)
		}

		for _, e := range ex {
			view.Exclusions = append(view.Exclusions, store.Exclusion{
				Org: e.Org, Repository: e.Name, ControlID: e.ControlID, State: e.State, Reason: e.Reason, ExcludedBy: e.ExcludedBy,
			})
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.Org: %w", err)
	}

	return view, nil
}

// RepositoryControls returns a repository's assignments with their
// results, rule results and latest remediation, in remediation priority
// order. ErrNotFound
// when the repository is unknown or invisible.
func (r *APIReader) RepositoryControls(ctx context.Context, scope store.APIScope, id int64) (out []store.RepositoryControl, err error) {
	defer func(start time.Time) { observeQuery("api_repository_controls", start, err) }(time.Now())

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		if _, err := visibleRepository(ctx, q, scope, id); err != nil {
			return err
		}

		params := controlsdb.APIRepositoryControlsParams{RepositoryID: id, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs()}

		rows, err := q.APIRepositoryControls(ctx, params)
		if err != nil {
			return fmt.Errorf("controls: %w", err)
		}

		rules, err := q.APIRepositoryRuleResults(ctx, controlsdb.APIRepositoryRuleResultsParams(params))
		if err != nil {
			return fmt.Errorf("rule results: %w", err)
		}

		byControl := make(map[string][]store.RuleResult)
		for _, rr := range rules {
			byControl[rr.ControlID] = append(byControl[rr.ControlID], store.RuleResult{
				RuleID: rr.RuleID, Status: rr.Status, Remediate: rr.Remediate, Evidence: rr.Evidence,
			})
		}

		remediations, err := repositoryRemediations(ctx, q, scope, id)
		if err != nil {
			return err
		}

		out = make([]store.RepositoryControl, 0, len(rows))
		for i := range rows {
			c := repositoryControl(&rows[i])
			c.Rules = byControl[c.ControlID]
			c.Remediation = remediations[c.ControlID]
			out = append(out, c)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.RepositoryControls: %w", err)
	}

	return out, nil
}

func repositoryControl(row *controlsdb.APIRepositoryControlsRow) store.RepositoryControl {
	return store.RepositoryControl{
		ControlID: row.ControlID, Version: int(row.ControlVersion), Revision: row.Revision, State: row.State,
		Decision: row.Decision, BaselineControl: derefOr(row.BaselineControl), Source: row.Source,
		SourceBlock: derefOr(row.SourceBlock), Replaced: derefOr(row.Replaced), ExcludedBy: derefOr(row.ExcludedBy),
		Reason: derefOr(row.Reason), Error: derefOr(row.Error), RequestedMode: row.RequestedMode,
		EffectiveMode: row.EffectiveMode, ModeSource: row.ModeSource, ModeReason: derefOr(row.ModeReason),
		Epoch: row.Epoch, Bucket: row.Bucket, Status: derefOr(row.Status), EvaluatedSHA: derefOr(row.EvaluatedSha),
		EvaluatedAt: row.EvaluatedAt, LastChangedAt: row.LastChangedAt, Hold: derefOr(row.Hold), HoldSince: row.HoldSince,
	}
}

// ComplianceHistory returns each UTC day's last snapshot per org,
// control and lineage over the visible orgs, oldest first.
func (r *APIReader) ComplianceHistory(
	ctx context.Context, scope store.APIScope, f *store.HistoryFilter,
) (out []store.ComplianceSnapshot, err error) {
	defer func(start time.Time) { observeQuery("api_compliance_history", start, err) }(time.Now())

	if f.Aggregate != "" && f.Aggregate != store.HistoryNone {
		return r.complianceHistoryAggregate(ctx, scope, f)
	}

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		rows, err := q.APIComplianceHistory(ctx, controlsdb.APIComplianceHistoryParams{
			ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(), Since: f.Since,
			ControlID: optString(f.ControlID), Org: optString(f.Org),
		})
		if err != nil {
			return err
		}

		out = make([]store.ComplianceSnapshot, 0, len(rows))

		for i := range rows {
			s, err := complianceSnapshot(&rows[i])
			if err != nil {
				return err
			}

			out = append(out, s)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.ComplianceHistory: %w", err)
	}

	return out, nil
}

// complianceHistoryAggregate is ComplianceHistory summed per UTC day
// in SQL, over the fleet or per org or control.
func (r *APIReader) complianceHistoryAggregate(
	ctx context.Context, scope store.APIScope, f *store.HistoryFilter,
) (out []store.ComplianceSnapshot, err error) {
	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		rows, err := q.APIComplianceHistoryAggregate(ctx, controlsdb.APIComplianceHistoryAggregateParams{
			By: f.Aggregate, ScopeAll: scope.All(), ScopeOrgs: scope.Orgs(), Since: f.Since,
			ControlID: optString(f.ControlID), Org: optString(f.Org),
		})
		if err != nil {
			return err
		}

		out = make([]store.ComplianceSnapshot, 0, len(rows))

		for i := range rows {
			a := &rows[i]
			s := store.ComplianceSnapshot{
				SnapshotAt: a.Day,
				ControlPosture: store.ControlPosture{
					Org: a.Org, ControlID: a.ControlID,
					Compliant: int(a.Compliant), NonCompliant: int(a.NonCompliant), Pending: int(a.Pending), Stale: int(a.Stale),
					UnmeasurableAccessDenied: int(a.UnmeasurableAccessDenied), UnmeasurableSuspended: int(a.UnmeasurableSuspended),
					UnmeasurableConflict: int(a.UnmeasurableConflict), NotApplicable: int(a.NotApplicable), Unknown: int(a.Unknown),
					Excluded: int(a.Excluded), Assigned: int(a.Assigned),
				},
			}

			if s.Percent, err = numericPtr(a.Percent); err != nil {
				return err
			}

			if s.Coverage, err = numericPtr(a.Coverage); err != nil {
				return err
			}

			out = append(out, s)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.ComplianceHistory: %w", err)
	}

	return out, nil
}

func complianceSnapshot(r *controlsdb.ComplianceSnapshot) (store.ComplianceSnapshot, error) {
	s := store.ComplianceSnapshot{
		SnapshotAt: r.SnapshotAt,
		ControlPosture: store.ControlPosture{
			Org: r.Org, ControlID: r.ControlID, Decision: r.Decision,
			Compliant: int(r.Compliant), NonCompliant: int(r.NonCompliant), Pending: int(r.Pending), Stale: int(r.Stale),
			UnmeasurableAccessDenied: int(r.UnmeasurableAccessDenied), UnmeasurableSuspended: int(r.UnmeasurableSuspended),
			UnmeasurableConflict: int(r.UnmeasurableConflict), NotApplicable: int(r.NotApplicable), Unknown: int(r.Unknown),
			Excluded: int(r.Excluded), Assigned: int(r.Assigned),
		},
	}

	var err error

	if s.Percent, err = numericPtr(r.Percent); err != nil {
		return s, err
	}

	s.Coverage, err = numericPtr(r.Coverage)

	return s, err
}

// ActivePolicy returns the active policy version and its stored
// summary, unscoped: the api filters it to the caller's orgs.
// ErrNotFound before a version is activated.
func (r *APIReader) ActivePolicy(ctx context.Context) (p *store.ActivePolicy, err error) {
	defer func(start time.Time) { observeQuery("api_policy", start, err) }(time.Now())

	err = r.readOnly(ctx, func(q *controlsdb.Queries) error {
		p, err = activePolicy(ctx, q)

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres.APIReader.ActivePolicy: %w", err)
	}

	return p, nil
}

func activePolicy(ctx context.Context, q *controlsdb.Queries) (*store.ActivePolicy, error) {
	row, err := q.ActivePolicy(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, store.ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("active policy: %w", err)
	}

	p := &store.ActivePolicy{
		Version: row.Version, FirstSeenAt: row.FirstSeenAt, ActivatedAt: row.ActivatedAt, RolloutCompletedAt: row.RolloutCompletedAt,
	}

	if len(row.Summary) > 0 {
		var sum policy.Summary
		if err := json.Unmarshal(row.Summary, &sum); err != nil {
			return nil, fmt.Errorf("policy summary: %w", err)
		}

		p.Summary = sum
	}

	return p, nil
}
