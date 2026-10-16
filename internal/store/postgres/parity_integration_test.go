//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/api/gen"
	"github.com/donaldgifford/repo-guardian/internal/store"
)

// TestCompliance_ParityAcrossReportAPIAndSnapshot is IMPL-0025 15.7: the
// same seed gives identical percentages through the report, /rules,
// /orgs and the snapshot writer. Each org carries exactly one rule so
// every surface describes the same (org, rule) population; the counts
// are chosen so flooring and rounding disagree (2/3 floors to 66.6).
func TestCompliance_ParityAcrossReportAPIAndSnapshot(t *testing.T) {
	t.Parallel()

	f := newV2Fixture(t)
	ctx := context.Background()

	seed := []struct {
		org, rule          string
		compliant, failing int
	}{
		{org: "par-a", rule: "codeowners", compliant: 2, failing: 1},
		{org: "par-b", rule: "renovate", compliant: 1, failing: 6},
		{org: "par-c", rule: "dependabot", compliant: 0, failing: 3},
	}

	n := 0
	for _, s := range seed {
		for i := range s.compliant + s.failing {
			n++

			res, err := f.store.UpsertDiscovered(ctx, &store.DiscoveredRepo{Org: s.org, Name: "r" + strconv.Itoa(i), InstallationID: testInstallation})
			if err != nil {
				t.Fatal(err)
			}

			o := compliant(s.rule)
			if i >= s.compliant {
				o = missing(s.rule, "X")
			}

			if _, err := f.store.RecordCheck(ctx, &store.CheckRecord{
				Key: "parity-" + strconv.Itoa(n), RepositoryID: res.ID, InstallationID: testInstallation,
				Trigger: store.TriggerSchedule, PolicyVersion: "v2:test", StartedAt: t0, FinishedAt: t0, Outcomes: []store.Outcome{o},
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	if _, err := f.store.InsertComplianceSnapshot(ctx, t0); err != nil {
		t.Fatal(err)
	}

	rep, err := f.store.ComplianceReport(ctx, store.Scope{Orgs: []string{"par-a", "par-b", "par-c"}})
	if err != nil {
		t.Fatal(err)
	}

	// The report renders the shared query's percentage as computed.
	fromReport := map[string]string{}
	for _, row := range rep.Current {
		fromReport[row.Org+"/"+row.RuleName] = pct(row.Percent)
	}

	c, _ := apiClient(t, f)

	fromRules := map[string]string{}
	for _, rc := range getJSON[gen.RuleList](t, c, "/rules").Items {
		fromRules[rc.Name] = pct(rc.CompliantPercent)
	}

	fromOrgs := map[string]string{}
	for _, o := range getJSON[gen.OrgList](t, c, "/orgs").Items {
		fromOrgs[o.Org] = pct(o.CompliantPercent)
	}

	fromOrgDetail := map[string]string{}
	fromSnapshot := map[string]string{}

	for _, s := range seed {
		for _, rc := range getJSON[gen.OrgDetail](t, c, "/orgs/"+s.org).Rules {
			fromOrgDetail[s.org+"/"+rc.Name] = pct(rc.CompliantPercent)
		}

		for _, h := range getJSON[gen.HistoryPage](t, c, "/compliance/history?org="+s.org).Items {
			fromSnapshot[deref(h.Org)+"/"+deref(h.Rule)] = pct(h.CompliantPercent)
		}
	}

	// aggregate=fleet (IMPL-0031 7.1) agrees with the report's rows
	// summed across the fleet, floored the same way.
	var fleet store.StatusCounts
	for i := range rep.Current {
		fleet = fleet.Add(rep.Current[i].Counts())
	}

	agg := getJSON[gen.HistoryPage](t, c, "/compliance/history?aggregate=fleet").Items
	if len(agg) != 1 || pct(agg[0].CompliantPercent) != pct(store.CompliantPercent(fleet)) {
		t.Errorf("aggregate=fleet = %+v, want one point at %s", agg, pct(store.CompliantPercent(fleet)))
	}

	want := map[string]string{"par-a/codeowners": "66.6", "par-b/renovate": "14.2", "par-c/dependabot": "0"}

	for key, w := range want {
		org, rule := splitKey(key)

		for surface, got := range map[string]string{
			"report": fromReport[key], "/rules": fromRules[rule], "/orgs": fromOrgs[org],
			"/orgs/{org}": fromOrgDetail[key], "snapshot": fromSnapshot[key],
		} {
			if got != w {
				t.Errorf("%s via %s = %s, want %s", key, surface, got, w)
			}
		}
	}
}

func pct(p *float64) string {
	if p == nil {
		return "unmeasured"
	}

	return fmt.Sprint(*p)
}

func splitKey(k string) (org, rule string) {
	for i := range k {
		if k[i] == '/' {
			return k[:i], k[i+1:]
		}
	}

	return k, ""
}
