package dashboard

import (
	"github.com/donaldgifford/repo-guardian/internal/monitoring"
)

// Units, as Grafana names them.
const (
	unitShort   = "short"
	unitPercent = "percentunit" // a 0-1 ratio, rendered as a percentage
)

// legendRuleName labels a series by the rule it measures.
const legendRuleName = "{{ rule_name }}"

// Posture aggregation, and why it looks like this everywhere below.
//
// The posture gauges are published by ONE replica — the one holding the
// stale-sweep leader lock. A demoted replica keeps serving whatever it
// last published until its process restarts, and during a failover both
// briefly hold series. So the inner aggregation is always
// `max by (<the gauge's own labels>)`, which is correct in both states;
// `sum` double-counts through every leader change.
//
// The OUTER aggregation is then a plain `sum`, because once the inner
// max has collapsed the replicas there is exactly one series per org
// and adding orgs together is the fleet total. Getting these two the
// wrong way round is the easy mistake: `max` on the outside would
// report the largest org rather than the fleet, and `sum` on the inside
// would multiply the fleet by the number of replicas that ever held the
// lock.
const (
	trackedTotal     = `sum(max by (org) (repo_guardian_repos_tracked))`
	actionableByRule = `sum by (rule_name) (max by (rule_name, org) (repo_guardian_repos_actionable))`
)

// complianceByRule is the share of tracked repositories satisfying each
// rule.
//
// The trailing `and on() (... > 0)` is load-bearing: it drops the
// series entirely when nothing is tracked, so the panel reads "no data"
// rather than a confident number. A rule evaluated against no
// repository is not 100% compliant — it is unmeasured, and reporting a
// perfect score for it is the comfortable wrong number this whole
// design exists to remove. `report.CompliantPercent` returns
// `(0, false)` for the same case; the two must agree or the dashboard
// and the report will describe the same fleet differently.
const complianceByRule = `(
  1 - (` + actionableByRule + ` / scalar(` + trackedTotal + `))
)
and on() (` + trackedTotal + ` > 0)`

// e1KPI builds the KPI dashboard.
//
// Business-tier sources only, with the handful of service-tier
// headlines DESIGN-0022 puts here deliberately: an operator reading a
// compliance number needs to know, on the same screen, whether the
// thing producing it is running. Everything else service-shaped lives
// on E3.
func e1KPI(_ *monitoring.Model, ds Datasources) Dashboard {
	b := New("repo-guardian-kpi", "repo-guardian — KPI",
		"Fleet compliance posture and the health of the service that measures it.",
		[]string{"repo-guardian", "generated"})

	b = b.
		WithRow(Row("Fleet compliance")).
		WithPanel(Stat(ds, "Repositories tracked",
			"Active repositories with any recorded posture. Parked repositories "+
				"(archived, forked, unreadable) are excluded and counted separately.",
			unitShort, Query{Expr: trackedTotal})).
		WithPanel(Stat(ds, "Repositories unmeasurable",
			"Parked repositories, by reason. These are excluded from every compliance "+
				"number — a rising access_denied share means the numbers cover less of the fleet.",
			unitShort, Query{Expr: `sum(max by (org, reason) (repo_guardian_repos_unmeasurable))`})).
		WithPanel(TimeSeries(ds, "Compliance by rule",
			"Share of tracked repositories satisfying each rule. Reads 'no data' rather "+
				"than 100% when nothing is tracked, because an unmeasured rule is not a compliant one.",
			unitPercent, Query{Expr: complianceByRule, Legend: legendRuleName})).
		WithPanel(Table(ds, "Repositories failing each rule",
			"Current non-compliant count per rule. This is state read back from the store, "+
				"not a rate over check events: a repository fixed yesterday is not in it.",
			Query{Expr: actionableByRule}))

	b = b.
		WithRow(Row("Convergence")).
		WithPanel(TimeSeries(ds, "Open repo-guardian PRs by age",
			"CAVEAT: this series is incremented by whichever replica performs a check and "+
				"reset only by the replica holding the sweep lock, so on a multi-replica "+
				"deployment a non-leader's copy accumulates rather than reflecting the current "+
				"open set. Read the shape, not the absolute number, until it is re-derived "+
				"from the store.",
			unitShort, Query{
				Expr:   `sum by (age_bucket) (max by (org, rule, age_bucket) (repo_guardian_open_prs_by_rule))`,
				Legend: "{{ age_bucket }}",
			})).
		WithPanel(TimeSeries(ds, "PR throughput",
			"Opened versus auto-closed-as-satisfied. Both are business-tier PR events, so "+
				"they belong on one panel: convergence is the relationship between them, and "+
				"split across two panels nobody reads it that way.",
			unitShort,
			Query{Expr: `sum(rate(repo_guardian_prs_created_total[1h])) * 3600`, Legend: "opened/h"},
			Query{
				Expr:   `sum(rate(repo_guardian_prs_closed_total{reason="satisfied"}[1h])) * 3600`,
				Legend: "auto-closed/h",
			})).
		WithPanel(Stat(ds, "PRs open over 30 days",
			"A repo-guardian PR nobody merged. With auto_close_pr disabled a satisfied PR "+
				"stays open by design and will show up here.",
			unitShort, Query{Expr: `sum(max by (org, rule) (repo_guardian_open_prs_by_rule{age_bucket="30d+"}))`}))

	return Dashboard{
		Slug:    "repo-guardian-kpi",
		Title:   "repo-guardian — KPI",
		Folder:  GrafanaFolder,
		Builder: withServiceHealth(b, ds),
	}
}

// withServiceHealth adds the "is the thing that measures this actually
// running" section.
//
// It sits on the KPI dashboard rather than on E3 because a compliance
// number is only worth reading if the exporter behind it is alive, and
// an operator who has to open a second dashboard to find that out will
// not.
func withServiceHealth(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("Service health")).
		WithPanel(Stat(ds, "Check failure rate",
			"Share of repository checks that errored. Matches the expression behind "+
				"RepoGuardianHighErrorRate, which pages above 10%. clamp_min keeps the ratio "+
				"defined when no check has completed in the window; without it an idle fleet "+
				"divides by zero and reads NaN, which nobody can tell from a broken exporter.",
			unitPercent, Query{Expr: `sum(rate(repo_guardian_errors_total[15m]))
/
clamp_min(sum(rate(repo_guardian_repos_checked_total[15m])), 1)`})).
		WithPanel(Stat(ds, "GitHub API budget remaining",
			"Minimum remaining across installations. Fed solely by the sweep's per-installation "+
				"sample, so it updates once per sweep rather than continuously.",
			unitShort, Query{Expr: `min(repo_guardian_rate_limit_remaining)`})).
		WithPanel(Stat(ds, "Job queue depth",
			"Jobs waiting to be claimed. A depth that only grows means workers are not keeping up.",
			unitShort, Query{Expr: `max(repo_guardian_queue_depth{queue="jobs"})`})).
		WithPanel(Stat(ds, "Sweep leader",
			"1 when a replica holds the stale-sweep lock. 0 means nothing is enqueuing stale "+
				"repositories and every posture number above is frozen. Aggregated with max, "+
				"never sum: during failover both replicas can briefly hold a series.",
			unitShort, Query{Expr: `max(repo_guardian_scheduler_is_leader{name="stale-sweep"})`})).
		WithPanel(Stat(ds, "Posture exporter ticks",
			"Successful posture reads in the last hour. Zero means every gauge above is stale "+
				"but still being served, which is the one failure the gauges cannot show "+
				"themselves. RepoGuardianPostureExportStalled pages on it.",
			unitShort,
			Query{Expr: `sum(increase(repo_guardian_posture_export_total{outcome="ok"}[1h]))`}))
}
