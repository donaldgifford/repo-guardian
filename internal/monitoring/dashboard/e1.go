package dashboard

import (
	"github.com/donaldgifford/repo-guardian/internal/monitoring"
)

// Units, as Grafana names them.
const (
	unitShort   = "short"
	unitPercent = "percentunit" // a 0-1 ratio, rendered as a percentage
)

// legendControl labels a series by the control it measures.
const legendControl = "{{ control }}"

// The posture gauges, as the api role's exporter publishes them.
const (
	seriesStatus   = "repo_guardian_controls_status"
	seriesCoverage = "repo_guardian_controls_coverage"
)

// Posture aggregation, and why it looks like this everywhere below.
//
// Every api replica exports the posture gauges from the same compliance
// query (IMPL-0029 OQ8), and a replica that has stopped exporting keeps
// serving its last values until its process restarts. So the inner
// aggregation is always `max by (<the gauge's own labels>)`, which
// collapses identical replicas to one series; `sum` would multiply the
// fleet by the number of replicas.
//
// The OUTER aggregation is then a plain `sum`, because once the inner
// max has collapsed the replicas there is exactly one series per org
// and control, and adding them together is the fleet total. Getting
// these two the wrong way round is the easy mistake: `max` on the
// outside reports the largest org rather than the fleet, and `sum` on
// the inside multiplies the fleet by the replica count.

// orgMatcher narrows a posture selector to org ("" for the fleet). org
// may be the "$org" template variable; Grafana interpolates it.
func orgMatcher(org string) string {
	if org == "" {
		return ""
	}

	return `, org="` + org + `"`
}

// bucket is one status bucket per (org, control), deduped across
// replicas.
func bucket(name, org string) string {
	return `max by (org, control) (` + seriesStatus + `{bucket="` + name + `"` + orgMatcher(org) + `})`
}

// measured is the compliant and non-compliant buckets: the denominator
// of the shared percent definition (store.CompliantPercent). Pending,
// stale and unmeasurable repositories are outside it.
func measured(org string) string {
	return `max by (org, control, bucket) (` + seriesStatus + `{bucket=~"compliant|non_compliant"` + orgMatcher(org) + `})`
}

// coverage is the number of repositories each control is assigned to.
func coverage(org string) string {
	sel := ""
	if org != "" {
		sel = `{org="` + org + `"}`
	}

	return `max by (org, control) (` + seriesCoverage + sel + `)`
}

// compliance is the share of measured repositories that pass, grouped
// by grouping ("" for one number).
//
// Two guards keep it from reporting a number nobody measured. The
// denominator is filtered to `> 0`, so a control with nothing compliant
// or non-compliant divides by nothing and drops out; and the trailing
// `and on (...)` drops the series when nothing is assigned at all. A
// control assigned to no repository is not 100% compliant — it is
// unmeasured — and the report and API return no percentage for the same
// case (store.CompliantPercent returns nil).
func compliance(grouping, org string) string {
	agg := "sum"
	on := "on()"

	if grouping != "" {
		agg = "sum by (" + grouping + ")"
		on = "on (" + grouping + ")"
	}

	return `(
  ` + agg + ` (` + bucket("compliant", org) + `)
  /
  (` + agg + ` (` + measured(org) + `) > 0)
)
and ` + on + ` (` + agg + ` (` + coverage(org) + `) > 0)`
}

// e1KPI builds the KPI dashboard.
//
// Business-tier sources only, with the handful of service-tier
// headlines DESIGN-0022 puts here deliberately: an operator reading a
// compliance number needs to know, on the same screen, whether the
// thing producing it is running. Everything else service-shaped lives
// on E3.
func e1KPI(_ *monitoring.Model, ds Datasources, id Identity) Dashboard {
	b := New(id.slug(kindKPI), id.title(labelKPI),
		"Fleet controls posture and the health of the service that measures it.",
		[]string{tagProject, tagGenerated})

	b = b.
		WithRow(Row("Fleet compliance")).
		WithPanel(Stat(ds, "Fleet compliance",
			"Compliant over compliant plus non-compliant, across every control assignment. "+
				"Pending, stale and unmeasurable repositories are outside the denominator. Reads "+
				"'no data' rather than 100% when nothing is assigned, because an unmeasured "+
				"fleet is not a compliant one.",
			unitPercent, Query{Expr: compliance("", "")})).
		WithPanel(Stat(ds, "Control assignments",
			"Repositories each control is actively assigned to, summed over controls. One "+
				"repository with three controls counts three times: this is the amount of "+
				"posture being measured, not the number of repositories.",
			unitShort, Query{Expr: `sum(` + coverage("") + `)`})).
		WithPanel(Stat(ds, "Unmeasurable assignments",
			"Assignments the evaluator could not measure: access denied, a suspended "+
				"installation, or an ownership conflict. They are outside every compliance "+
				"number, so a rising count means the numbers cover less of the fleet.",
			unitShort, Query{Expr: `sum(` + bucket("unmeasurable", "") + `)`})).
		WithPanel(TimeSeries(ds, "Compliance by control",
			"Share of each control's measured repositories that pass. A control drops out "+
				"rather than reading 100% when it measures nothing.",
			unitPercent, Query{Expr: compliance("control", ""), Legend: legendControl})).
		WithPanel(Table(ds, "Repositories failing each control",
			"Current non-compliant count per control. State read back from the store, not a "+
				"rate over evaluation events: a repository fixed yesterday is not in it.",
			Query{Expr: `sum by (control) (` + bucket("non_compliant", "") + `)`})).
		WithPanel(TimeSeries(ds, "Assignments by status",
			"Every assignment in exactly one bucket. Pending is assigned but not yet "+
				"evaluated; stale is evaluated longer ago than the freshness window. A growing "+
				"pending or stale share means evaluation is not keeping up with the fleet.",
			unitShort, Query{
				Expr:   `sum by (bucket) (max by (org, control, bucket) (` + seriesStatus + `))`,
				Legend: "{{ bucket }}",
			}))

	return id.dashboard(kindKPI, labelKPI, withServiceHealth(b, ds))
}

// withServiceHealth adds the "is the thing that measures this actually
// running" section.
//
// It sits on the KPI dashboard rather than on E3 because a compliance
// number is only worth reading if the evaluator behind it is alive, and
// an operator who has to open a second dashboard to find that out will
// not.
func withServiceHealth(b *Builder, ds Datasources) *Builder {
	return b.
		WithRow(Row("Service health")).
		WithPanel(Stat(ds, "Evaluation error rate",
			"Share of evaluations that ended in an error. The `> 0` on the denominator makes "+
				"an idle evaluator read 'no data' instead of dividing by zero.",
			unitPercent, Query{Expr: `sum(rate(repo_guardian_evaluations_total{outcome="error"}[15m]))
/
(sum(rate(repo_guardian_evaluations_total[15m])) > 0)`})).
		WithPanel(Stat(ds, "Evaluation freshness p95",
			"Age of a control's previous result when it is re-evaluated. This is the freshness "+
				"SLO: a value climbing past EVAL_INTERVAL means repositories wait longer than "+
				"their timer between evaluations.",
			unitSeconds, Query{Expr: `histogram_quantile(0.95,
  sum by (le) (rate(repo_guardian_evaluation_freshness_seconds_bucket[1h]))
)`})).
		WithPanel(Stat(ds, "GitHub API budget remaining",
			"Minimum remaining across installations, set by every evaluation that talks to "+
				"GitHub.",
			unitShort, Query{Expr: `min(repo_guardian_rate_limit_remaining)`})).
		WithPanel(Stat(ds, "Budget waits per hour",
			"Evaluations the installation budget told to wait for the rate-limit reset. "+
				"Sustained waits are backpressure: work is not lost, but the fleet is not "+
				"converging either.",
			unitShort, Query{Expr: `sum(rate(repo_guardian_budget_acquire_total{result="wait"}[1h])) * 3600`}))
}
