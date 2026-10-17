package api

import (
	"context"
	"slices"
	"strings"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// GetPolicies returns the active policy filtered to the caller's orgs
// (DESIGN-0032 AR-0032-12). The stored summary is global, so this is the
// one view scoped in Go rather than SQL; TestPoliciesSummary_IsScoped
// holds it to that.
func (s *server) GetPolicies(ctx context.Context, _ gen.GetPoliciesRequestObject) (gen.GetPoliciesResponseObject, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}

	active, err := s.opts.Reader.ActivePolicy(ctx)
	if err != nil {
		return nil, readErr(err)
	}

	return gen.GetPolicies200JSONResponse(policiesOut(active, p.Visible)), nil
}

// policiesOut filters a policy summary to scope: the visible orgs'
// policies, the catalogue definitions they refer to, and the enterprise
// fields only for a scope that sees every org.
func policiesOut(a *store.ActivePolicy, scope store.APIScope) gen.Policies {
	out := gen.Policies{
		Version: a.Version, FirstSeenAt: a.FirstSeenAt, ActivatedAt: a.ActivatedAt, RolloutCompletedAt: a.RolloutCompletedAt,
		Orgs: []gen.PolicyOrg{}, Controls: []gen.PolicyControl{},
	}

	sum := &a.Summary
	referenced := map[control.ID]bool{}
	visible := scope.Orgs()

	for i := range sum.Orgs {
		o := &sum.Orgs[i]
		if !scope.All() && !slices.Contains(visible, strings.ToLower(o.Org)) {
			continue
		}

		out.Orgs = append(out.Orgs, policyOrgOut(o, referenced))
	}

	if scope.All() {
		e := gen.PolicyEnterprise{Orgs: nonNil(sum.Enterprise.Orgs), Mode: string(sum.Enterprise.Mode), Controls: idStrings(sum.Enterprise.Controls)}
		out.Enterprise = &e
	}

	for _, d := range sum.Controls {
		if scope.All() || referenced[d.Control] {
			out.Controls = append(out.Controls, gen.PolicyControl{Id: d.Control.String(), Title: optStr(d.Title), Type: d.Type, Revision: d.Revision})
		}
	}

	return out
}

func policyOrgOut(o *policy.OrgSummary, referenced map[control.ID]bool) gen.PolicyOrg {
	out := gen.PolicyOrg{
		Org: o.Org, Mode: optStr(o.Mode),
		Added: []gen.PolicyChange{}, Replaced: []gen.PolicyChange{}, Excluded: []gen.PolicyChange{},
		ExcludedRepos: []gen.PolicyExcludedRepos{}, Repos: []gen.PolicyRepos{},
	}

	for _, c := range o.Added {
		referenced[c.Control] = true
		out.Added = append(out.Added, gen.PolicyChange{Control: c.Control.String(), Block: optStr(c.Block)})
	}

	for _, c := range o.Replaced {
		referenced[c.Control], referenced[c.With] = true, true
		with := c.With.String()
		out.Replaced = append(out.Replaced, gen.PolicyChange{
			Control: c.Control.String(), With: &with, Reason: optStr(c.Reason), Block: optStr(c.Block),
		})
	}

	for _, c := range o.Excluded {
		referenced[c.Control] = true
		out.Excluded = append(out.Excluded, gen.PolicyChange{Control: c.Control.String(), Reason: optStr(c.Reason), Block: optStr(c.Block)})
	}

	for _, e := range o.ExcludedRepos {
		out.ExcludedRepos = append(out.ExcludedRepos, gen.PolicyExcludedRepos{Match: nonNil(e.Match), Reason: e.Reason})
	}

	for _, r := range o.Repos {
		out.Repos = append(out.Repos, gen.PolicyRepos{Label: r.Label, Match: nonNil(r.Match), Mode: optStr(r.Mode)})
	}

	return out
}

func idStrings(ids []control.ID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}

	return out
}

// nonNil keeps a required array from encoding as null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}
