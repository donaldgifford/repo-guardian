// Package dashboard is the panel library the four generated dashboards
// are authored in. See DESIGN-0022 §Dashboard suite (Finding E) and
// IMPL-0023 Phase 5 task 5.2 / Phase 6.
//
// There is no hand-maintained dashboard JSON anywhere in this
// repository, and this package is why. Panels are Go, so a panel that
// charts a rule can be generated from the rule, and a panel whose
// mechanism is not configured is never emitted at all.
//
// # Why this is a separate package
//
// Every byte of the grafana-foundation-sdk dependency enters here.
// internal/monitoring (the model) and the future alert package stay
// clean of it, so DESIGN-0022 OQ5's escape hatch — relocate the
// generator if the binary-size delta is egregious — is a move of this
// directory rather than an untangling. The alert half would keep
// working in the main binary, which is a genuinely useful degraded
// mode. Do NOT import this package from internal/monitoring.
//
// # One source per panel
//
// Every panel takes exactly one datasource and one query. That is the
// Finding I taxonomy rule, and it is a rule because a panel mixing a
// business-tier gauge with a service-tier counter cannot be read: when
// it looks wrong, there is no way to tell which half is lying.
package dashboard

import (
	"encoding/json"
	"fmt"

	"github.com/grafana/grafana-foundation-sdk/go/common"
	sdk "github.com/grafana/grafana-foundation-sdk/go/dashboard"
	"github.com/grafana/grafana-foundation-sdk/go/prometheus"
	"github.com/grafana/grafana-foundation-sdk/go/stat"
	"github.com/grafana/grafana-foundation-sdk/go/table"
	"github.com/grafana/grafana-foundation-sdk/go/timeseries"
)

// Datasource UIDs.
//
// Concrete UIDs, never the `${DS_PROMETHEUS}` input placeholder. A
// dashboard carrying an input prompts on every import, which makes the
// generated tier un-provisionable: grafana-operator applies a CR, and
// nobody is there to answer a prompt. The defaults are overridable at
// generation time for clusters that name their datasources
// differently.
const (
	DefaultPrometheusUID = "prometheus"
	DefaultLokiUID       = "loki"
)

// Datasource types as Grafana names them.
const (
	prometheusType = "prometheus"
	lokiType       = "loki"
)

// Datasources carries the UIDs a generated dashboard points at.
type Datasources struct {
	Prometheus string
	Loki       string
}

// WithDefaults fills empty UIDs with the defaults.
func (d Datasources) WithDefaults() Datasources {
	if d.Prometheus == "" {
		d.Prometheus = DefaultPrometheusUID
	}

	if d.Loki == "" {
		d.Loki = DefaultLokiUID
	}

	return d
}

// PrometheusRef returns the Prometheus datasource reference.
func (d Datasources) PrometheusRef() common.DataSourceRef {
	t := prometheusType

	return common.DataSourceRef{Type: &t, Uid: &d.Prometheus}
}

// LokiRef returns the Loki datasource reference.
func (d Datasources) LokiRef() common.DataSourceRef {
	t := lokiType

	return common.DataSourceRef{Type: &t, Uid: &d.Loki}
}

// Builder is a dashboard under construction.
//
// An alias rather than a wrapper, so it composes with the SDK's own
// builder methods. It exists so the dashboards in this package can name
// the type without every file needing the schema-v1 deprecation
// suppression that New and Render carry — the alias takes it once.
//
//nolint:staticcheck // SA1019: v1 is the only schema the panel builders produce, see New
type Builder = sdk.DashboardBuilder

// Query is one PromQL expression and how to label its series.
type Query struct {
	Expr   string
	Legend string

	// Instant asks for a single value at the evaluation time rather
	// than a range. Stat and table panels want this; a graph does not.
	Instant bool
}

// target builds the Prometheus dataquery for a Query.
func (q Query) target() *prometheus.DataqueryBuilder {
	b := prometheus.NewDataqueryBuilder().Expr(q.Expr)

	if q.Legend != "" {
		b = b.LegendFormat(q.Legend)
	}

	if q.Instant {
		return b.Instant()
	}

	return b.Range()
}

// noData is what every panel shows when its query returns nothing.
//
// Not "0", and not an empty panel. A zero is a measurement; an absent
// series is the absence of one, and rendering the second as the first
// is how a dashboard reports a healthy fleet while the exporter is
// dead. This is the same reasoning as the report's "n/a" for an
// unmeasured rule.
const noData = "no data"

// Stat builds a single-value panel.
func Stat(ds Datasources, title, description, unit string, q Query) *stat.PanelBuilder {
	q.Instant = true

	return stat.NewPanelBuilder().
		Title(title).
		Description(description).
		Datasource(ds.PrometheusRef()).
		Unit(unit).
		NoValue(noData).
		WithTarget(q.target())
}

// TimeSeries builds a graph panel.
func TimeSeries(ds Datasources, title, description, unit string, queries ...Query) *timeseries.PanelBuilder {
	b := timeseries.NewPanelBuilder().
		Title(title).
		Description(description).
		Datasource(ds.PrometheusRef()).
		Unit(unit).
		NoValue(noData)

	for _, q := range queries {
		b = b.WithTarget(q.target())
	}

	return b
}

// Table builds a tabular panel.
func Table(ds Datasources, title, description string, q Query) *table.PanelBuilder {
	q.Instant = true

	return table.NewPanelBuilder().
		Title(title).
		Description(description).
		Datasource(ds.PrometheusRef()).
		NoValue(noData).
		WithTarget(q.target())
}

// Row builds a collapsible row.
func Row(title string) *sdk.RowBuilder {
	return sdk.NewRowBuilder(title)
}

// OrgVariable builds the org template variable used when the org rows
// cannot be declared from the config.
//
// Discovered from repos_tracked rather than from any event counter:
// the gauge is present for every org the exporter knows about,
// including one with zero failures, whereas a counter only exists once
// something has gone wrong. A variable driven by an event counter
// would silently drop exactly the compliant orgs.
func OrgVariable(ds Datasources) *sdk.QueryVariableBuilder {
	return sdk.NewQueryVariableBuilder("org").
		Label("Organisation").
		Datasource(ds.PrometheusRef()).
		Query(sdk.StringOrMap{String: new("label_values(repo_guardian_repos_tracked, org)")}).
		Refresh(sdk.VariableRefreshOnTimeRangeChanged).
		Sort(sdk.VariableSortAlphabeticalAsc).
		Multi(true).
		IncludeAll(true)
}

// New starts a dashboard with the house defaults.
//
// Schema v1, deliberately, even though the SDK flags this builder as
// superseded by dashboardv2.
//
// v2 is not a drop-in here: EVERY panel package in the SDK
// (timeseries, stat, table, gauge, ...) declares
// `var _ cog.Builder[dashboard.Panel]` — they build the v1 Panel type.
// Authoring against dashboardv2 would therefore mean abandoning the
// panel builders and hand-writing v2 element structs, which is exactly
// the hand-maintained dashboard authoring this whole package exists to
// remove. The consumers agree with v1 too: grafana-operator's
// GrafanaDashboard takes classic dashboard JSON, and the static tier in
// contrib/ is imported by hand.
//
// Revisit when the panel packages emit v2 builders, not before. The
// deprecation is forward-looking; v1 dashboards render fine on the
// Grafana >= 13 floor.
//
//nolint:staticcheck // SA1019: v1 is the only schema the panel builders produce, see above
func New(uid, title, description string, tags []string) *sdk.DashboardBuilder {
	return sdk.NewDashboardBuilder(title).
		Uid(uid).
		Description(description).
		Tags(tags).
		Timezone(common.TimeZoneUtc).
		Refresh("1m").
		Time("now-6h", "now")
}

// Render builds a dashboard and marshals it to indented JSON.
//
// Indented and newline-terminated because the output is committed and
// diffed by the CI drift gate: a one-line blob would make every change
// a whole-file diff nobody can review.
//
//nolint:staticcheck // SA1019: see New
func Render(b *sdk.DashboardBuilder) ([]byte, error) {
	d, err := b.Build()
	if err != nil {
		return nil, fmt.Errorf("dashboard: build: %w", err)
	}

	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("dashboard: marshal: %w", err)
	}

	return append(out, '\n'), nil
}
