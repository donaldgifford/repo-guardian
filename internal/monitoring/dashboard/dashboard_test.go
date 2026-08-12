package dashboard_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/monitoring/dashboard"
)

// renderSample builds a dashboard exercising every panel type in the
// library and returns its JSON, decoded.
func renderSample(t *testing.T, ds dashboard.Datasources) map[string]any {
	t.Helper()

	b := dashboard.New("rg-test", "Test", "A test dashboard", []string{"repo-guardian"}).
		WithVariable(dashboard.OrgVariable(ds)).
		WithRow(dashboard.Row("Compliance")).
		WithPanel(dashboard.Stat(ds, "Tracked", "Repositories tracked", "short", dashboard.Query{
			Expr: `sum(repo_guardian_repos_tracked)`,
		})).
		WithPanel(dashboard.TimeSeries(ds, "Actionable", "Repositories failing each rule", "short", dashboard.Query{
			Expr:   `sum by (rule_name) (repo_guardian_repos_actionable)`,
			Legend: "{{ rule_name }}",
		})).
		WithPanel(dashboard.Table(ds, "Open PRs", "Open PRs by rule", dashboard.Query{
			Expr: `repo_guardian_open_prs_by_rule`,
		}))

	raw, err := dashboard.Render(b)
	if err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("generated dashboard is not valid JSON: %v\n%s", err, raw)
	}

	return out
}

// TestRender_ProducesAValidDashboard is the schema render test.
//
// The SDK's Build() is where schema violations surface — a builder can
// accept a value the schema rejects, and the error appears only at
// Build. Rendering every panel type the library offers is what makes an
// SDK bump a compile-or-test failure rather than a dashboard that
// imports and shows nothing.
func TestRender_ProducesAValidDashboard(t *testing.T) {
	t.Parallel()

	got := renderSample(t, dashboard.Datasources{}.WithDefaults())

	for _, key := range []string{"uid", "title", "panels", "templating", "schemaVersion"} {
		if _, ok := got[key]; !ok {
			t.Errorf("rendered dashboard has no %q key; keys are %v", key, mapKeys(got))
		}
	}

	if got["uid"] != "rg-test" {
		t.Errorf("uid = %v, want rg-test", got["uid"])
	}

	// One row + three panels. A row is itself a panel in schema v1, so a
	// count below four means a builder silently dropped one.
	panels, ok := got["panels"].([]any)
	if !ok {
		t.Fatalf("panels is %T, want a list", got["panels"])
	}

	if len(panels) != 4 {
		t.Errorf("panels = %d, want 4 (one row plus three panels)", len(panels))
	}
}

// TestRender_UsesConcreteDatasourceUIDs pins the resolved-datasource
// decision (IMPL-0023 OQ4).
//
// Never the `${DS_PROMETHEUS}` input placeholder. A dashboard carrying
// an input prompts the importer to pick a datasource, which makes the
// generated tier un-provisionable: grafana-operator applies a CR and
// there is nobody there to answer the prompt. The panels would land
// with no datasource and render empty — indistinguishable from a fleet
// with no data.
func TestRender_UsesConcreteDatasourceUIDs(t *testing.T) {
	t.Parallel()

	b := dashboard.New("rg-test", "Test", "", nil).
		WithPanel(dashboard.Stat(dashboard.Datasources{}.WithDefaults(), "Tracked", "", "short", dashboard.Query{
			Expr: `sum(repo_guardian_repos_tracked)`,
		}))

	raw, err := dashboard.Render(b)
	if err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}

	body := string(raw)

	if strings.Contains(body, "${DS_") {
		t.Errorf("rendered dashboard carries a datasource input placeholder:\n%s", body)
	}

	if !strings.Contains(body, `"uid": "prometheus"`) {
		t.Errorf("rendered dashboard does not name the prometheus datasource:\n%s", body)
	}

	if strings.Contains(body, `"__inputs"`) {
		t.Errorf("rendered dashboard declares __inputs, which prompts on import:\n%s", body)
	}
}

// TestDatasources_WithDefaults pins that an override survives and an
// empty falls back.
func TestDatasources_WithDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		in             dashboard.Datasources
		wantPrometheus string
		wantLoki       string
	}{
		{
			name:           "empty takes the defaults",
			in:             dashboard.Datasources{},
			wantPrometheus: dashboard.DefaultPrometheusUID,
			wantLoki:       dashboard.DefaultLokiUID,
		},
		{
			name:           "overrides survive",
			in:             dashboard.Datasources{Prometheus: "mimir", Loki: "logs"},
			wantPrometheus: "mimir",
			wantLoki:       "logs",
		},
		{
			name:           "one override does not clobber the other",
			in:             dashboard.Datasources{Prometheus: "mimir"},
			wantPrometheus: "mimir",
			wantLoki:       dashboard.DefaultLokiUID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.in.WithDefaults()
			if got.Prometheus != tt.wantPrometheus {
				t.Errorf("Prometheus = %q, want %q", got.Prometheus, tt.wantPrometheus)
			}

			if got.Loki != tt.wantLoki {
				t.Errorf("Loki = %q, want %q", got.Loki, tt.wantLoki)
			}
		})
	}
}

// TestRender_HonoursACustomPrometheusUID pins that the override reaches
// the panels, not just the struct.
func TestRender_HonoursACustomPrometheusUID(t *testing.T) {
	t.Parallel()

	got := renderSample(t, dashboard.Datasources{Prometheus: "mimir"}.WithDefaults())

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal() = %v, want nil", err)
	}

	body := string(raw)

	// The datasource TYPE is the literal string "prometheus" on every
	// panel, so presence of that word proves nothing. What matters is
	// the uid: "mimir" must appear, and the default uid must not.
	if !strings.Contains(body, `"uid":"mimir"`) {
		t.Errorf("panels do not carry the overridden datasource uid:\n%s", body)
	}

	if strings.Contains(body, `"uid":"prometheus"`) {
		t.Errorf("panels still point at the default datasource uid:\n%s", body)
	}
}

// TestRender_PanelsShowNoDataRatherThanZero pins the empty-series
// display.
//
// A zero is a measurement; an absent series is the absence of one.
// Rendering the second as the first is how a dashboard reports a
// healthy fleet while the exporter is dead — the same failure the
// report's "n/a" for an unmeasured rule exists to prevent.
func TestRender_PanelsShowNoDataRatherThanZero(t *testing.T) {
	t.Parallel()

	got := renderSample(t, dashboard.Datasources{}.WithDefaults())

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal() = %v, want nil", err)
	}

	if !strings.Contains(string(raw), `"noValue":"no data"`) {
		t.Errorf("no panel sets noValue; an empty series would render as blank or zero:\n%s", raw)
	}
}

// TestRender_IsDeterministic pins byte-stability across renders.
//
// The static tier is committed and gated on a byte diff, so a map range
// anywhere in the builder chain would make the gate flap on nothing.
func TestRender_IsDeterministic(t *testing.T) {
	t.Parallel()

	ds := dashboard.Datasources{}.WithDefaults()

	build := func() []byte {
		b := dashboard.New("rg-test", "Test", "", []string{"a", "b"}).
			WithVariable(dashboard.OrgVariable(ds)).
			WithPanel(dashboard.TimeSeries(ds, "T", "", "short", dashboard.Query{Expr: `up`}))

		raw, err := dashboard.Render(b)
		if err != nil {
			t.Fatalf("Render() = %v, want nil", err)
		}

		return raw
	}

	first := build()

	for i := range 8 {
		if got := build(); !bytes.Equal(got, first) {
			t.Fatalf("render %d differs from the first:\n%s\n%s", i, first, got)
		}
	}
}

// TestRender_EndsWithANewline pins the trailing byte.
//
// Committed artifacts without one produce a "\ No newline at end of
// file" marker in every diff, and some tooling rewrites it back —
// which the drift gate would then report as drift.
func TestRender_EndsWithANewline(t *testing.T) {
	t.Parallel()

	raw, err := dashboard.Render(dashboard.New("rg-test", "Test", "", nil))
	if err != nil {
		t.Fatalf("Render() = %v, want nil", err)
	}

	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Error("rendered dashboard does not end with a newline")
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
