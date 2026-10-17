package api_test

import (
	"bytes"
	"net/http"
	"slices"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/control"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

func id(slug string, v int) control.ID { return control.ID{Slug: slug, Version: v} }

// twoOrgPolicy is a policy over acme-web and acme-pay where each org's
// layer refers to controls the other's does not.
func twoOrgPolicy() *store.ActivePolicy {
	return &store.ActivePolicy{
		Version: "controls:abc", FirstSeenAt: now, ActivatedAt: now,
		Summary: policy.Summary{
			Version: "controls:abc",
			Enterprise: policy.EnterpriseSummary{
				Orgs: []string{"acme-web", "acme-pay"}, Mode: policy.ModeEvaluate, Controls: []control.ID{id("codeowners", 1)},
			},
			Orgs: []policy.OrgSummary{
				{Org: "acme-web", Added: []policy.AddedSummary{{Control: id("labels", 1)}}},
				{
					Org:           "acme-pay",
					Mode:          policy.ModeRemediate,
					Replaced:      []policy.ReplacedSummary{{Control: id("codeowners", 1), With: id("codeowners", 2), Reason: "pay-review"}},
					Excluded:      []policy.ExcludedSummary{{Control: id("ruleset", 1), Reason: "ledger-freeze"}},
					ExcludedRepos: []policy.ExcludedReposSummary{{Match: []string{"ledger-*"}, Reason: "pci"}},
				},
			},
			Controls: []policy.ControlSummary{
				{Control: id("codeowners", 1), Type: "codeowners", Revision: "r1"},
				{Control: id("codeowners", 2), Type: "codeowners", Revision: "r2"},
				{Control: id("labels", 1), Type: "labels", Revision: "r3"},
				{Control: id("ruleset", 1), Type: "branch_ruleset", Revision: "r4"},
			},
		},
	}
}

// TestPoliciesSummary_IsScoped pins AR-0032-12: the stored summary is
// global and filtered in Go, so a single-org principal must see its own
// org's policy and the definitions it refers to, and nothing that names
// or reveals another org: no other org, no enterprise list, no baseline.
func TestPoliciesSummary_IsScoped(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.reader.policy = twoOrgPolicy()

	resp := h.client.Get("/policies", h.issuer.Valid(t, "team-web"))
	if resp.Status != http.StatusOK {
		t.Fatalf("policies = %d: %s", resp.Status, resp.Body)
	}

	for _, leak := range []string{"acme-pay", "pay-review", "ledger", "pci", "codeowners@2", "ruleset"} {
		if bytes.Contains(resp.Body, []byte(leak)) {
			t.Errorf("team-web's /policies contains %q, which only acme-pay's policy names:\n%s", leak, resp.Body)
		}
	}

	scoped := decode[gen.Policies](t, resp)
	if len(scoped.Orgs) != 1 || scoped.Orgs[0].Org != "acme-web" {
		t.Errorf("orgs = %+v, want acme-web alone", scoped.Orgs)
	}

	if scoped.Enterprise != nil {
		t.Errorf("enterprise = %+v, want it hidden from a principal who does not see every org", scoped.Enterprise)
	}

	if got := controlIDs(scoped.Controls); !slices.Equal(got, []string{"labels@1"}) {
		t.Errorf("controls = %v, want only acme-web's addition", got)
	}

	all := decode[gen.Policies](t, h.client.Get("/policies", h.issuer.Valid(t, "platform")))
	if len(all.Orgs) != 2 || all.Enterprise == nil || len(all.Controls) != 4 {
		t.Errorf("platform /policies = %+v, want both orgs, the enterprise block and every definition", all)
	}
}

func TestPolicies_NotFoundBeforeActivation(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	if resp := h.client.Get("/policies", h.issuer.Valid(t, "platform")); resp.Status != http.StatusNotFound {
		t.Errorf("policies before activation = %d, want 404", resp.Status)
	}
}

func controlIDs(cs []gen.PolicyControl) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Id)
	}

	return out
}
