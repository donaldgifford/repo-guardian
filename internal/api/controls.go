package api

import (
	"cmp"
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// worstControls is how many controls /orgs/{org} ranks.
const worstControls = 5

// History bounds, in days (the spec's ?days=).
const (
	defaultHistoryDays = 30
	maxHistoryDays     = 365
)

// sumPosture adds compliance rows; every total is recomputed from the
// summed counts, as the query does per row. No rows is the zero
// posture, whose percent and coverage are nil.
func sumPosture(rows []store.ControlPosture, keep func(*store.ControlPosture) bool) store.ControlPosture {
	var total store.ControlPosture

	for i := range rows {
		if keep == nil || keep(&rows[i]) {
			total = total.Plus(&rows[i])
		}
	}

	return total
}

// repositoryTotals splits per-org repository counts into tracked and
// parked.
func repositoryTotals(counts []store.OrgRepositoryCount) (tracked, parked int) {
	for _, c := range counts {
		if c.Active {
			tracked += c.Count
		} else {
			parked += c.Count
		}
	}

	return tracked, parked
}

// GetSummary returns fleet totals over the visible orgs.
func (s *server) GetSummary(ctx context.Context, _ gen.GetSummaryRequestObject) (gen.GetSummaryResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	view, err := s.opts.Reader.Summary(ctx, p.Visible, s.opts.Now())
	if err != nil {
		return nil, err
	}

	tracked, parked := repositoryTotals(view.Repositories)
	total := sumPosture(view.Compliance, nil)

	return gen.GetSummary200JSONResponse{
		Tracked: tracked, Parked: parked, Compliance: postureOut(&total), OpenPrs: view.OpenPRs,
		Evaluated24h: view.Evaluated, LastEvaluatedAt: view.LastEvaluatedAt, PolicyVersion: optStr(view.PolicyVersion),
		RemediationBacklog: view.RemediationBacklog,
	}, nil
}

// catalogueEntry is a control's title and type, from its newest version
// in the active policy's catalogue.
type catalogueEntry struct {
	title, typ string
	version    int
}

func catalogueBySlug(defs []policy.ControlSummary) map[string]catalogueEntry {
	out := make(map[string]catalogueEntry, len(defs))

	for _, d := range defs {
		if e, ok := out[d.Control.Slug]; ok && e.version > d.Control.Version {
			continue
		}

		out[d.Control.Slug] = catalogueEntry{title: d.Title, typ: d.Type, version: d.Control.Version}
	}

	return out
}

// ListControls returns every control assigned in the visible orgs, by id.
func (s *server) ListControls(ctx context.Context, req gen.ListControlsRequestObject) (gen.ListControlsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	view, err := s.opts.Reader.Controls(ctx, p.Visible, "")
	if err != nil {
		return nil, err
	}

	org := deref(req.Params.Org)
	byID := map[string][]store.ControlPosture{}

	for i := range view.Compliance {
		row := view.Compliance[i]
		if org == "" || strings.EqualFold(row.Org, org) {
			byID[row.ControlID] = append(byID[row.ControlID], row)
		}
	}

	cat := catalogueBySlug(view.Catalogue)
	out := gen.ListControls200JSONResponse{Items: make([]gen.ControlSummary, 0, len(byID))}

	for _, id := range slices.Sorted(maps.Keys(byID)) {
		total := sumPosture(byID[id], nil)
		e := cat[id]
		out.Items = append(out.Items, gen.ControlSummary{Id: id, Title: optStr(e.title), Type: e.typ, Compliance: postureOut(&total)})
	}

	return out, nil
}

// GetControl returns one control's fleet compliance and its per-org
// breakdown with the versions each org carries.
func (s *server) GetControl(ctx context.Context, req gen.GetControlRequestObject) (gen.GetControlResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	if !control.ValidSlug.MatchString(req.Id) {
		return nil, statusError(http.StatusBadRequest, "id is not a control slug")
	}

	view, err := s.opts.Reader.Controls(ctx, p.Visible, req.Id)
	if err != nil {
		return nil, err
	}

	if len(view.Compliance) == 0 && len(view.Versions) == 0 {
		return nil, statusError(http.StatusNotFound, "")
	}

	versions := map[string][]gen.ControlVersion{}
	for _, v := range view.Versions {
		k := strings.ToLower(v.Org) + "\x00" + v.Decision
		versions[k] = append(versions[k], gen.ControlVersion{Version: v.Version, Revision: v.Revision, Repositories: v.Repositories})
	}

	total := sumPosture(view.Compliance, nil)
	e := catalogueBySlug(view.Catalogue)[req.Id]
	out := gen.GetControl200JSONResponse{
		Id: req.Id, Title: optStr(e.title), Type: e.typ, Compliance: postureOut(&total),
		Orgs: make([]gen.ControlOrg, 0, len(view.Compliance)),
	}

	for i := range view.Compliance {
		row := &view.Compliance[i]

		vs := versions[strings.ToLower(row.Org)+"\x00"+row.Decision]
		if vs == nil {
			vs = []gen.ControlVersion{}
		}

		out.Orgs = append(out.Orgs, gen.ControlOrg{Org: row.Org, Decision: row.Decision, Versions: vs, Compliance: postureOut(row)})
	}

	return out, nil
}

// baselineLineage reports whether a compliance row counts toward the
// baseline: a baseline control, whichever layer fulfils it (a replace
// keeps the lineage).
func baselineLineage(row *store.ControlPosture) bool {
	return row.Decision == "baseline" || row.Decision == "replaced"
}

// GetOrg returns one org's baseline-versus-org compliance, its worst
// controls and its exclusions.
func (s *server) GetOrg(ctx context.Context, req gen.GetOrgRequestObject) (gen.GetOrgResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	view, err := s.opts.Reader.Org(ctx, p.Visible, req.Org)
	if err != nil {
		return nil, readErr(err)
	}

	tracked, parked := repositoryTotals(view.Repositories)
	baseline := sumPosture(view.Compliance, baselineLineage)
	added := sumPosture(view.Compliance, func(r *store.ControlPosture) bool { return !baselineLineage(r) })

	out := gen.GetOrg200JSONResponse{
		Org: view.Repositories[0].Org, Tracked: tracked, Parked: parked,
		Baseline: postureOut(&baseline), OrgAdded: postureOut(&added),
		Controls:      make([]gen.OrgControl, 0, len(view.Compliance)),
		WorstControls: []gen.OrgControl{},
		Exclusions:    make([]gen.Exclusion, 0, len(view.Exclusions)),
	}

	var measured []*store.ControlPosture

	for i := range view.Compliance {
		row := &view.Compliance[i]
		out.Controls = append(out.Controls, orgControlOut(row))

		if row.Percent != nil {
			measured = append(measured, row)
		}
	}

	slices.SortStableFunc(measured, func(a, b *store.ControlPosture) int {
		return cmp.Or(cmp.Compare(*a.Percent, *b.Percent), cmp.Compare(a.ControlID, b.ControlID))
	})

	for _, row := range measured[:min(worstControls, len(measured))] {
		out.WorstControls = append(out.WorstControls, orgControlOut(row))
	}

	for _, e := range view.Exclusions {
		out.Exclusions = append(out.Exclusions, gen.Exclusion{
			Repository: e.Repository, ControlId: optStr(e.ControlID), State: e.State, Reason: e.Reason, ExcludedBy: optStr(e.ExcludedBy),
		})
	}

	return out, nil
}

func orgControlOut(row *store.ControlPosture) gen.OrgControl {
	return gen.OrgControl{ControlId: row.ControlID, Decision: row.Decision, Compliance: postureOut(row)}
}

// GetComplianceHistory returns each UTC day's last snapshot per org,
// control and lineage over the visible orgs, oldest first.
func (s *server) GetComplianceHistory(
	ctx context.Context, req gen.GetComplianceHistoryRequestObject,
) (gen.GetComplianceHistoryResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	q := req.Params

	days := defaultHistoryDays
	if q.Days != nil {
		days = *q.Days
	}

	if days < 1 || days > maxHistoryDays {
		return nil, statusError(http.StatusBadRequest, "days must be from 1 to 365")
	}

	if q.Control != nil && !control.ValidSlug.MatchString(*q.Control) {
		return nil, statusError(http.StatusBadRequest, "control is not a control slug")
	}

	rows, err := s.opts.Reader.ComplianceHistory(ctx, p.Visible, &store.HistoryFilter{
		ControlID: deref(q.Control), Org: deref(q.Org), Since: s.opts.Now().Add(-time.Duration(days) * 24 * time.Hour),
	})
	if err != nil {
		return nil, err
	}

	out := gen.GetComplianceHistory200JSONResponse{Items: make([]gen.HistoryPoint, 0, len(rows))}
	for i := range rows {
		r := &rows[i]
		out.Items = append(out.Items, gen.HistoryPoint{
			Org: r.Org, ControlId: r.ControlID, Decision: r.Decision, SnapshotAt: r.SnapshotAt.UTC(), Compliance: postureOut(&r.ControlPosture),
		})
	}

	return out, nil
}

// ListRepositoryControls returns a repository's assignments with their
// results, rule results and evidence.
func (s *server) ListRepositoryControls(
	ctx context.Context, req gen.ListRepositoryControlsRequestObject,
) (gen.ListRepositoryControlsResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	controls, err := s.opts.Reader.RepositoryControls(ctx, p.Visible, req.Id)
	if err != nil {
		return nil, readErr(err)
	}

	out := gen.ListRepositoryControls200JSONResponse{Items: make([]gen.RepositoryControl, 0, len(controls))}

	for i := range controls {
		c, err := repositoryControlOut(&controls[i])
		if err != nil {
			return nil, err
		}

		out.Items = append(out.Items, c)
	}

	return out, nil
}
