package emit_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
	"github.com/donaldgifford/repo-guardian/internal/monitoring/dashboard"
	"github.com/donaldgifford/repo-guardian/internal/monitoring/emit"
)

// sampleDashboards stands in for the Phase 6 suite.
//
// Phase 5 ships the emission plumbing and Phase 6 authors the four real
// dashboards, so the writer is exercised against a synthetic pair here.
// The alternative — waiting for Phase 6 to test the CR wrapping — would
// leave the k8s path unverified through two more phases.
func sampleDashboards(t *testing.T) []dashboard.Dashboard {
	t.Helper()

	ds := dashboard.Datasources{}.WithDefaults()

	return []dashboard.Dashboard{
		{
			Slug:   "rg-first",
			Title:  "First",
			Folder: "repo-guardian",
			Builder: dashboard.New("rg-first", "First", "", nil).
				WithPanel(dashboard.Stat(ds, "Tracked", "", "short", dashboard.Query{
					Expr: `sum(repo_guardian_repos_tracked)`,
				})),
		},
		{
			Slug:  "rg-second",
			Title: "Second",
			Builder: dashboard.New("rg-second", "Second", "", nil).
				WithPanel(dashboard.TimeSeries(ds, "Actionable", "", "short", dashboard.Query{
					Expr: `sum(repo_guardian_repos_actionable)`,
				})),
		},
	}
}

func modelWith(ms ...monitoring.Mechanism) *monitoring.Model {
	set := make(monitoring.Mechanisms, len(ms))
	for _, m := range ms {
		set[m] = struct{}{}
	}

	return &monitoring.Model{Mechanisms: set}
}

func paths(artifacts []emit.Artifact) []string {
	out := make([]string, 0, len(artifacts))
	for i := range artifacts {
		out = append(out, artifacts[i].Path)
	}

	return out
}

func find(t *testing.T, artifacts []emit.Artifact, path string) []byte {
	t.Helper()

	for i := range artifacts {
		if artifacts[i].Path == path {
			return artifacts[i].Content
		}
	}

	t.Fatalf("no artifact at %q; got %v", path, paths(artifacts))

	return nil
}

func k8sOptions() *emit.Options {
	return &emit.Options{
		Format:           emit.FormatK8s,
		Namespace:        "monitoring",
		InstanceSelector: map[string]string{"dashboards": "grafana"},
	}
}

// TestGenerate_JSONLayout pins the plain-file output.
func TestGenerate_JSONLayout(t *testing.T) {
	t.Parallel()

	got, err := emit.Generate(modelWith(), sampleDashboards(t), &emit.Options{Format: emit.FormatJSON})
	if err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}

	want := []string{"dashboards/rg-first.json", "dashboards/rg-second.json", "alerts/rules.yaml"}
	if !slices.Equal(paths(got), want) {
		t.Errorf("paths = %v, want %v", paths(got), want)
	}

	var decoded map[string]any
	if err := json.Unmarshal(find(t, got, "dashboards/rg-first.json"), &decoded); err != nil {
		t.Fatalf("the json-format dashboard is not JSON: %v", err)
	}

	if decoded["uid"] != "rg-first" {
		t.Errorf("uid = %v, want rg-first", decoded["uid"])
	}
}

// TestGenerate_K8sLayout pins the CR output.
func TestGenerate_K8sLayout(t *testing.T) {
	t.Parallel()

	got, err := emit.Generate(modelWith(), sampleDashboards(t), k8sOptions())
	if err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}

	want := []string{"dashboards/rg-first.yaml", "dashboards/rg-second.yaml", "alerts/prometheusrule.yaml"}
	if !slices.Equal(paths(got), want) {
		t.Errorf("paths = %v, want %v", paths(got), want)
	}

	raw := find(t, got, "dashboards/rg-first.yaml")

	var cr struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
		Spec struct {
			JSON             string `yaml:"json"`
			Folder           string `yaml:"folder"`
			InstanceSelector struct {
				MatchLabels map[string]string `yaml:"matchLabels"`
			} `yaml:"instanceSelector"`
		} `yaml:"spec"`
	}

	if err := yaml.Unmarshal(raw, &cr); err != nil {
		t.Fatalf("generated CR is not YAML: %v\n%s", err, raw)
	}

	if cr.APIVersion != "grafana.integreatly.org/v1beta1" {
		t.Errorf("apiVersion = %q, want grafana.integreatly.org/v1beta1", cr.APIVersion)
	}

	if cr.Kind != "GrafanaDashboard" {
		t.Errorf("kind = %q, want GrafanaDashboard", cr.Kind)
	}

	// The slug is metadata.name, which is why ValidateSuite constrains
	// it to RFC 1123 rather than to a filename grammar.
	if cr.Metadata.Name != "rg-first" {
		t.Errorf("metadata.name = %q, want rg-first", cr.Metadata.Name)
	}

	if cr.Metadata.Namespace != "monitoring" {
		t.Errorf("metadata.namespace = %q, want monitoring", cr.Metadata.Namespace)
	}

	if cr.Spec.Folder != "repo-guardian" {
		t.Errorf("spec.folder = %q, want repo-guardian", cr.Spec.Folder)
	}

	if cr.Spec.InstanceSelector.MatchLabels["dashboards"] != "grafana" {
		t.Errorf("spec.instanceSelector.matchLabels = %v, want dashboards=grafana",
			cr.Spec.InstanceSelector.MatchLabels)
	}

	// spec.json is a STRING holding the dashboard, not a nested object.
	var inner map[string]any
	if err := json.Unmarshal([]byte(cr.Spec.JSON), &inner); err != nil {
		t.Fatalf("spec.json does not hold dashboard JSON: %v\n%s", err, cr.Spec.JSON)
	}

	if inner["uid"] != "rg-first" {
		t.Errorf("spec.json uid = %v, want rg-first", inner["uid"])
	}
}

// TestGenerate_K8sOptionalFieldsAreOmittedWhenUnset pins that we do not
// stamp operator defaults we were not asked for.
func TestGenerate_K8sOptionalFieldsAreOmittedWhenUnset(t *testing.T) {
	t.Parallel()

	got, err := emit.Generate(modelWith(), sampleDashboards(t), k8sOptions())
	if err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}

	body := string(find(t, got, "dashboards/rg-second.yaml"))

	for _, absent := range []string{
		"resyncPeriod",              // the operator's own default is 10m0s
		"allowCrossNamespaceImport", // defaults to false
		"folder",                    // rg-second declares none
		// The panel library bakes concrete datasource UIDs precisely so
		// no ${DS_} placeholder is ever emitted, which makes the CR's
		// datasource-remapping field meaningless here. Emitting it
		// anyway would reintroduce the prompt-on-import problem.
		"datasources",
	} {
		if strings.Contains(body, absent+":") {
			t.Errorf("CR carries %s when it was not asked for:\n%s", absent, body)
		}
	}
}

// TestGenerate_K8sHonoursTheOptionalFields pins the other direction.
func TestGenerate_K8sHonoursTheOptionalFields(t *testing.T) {
	t.Parallel()

	opts := k8sOptions()
	opts.AllowCrossNamespaceImport = true
	opts.ResyncPeriod = "5m"
	opts.Labels = map[string]string{"app.kubernetes.io/part-of": "repo-guardian"}

	got, err := emit.Generate(modelWith(), sampleDashboards(t), opts)
	if err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}

	body := string(find(t, got, "dashboards/rg-second.yaml"))

	for _, want := range []string{
		"allowCrossNamespaceImport: true",
		"resyncPeriod: 5m",
		"app.kubernetes.io/part-of: repo-guardian",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("CR is missing %q:\n%s", want, body)
		}
	}
}

// TestGenerate_K8sRefusesWithoutAnInstanceSelector pins the refusal.
//
// The CRD does not require the field in the sense of erroring without
// it — the operator simply has no Grafana to file the dashboard into,
// so the CR sits unreconciled forever with nothing in `kubectl get` to
// say so. Refusing to render beats emitting something inert, which is
// the same call RenderPrometheusRule makes for a missing name.
func TestGenerate_K8sRefusesWithoutAnInstanceSelector(t *testing.T) {
	t.Parallel()

	_, err := emit.Generate(modelWith(), sampleDashboards(t), &emit.Options{Format: emit.FormatK8s})
	if err == nil {
		t.Fatal("Generate() = nil, want an error")
	}

	if !strings.Contains(err.Error(), "instance selector") {
		t.Errorf("error does not name the missing selector: %v", err)
	}
}

// TestGenerate_K8sWithoutDashboardsNeedsNoSelector pins the Phase 5
// state, where dashboard.Suite is still empty.
func TestGenerate_K8sWithoutDashboardsNeedsNoSelector(t *testing.T) {
	t.Parallel()

	got, err := emit.Generate(modelWith(), nil, &emit.Options{Format: emit.FormatK8s})
	if err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}

	if !slices.Equal(paths(got), []string{"alerts/prometheusrule.yaml"}) {
		t.Errorf("paths = %v, want just the alert manifest", paths(got))
	}
}

// TestGenerate_AlertsAreMechanismScoped is the trap this package is
// most likely to fall into.
//
// alert.Generate returns the mechanism-filtered set; alert.Catalogue
// returns everything, and task 5.3's promtool test deliberately renders
// the latter. Wiring Catalogue into the emitted manifest would ship
// PropertySchemaMissing to a deployment with no custom_properties
// reconciler — an alert watching a series nothing produces, which is
// the exact INV-0012 finding-A shape this generator exists to prevent.
func TestGenerate_AlertsAreMechanismScoped(t *testing.T) {
	t.Parallel()

	const gated = "RepoGuardianPropertySchemaMissing"

	without, err := emit.Generate(modelWith(), nil, &emit.Options{Format: emit.FormatJSON})
	if err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}

	if body := string(find(t, without, "alerts/rules.yaml")); strings.Contains(body, gated) {
		t.Errorf("%s shipped with no custom_properties reconciler configured", gated)
	}

	with, err := emit.Generate(
		modelWith(monitoring.MechanismCustomProperties), nil, &emit.Options{Format: emit.FormatJSON})
	if err != nil {
		t.Fatalf("Generate() = %v, want nil", err)
	}

	if body := string(find(t, with, "alerts/rules.yaml")); !strings.Contains(body, gated) {
		t.Errorf("%s was dropped despite a custom_properties reconciler", gated)
	}
}

// TestGenerate_Rejections pins the refusals.
func TestGenerate_Rejections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		model      *monitoring.Model
		dashboards []dashboard.Dashboard
		opts       *emit.Options
		want       string
	}{
		{
			name:  "nil model",
			opts:  &emit.Options{Format: emit.FormatJSON},
			want:  "nil model",
			model: nil,
		},
		{
			name:  "unknown format",
			model: modelWith(),
			opts:  &emit.Options{Format: "yaml"},
			want:  "unknown format",
		},
		{
			name:  "empty format",
			model: modelWith(),
			opts:  &emit.Options{},
			want:  "unknown format",
		},
		{
			name:       "slug that is not a valid object name",
			model:      modelWith(),
			dashboards: []dashboard.Dashboard{{Slug: "RG_First", Builder: dashboard.New("x", "X", "", nil)}},
			opts:       &emit.Options{Format: emit.FormatJSON},
			want:       "not a valid Kubernetes object name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := emit.Generate(tt.model, tt.dashboards, tt.opts)
			if err == nil {
				t.Fatalf("Generate() = nil, want an error containing %q", tt.want)
			}

			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Generate() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

// TestGenerate_IsDeterministic pins byte-stability for the drift gate.
//
// The gate compares bytes, so a map range anywhere in the chain would
// make it flap on nothing — and a flapping gate gets disabled, which
// costs the guarantee entirely.
func TestGenerate_IsDeterministic(t *testing.T) {
	t.Parallel()

	opts := k8sOptions()
	opts.Labels = map[string]string{"a": "1", "b": "2", "c": "3"}
	opts.InstanceSelector = map[string]string{"dashboards": "grafana", "tier": "platform"}

	build := func() []emit.Artifact {
		got, err := emit.Generate(modelWith(monitoring.MechanismFileRules), sampleDashboards(t), opts)
		if err != nil {
			t.Fatalf("Generate() = %v, want nil", err)
		}

		return got
	}

	first := build()

	for i := range 8 {
		got := build()

		if !slices.Equal(paths(got), paths(first)) {
			t.Fatalf("run %d changed the paths: %v vs %v", i, paths(got), paths(first))
		}

		for j := range got {
			if !bytes.Equal(got[j].Content, first[j].Content) {
				t.Fatalf("run %d changed %s:\n%s\n%s", i, got[j].Path, first[j].Content, got[j].Content)
			}
		}
	}
}
