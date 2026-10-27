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
// Business and service signals sliced by org. Store, Temporal and
// budget-wait metrics are deliberately absent: they are fleet-scoped, so
// a per-org row of them would repeat the same number under every
// heading and invite someone to read it as that org's share. They live
// on E3.
func e2Detail(m *monitoring.Model, ds Datasources, id Identity) Dashboard {
	b := New(id.slug(kindDetail), id.title(labelDetail),
		"Fleet aggregates and a section per organisation.",
		[]string{tagProject, tagGenerated})

	orgs := declarableOrgs(m)

	// The variable is declared only when the rows are discovered,
	// because a dashboard carrying a variable nothing references
	// invites someone to switch it and wonder why nothing moved.
	if len(orgs) == 0 {
		b = b.WithVariable(OrgVariable(ds))
	}

	b = withFleetSection(b, ds)
	b = withRemediationSection(b, ds)

	if len(orgs) == 0 {
		return id.dashboard(kindDetail, labelDetail, withOrgSection(b, ds, orgVar, "Organisation: $org"))
	}

	for _, org := range orgs {
		b = withOrgSection(b, ds, org, "Organisation: "+org)
	}

	return id.dashboard(kindDetail, labelDetail, b)
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
		WithPanel(TimeSeries(ds, "Compliance by org",
			"Each organisation's compliant share across its control assignments. An org drops "+
				"out rather than reading 100% when it measures nothing.",
			unitPercent, Query{Expr: compliance("org", ""), Legend: legendOrg})).
		WithPanel(TimeSeries(ds, "Control assignments by org",
			"Assignments per organisation. A line that drops to zero is an org that stopped "+
				"being measured — which is the signal a declared row exists to preserve.",
			unitShort, Query{Expr: `sum by (org) (` + coverage("") + `)`, Legend: legendOrg})).
		WithPanel(TimeSeries(ds, "Repositories parked in the last 24h",
			"Why repositories left the measurable fleet. This is the denominator context for "+
				"every compliance number above: archived and fork parks are routine, a rising "+
				"access_denied share is the one to act on.",
			unitShort, Query{
				Expr:   `sum by (reason) (increase(repo_guardian_repos_parked_total[24h]))`,
				Legend: legendReason,
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
				Legend: legendOrg,
			}))
}

// withRemediationSection charts remediation across the fleet (IMPL-0030
// 7.3). The two gauges are published by every api replica from the
// store, so they collapse replicas with max by before summing, exactly
// as the posture gauges do.
func withRemediationSection(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("Remediation")).
		WithPanel(TimeSeries(ds, "Remediation backlog: held results by reason",
			"Control results held from remediation, by hold. pr_cap and stale_branch "+
				"are routine and clear on their own; foreign_branch, permission and a "+
				"conflict held past a week need a human.",
			unitShort, Query{
				Expr:   `sum by (reason) (max by (org, reason, age_bucket) (repo_guardian_remediation_holds))`,
				Legend: legendReason,
			})).
		WithPanel(TimeSeries(ds, "Open remediation pull requests by age",
			"Open pull requests repo-guardian is waiting on someone to merge. A growing 30d+ "+
				"band is a fleet that is not taking its remediations.",
			unitShort, Query{
				Expr:   `sum by (age_bucket) (max by (org, control, age_bucket) (repo_guardian_remediation_prs_open))`,
				Legend: "{{ age_bucket }}",
			})).
		WithPanel(TimeSeries(ds, "Remediations blocked in the last 24h",
			"New holds and Remediation App access losses by reason. The E4 logs name the "+
				"repositories.",
			unitShort, Query{
				Expr:   `sum by (reason) (increase(repo_guardian_remediation_blocked_total[24h]))`,
				Legend: legendReason,
			})).
		WithPanel(TimeSeries(ds, "Remediation run results in the last 24h",
			"What remediation runs and lifecycle maintenance did. noop is a run that found "+
				"nothing to change; deferred is a run waiting for a budget reset, not a failure.",
			unitShort, Query{
				Expr:   `sum by (result) (increase(repo_guardian_remediation_runs_total[24h]))`,
				Legend: "{{ result }}",
			}))
}

// Legends shared across panels.
const (
	// legendOrg labels a series by organisation.
	legendOrg = "{{ org }}"
	// legendReason labels a series by reason.
	legendReason = "{{ reason }}"
)

// withOrgSection adds one organisation's row.
//
// org is either a literal name or the "$org" template variable; both
// are legal inside a PromQL label matcher, and Grafana interpolates the
// latter before the query is sent.
func withOrgSection(b *Builder, ds Datasources, org, title string) *Builder {
	sel := fmt.Sprintf(`{org=%q}`, org)

	return b.
		WithRow(Row(title)).
		WithPanel(Stat(ds, "Control assignments",
			"Assignments in this organisation. Reads 'no data' if the org has stopped being "+
				"measured entirely, which is a different condition from zero assignments.",
			unitShort, Query{Expr: `sum(` + coverage(org) + `)`})).
		WithPanel(TimeSeries(ds, "Compliance by control",
			"Share of this organisation's measured repositories passing each control. Blank "+
				"for a control that measures nothing here — an unmeasured control is not a "+
				"compliant one.",
			unitPercent, Query{Expr: compliance("control", org), Legend: legendControl})).
		WithPanel(Table(ds, "Assignments by control and status",
			"Current bucket counts per control for this organisation. State read back from "+
				"the store, not a rate over evaluation events.",
			Query{Expr: `max by (control, bucket) (` + seriesStatus + sel + `)`})).
		WithPanel(TimeSeries(ds, "Evaluation errors and changes",
			"Control evaluations that ended in an error, and recorded results whose evidence "+
				"moved, over a day. Every control of the org erroring at once is a permission "+
				"problem, not a repository problem.",
			unitShort,
			Query{
				Expr:   `sum by (control) (increase(repo_guardian_evaluation_errors_total` + sel + `[24h]))`,
				Legend: "errors ({{ control }})",
			},
			Query{
				Expr:   `sum by (control) (increase(repo_guardian_evaluation_changes_total` + sel + `[24h]))`,
				Legend: "changes ({{ control }})",
			}))
}
