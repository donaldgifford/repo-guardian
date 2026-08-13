package dashboard

import (
	"fmt"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
)

// orgVar is the template variable the per-org panels fall back to when
// the policy declares no orgs to generate rows from.
const orgVar = "$org"

// e2Detail builds the per-org detail dashboard.
//
// Business and service signals sliced by org. Queue, store and
// scheduler metrics are deliberately absent: they are fleet-scoped, so
// a per-org row of them would repeat the same number under every
// heading and invite someone to read it as that org's share. They live
// on E3.
func e2Detail(m *monitoring.Model, ds Datasources) Dashboard {
	b := New("repo-guardian-detail", "repo-guardian — detail",
		"Fleet aggregates and a section per organisation.",
		[]string{"repo-guardian", "generated"})

	orgs := declarableOrgs(m)

	// The variable is declared only when the rows are discovered,
	// because a dashboard carrying a variable nothing references
	// invites someone to switch it and wonder why nothing moved.
	if len(orgs) == 0 {
		b = b.WithVariable(OrgVariable(ds))
	}

	b = withFleetSection(b, ds)

	if len(orgs) == 0 {
		return Dashboard{
			Slug:    "repo-guardian-detail",
			Title:   "repo-guardian — detail",
			Folder:  GrafanaFolder,
			Builder: withOrgSection(b, ds, orgVar, "Organisation: $org"),
		}
	}

	for _, org := range orgs {
		b = withOrgSection(b, ds, org, "Organisation: "+org)
	}

	return Dashboard{
		Slug:    "repo-guardian-detail",
		Title:   "repo-guardian — detail",
		Folder:  GrafanaFolder,
		Builder: b,
	}
}

// declarableOrgs returns the orgs a row can be generated for.
//
// Glob patterns are excluded, and that exclusion is the whole point of
// carrying Pattern on Org. `orgs = ["acme-*"]` cannot be enumerated
// without asking the API, so a declared row for it is impossible — and
// a discovered row cannot carry the silent-org signal, because a row
// that never appears and a row that appears empty are the same thing
// once the row set itself comes from the series.
func declarableOrgs(m *monitoring.Model) []string {
	if m == nil {
		return nil
	}

	out := make([]string, 0, len(m.Orgs))

	for i := range m.Orgs {
		if !m.Orgs[i].Pattern {
			out = append(out, m.Orgs[i].Name)
		}
	}

	return out
}

// withFleetSection adds the aggregate row.
func withFleetSection(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("Fleet")).
		WithPanel(TimeSeries(ds, "Repositories tracked by org",
			"Active repositories per organisation. A line that drops to zero is an org that "+
				"stopped reporting — which is the signal a declared row exists to preserve.",
			unitShort, Query{
				Expr:   `max by (org) (repo_guardian_repos_tracked)`,
				Legend: "{{ org }}",
			})).
		WithPanel(TimeSeries(ds, "Repositories failing each rule",
			"Fleet totals per rule, summed across orgs after the per-replica max.",
			unitShort, Query{Expr: actionableByRule, Legend: legendRuleName})).
		WithPanel(TimeSeries(ds, "Repositories parked in the last 24h",
			"Why repositories left the measurable fleet. This is the denominator context for "+
				"every compliance number above: a fleet whose tracked count drops needs the "+
				"archived and fork series beside it to tell 'repositories left the fleet' from "+
				"'the exporter broke'. A rising access_denied share is the one to act on.",
			unitShort, Query{
				Expr:   `sum by (reason) (increase(repo_guardian_repos_parked_total[24h]))`,
				Legend: "{{ reason }}",
			})).
		WithPanel(TimeSeries(ds, "GitHub API budget by org",
			"Remaining API budget, joined from the installation-keyed gauge onto orgs via "+
				"installation_info. group_left is what makes an installation_id-keyed series "+
				"answerable per org at all; without the join this panel could only be read by "+
				"someone who knows which installation belongs to whom.",
			unitShort, Query{
				Expr: `min by (org) (
  repo_guardian_rate_limit_remaining
  * on (installation_id) group_left(org) repo_guardian_installation_info
)`,
				Legend: "{{ org }}",
			}))
}

// withOrgSection adds one organisation's row.
//
// org is either a literal name or the "$org" template variable; both
// are legal inside a PromQL label matcher, and Grafana interpolates the
// latter before the query is sent.
func withOrgSection(b *Builder, ds Datasources, org, title string) *Builder {
	sel := fmt.Sprintf(`{org=%q}`, org)

	return b.
		WithRow(Row(title)).
		WithPanel(Stat(ds, "Repositories tracked",
			"Active repositories in this organisation. Reads 'no data' if the org has stopped "+
				"reporting entirely, which is a different condition from zero repositories.",
			unitShort, Query{Expr: `max by (org) (repo_guardian_repos_tracked` + sel + `)`})).
		WithPanel(TimeSeries(ds, "Compliance by rule",
			"Share of this organisation's tracked repositories satisfying each rule. Undefined, "+
				"and therefore blank, when the org tracks nothing — an unmeasured rule is not a "+
				"compliant one.",
			unitPercent, Query{Expr: orgCompliance(sel), Legend: legendRuleName})).
		WithPanel(Table(ds, "Repositories failing each rule",
			"Current non-compliant count per rule for this organisation. State read back from "+
				"the store, not a rate over check events.",
			Query{Expr: `max by (rule_name) (repo_guardian_repos_actionable` + sel + `)`})).
		WithPanel(TimeSeries(ds, "Rules skipped for scope or ignore",
			"Rules that never ran against this organisation's repositories. A rule skipped "+
				"everywhere is a rule that is not protecting anything, and it looks identical "+
				"to a rule that is passing.",
			unitShort,
			Query{
				Expr:   `sum by (level) (increase(repo_guardian_out_of_scope_total` + sel + `[24h]))`,
				Legend: "out of scope ({{ level }})",
			},
			Query{
				Expr:   `sum by (scope) (increase(repo_guardian_ignored_total` + sel + `[24h]))`,
				Legend: "ignored ({{ scope }})",
			}))
}

// orgCompliance is complianceByRule narrowed to one org.
//
// The `and on() (... > 0)` guard is carried over deliberately: without
// it an org with nothing tracked divides by zero and the panel reports
// a confident number for a fleet it has not measured.
func orgCompliance(sel string) string {
	tracked := `max by (org) (repo_guardian_repos_tracked` + sel + `)`

	return `(
  1 - (
    max by (rule_name) (repo_guardian_repos_actionable` + sel + `)
    / scalar(` + tracked + `)
  )
)
and on() (` + tracked + ` > 0)`
}
