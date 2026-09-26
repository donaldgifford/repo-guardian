package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/findings"
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

// staleBefore is the pr_stale cut-off: a PR created before it is stale.
func (s *server) staleBefore(staleAfter time.Duration) time.Time {
	return s.opts.Now().Add(-staleAfter)
}

// ListRules returns compliance for every rule over the visible orgs.
func (s *server) ListRules(ctx context.Context, _ gen.ListRulesRequestObject) (gen.ListRulesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	rep, err := s.opts.Reader.Rules(ctx, p.Visible)
	if err != nil {
		return nil, err
	}

	return gen.ListRules200JSONResponse{Items: ruleCompliancesOut(rep.Current, rep.Previous)}, nil
}

// GetRule returns one rule across the visible orgs.
func (s *server) GetRule(ctx context.Context, req gen.GetRuleRequestObject) (gen.GetRuleResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	kind := findings.RuleKind(req.Kind)

	view, err := s.opts.Reader.Rule(ctx, p.Visible, kind, req.Name, s.staleBefore(s.opts.StaleAfter))
	if err != nil {
		return nil, readErr(err)
	}

	totals := ruleTotals(view.Current, nil)
	out := gen.GetRule200JSONResponse{
		Kind: req.Kind, Name: req.Name, Counts: counts(totals[0].counts), CompliantPercent: store.CompliantPercent(totals[0].counts),
		Orgs: make([]gen.OrgCompliance, 0, len(view.Current)), TopReasons: reasonsOut(view.Reasons),
	}

	for i := range view.Current {
		c := view.Current[i].Counts()
		out.Orgs = append(out.Orgs, gen.OrgCompliance{Org: view.Current[i].Org, Counts: counts(c), CompliantPercent: view.Current[i].Percent})
	}

	if out.OldestFailures, err = findingsOut(view.Oldest); err != nil {
		return nil, err
	}

	out.Description, err = s.ruleDescription(ctx, kind, req.Name)
	if err != nil {
		return nil, err
	}

	return out, nil
}

// ruleDescription looks the rule up in the current policy summary.
func (s *server) ruleDescription(ctx context.Context, kind findings.RuleKind, name string) (*string, error) {
	pol, err := s.opts.Reader.Policy(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil //nolint:nilnil // no policy recorded yet
	}

	if err != nil {
		return nil, err
	}

	for i := range pol.Summary.Rules {
		if r := &pol.Summary.Rules[i]; r.Kind == kind && r.Name == name {
			return optStr(r.Description), nil
		}
	}

	return nil, nil //nolint:nilnil // rule not in the current policy
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
		})
	}

	return out, nil
}

// GetOrg returns one visible org.
func (s *server) GetOrg(ctx context.Context, req gen.GetOrgRequestObject) (gen.GetOrgResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	view, err := s.opts.Reader.Org(ctx, p.Visible, req.Org)
	if err != nil {
		return nil, readErr(err)
	}

	t := orgTotals(view.Repositories, nil)[0]

	return gen.GetOrg200JSONResponse{
		Org: view.Org, Tracked: t.tracked, Parked: parkedOut(t.parked),
		Rules: ruleCompliancesOut(view.Current, view.Previous), NotApplicable: reasonsOut(view.NotApplicable),
	}, nil
}

// findingFilters is what a findings cursor is bound to.
type findingFilters struct {
	Status      string     `json:"status,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	Remediation string     `json:"remediation,omitempty"`
	Org         string     `json:"org,omitempty"`
	Kind        string     `json:"kind,omitempty"`
	Rule        string     `json:"rule,omitempty"`
	PRStale     *bool      `json:"pr_stale,omitempty"`
	SinceBefore *time.Time `json:"since_before,omitempty"`
	StaleAfter  string     `json:"stale_after"`
}

type findingKey struct {
	RepositoryID int64  `json:"r"`
	Kind         string `json:"k"`
	RuleName     string `json:"n"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// ListFindings returns one page of findings over the visible orgs.
//
//nolint:gocritic // hugeParam: the signature is fixed by gen.StrictServerInterface
func (s *server) ListFindings(ctx context.Context, req gen.ListFindingsRequestObject) (gen.ListFindingsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	q := req.Params

	staleAfter, err := s.staleAfter(q.StaleAfter)
	if err != nil {
		return nil, err
	}

	limit, err := pageSize(q.Limit)
	if err != nil {
		return nil, err
	}

	filters := findingFilters{
		Status: deref(q.Status), Reason: deref(q.Reason), Remediation: deref(q.Remediation), Org: deref(q.Org),
		Kind: deref(q.Kind), Rule: deref(q.Rule), PRStale: q.PrStale, SinceBefore: q.SinceBefore, StaleAfter: staleAfter.String(),
	}

	f := store.FindingFilter{
		Status: findings.Status(filters.Status), Reason: findings.Reason(filters.Reason),
		Remediation: findings.Remediation(filters.Remediation), Org: filters.Org, Kind: findings.RuleKind(filters.Kind),
		RuleName: filters.Rule, PRStale: q.PrStale, SinceBefore: q.SinceBefore, StaleBefore: s.staleBefore(staleAfter), Limit: limit,
	}

	var key findingKey
	if ok, err := cursorIn(q.Cursor, &key, filters); err != nil {
		return nil, err
	} else if ok {
		f.After = &store.FindingKey{RepositoryID: key.RepositoryID, Kind: findings.RuleKind(key.Kind), RuleName: key.RuleName}
	}

	page, err := s.opts.Reader.Findings(ctx, p.Visible, &f)
	if err != nil {
		return nil, err
	}

	out := gen.ListFindings200JSONResponse{}
	if out.Items, err = findingsOut(page.Items); err != nil {
		return nil, err
	}

	out.NextCursor, err = nextCursor(page, func(it *store.APIFinding) any {
		return findingKey{RepositoryID: it.RepositoryID, Kind: string(it.Kind), RuleName: it.RuleName}
	}, filters)

	return out, err
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

	staleAfter, err := s.staleAfter(req.Params.StaleAfter)
	if err != nil {
		return nil, err
	}

	d, err := s.opts.Reader.Repository(ctx, p.Visible, req.Id, s.staleBefore(staleAfter))
	if err != nil {
		return nil, readErr(err)
	}

	out := gen.GetRepository200JSONResponse{
		Repository: repositoryOut(&d.Repository), Installation: installationOut(&d.Installation, s.opts.Now()),
	}

	if d.LastCheck != nil {
		c := checkOut(d.LastCheck)
		out.LastCheck = &c
	}

	out.Findings, err = findingsOut(d.Findings)

	return out, err
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

// historyFilters is what a history cursor is bound to.
type historyFilters struct {
	Org  string     `json:"org,omitempty"`
	Kind string     `json:"kind,omitempty"`
	Rule string     `json:"rule,omitempty"`
	From *time.Time `json:"from,omitempty"`
	To   *time.Time `json:"to,omitempty"`
}

type historyKey struct {
	SnapshotAt time.Time `json:"at"`
	Org        string    `json:"o"`
	Kind       string    `json:"k"`
	RuleName   string    `json:"n"`
}

// ListComplianceHistory returns one page of stored snapshots.
func (s *server) ListComplianceHistory(
	ctx context.Context, req gen.ListComplianceHistoryRequestObject,
) (gen.ListComplianceHistoryResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	q := req.Params

	limit, err := pageSize(q.Limit)
	if err != nil {
		return nil, err
	}

	filters := historyFilters{Org: deref(q.Org), Kind: deref(q.Kind), Rule: deref(q.Rule), From: q.From, To: q.To}
	f := store.HistoryFilter{
		Org: filters.Org, Kind: findings.RuleKind(filters.Kind), RuleName: filters.Rule, From: q.From, To: q.To, Limit: limit,
	}

	var key historyKey
	if ok, err := cursorIn(q.Cursor, &key, filters); err != nil {
		return nil, err
	} else if ok {
		f.After = &store.HistoryKey{SnapshotAt: key.SnapshotAt, Org: key.Org, Kind: findings.RuleKind(key.Kind), RuleName: key.RuleName}
	}

	page, err := s.opts.Reader.ComplianceHistory(ctx, p.Visible, &f)
	if err != nil {
		return nil, err
	}

	out := gen.ListComplianceHistory200JSONResponse{Items: make([]gen.HistoryPoint, 0, len(page.Items))}
	for i := range page.Items {
		out.Items = append(out.Items, historyOut(&page.Items[i]))
	}

	out.NextCursor, err = nextCursor(page, func(it *store.ComplianceSnapshot) any {
		return historyKey{SnapshotAt: it.SnapshotAt, Org: it.Org, Kind: string(it.Kind), RuleName: it.RuleName}
	}, filters)

	return out, err
}

// ListInstallations returns one page of installations over the visible
// orgs.
func (s *server) ListInstallations(ctx context.Context, req gen.ListInstallationsRequestObject) (gen.ListInstallationsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	limit, err := pageSize(req.Params.Limit)
	if err != nil {
		return nil, err
	}

	filters := struct{}{}

	var key idKey
	if _, err := cursorIn(req.Params.Cursor, &key, filters); err != nil {
		return nil, err
	}

	page, err := s.opts.Reader.Installations(ctx, p.Visible, key.ID, limit)
	if err != nil {
		return nil, err
	}

	now := s.opts.Now()
	out := gen.ListInstallations200JSONResponse{Items: make([]gen.Installation, 0, len(page.Items))}

	for i := range page.Items {
		out.Items = append(out.Items, installationOut(&page.Items[i], now))
	}

	out.NextCursor, err = nextCursor(page, func(it *store.InstallationStatus) any { return idKey{ID: it.InstallationID} }, filters)

	return out, err
}

// Rollout states.
const (
	rolloutInProgress = "in_progress"
	rolloutComplete   = "complete"
)

// GetPolicy returns the current policy version, its rollout and rules.
// The policy is one document for the fleet, so it is not org-scoped.
func (s *server) GetPolicy(ctx context.Context, _ gen.GetPolicyRequestObject) (gen.GetPolicyResponseObject, error) {
	pol, err := s.opts.Reader.Policy(ctx)
	if err != nil {
		return nil, readErr(err)
	}

	out := gen.GetPolicy200JSONResponse{
		Version: pol.Version, FirstSeenAt: pol.FirstSeenAt, RolloutCompletedAt: pol.RolloutCompletedAt,
		RolloutState: rolloutInProgress, Rules: make([]gen.PolicyRule, 0, len(pol.Summary.Rules)),
	}

	if pol.RolloutCompletedAt != nil {
		out.RolloutState = rolloutComplete
	}

	for i := range pol.Summary.Rules {
		out.Rules = append(out.Rules, policyRuleOut(&pol.Summary.Rules[i]))
	}

	return out, nil
}
