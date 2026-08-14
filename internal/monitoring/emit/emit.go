// Package emit turns a derived monitoring model into the files
// `repo-guardian monitoring generate` writes. See IMPL-0023 task 5.4.
//
// # Why this package exists at all
//
// It is the one place allowed to see both halves. internal/monitoring
// must not import the Grafana SDK, and internal/monitoring/alert must
// not import internal/monitoring/dashboard — that separation is what
// makes DESIGN-0022 OQ5's escape hatch a directory move rather than an
// untangling. Something still has to compose an alert manifest and a
// pile of dashboards into one output directory, and this is it.
//
// # Pure render, thin writer
//
// Generate does no I/O and Write does nothing else, the same split
// internal/report uses. The CI drift gate (task 5.5) diffs bytes, so
// every guarantee it depends on — determinism, ordering, trailing
// newlines — is testable without a temp directory.
package emit

import (
	"fmt"
	"path"

	"github.com/donaldgifford/repo-guardian/internal/monitoring"
	"github.com/donaldgifford/repo-guardian/internal/monitoring/alert"
	"github.com/donaldgifford/repo-guardian/internal/monitoring/dashboard"
)

// Output formats.
const (
	FormatJSON = "json"
	FormatK8s  = "k8s"
)

// Output layout, mirroring the contrib/ split operators already know.
const (
	dashboardDir = "dashboards"
	alertDir     = "alerts"
)

// DefaultName is the base name for generated Kubernetes objects.
const DefaultName = "repo-guardian"

// Artifact is one file to write, with its path relative to the output
// directory.
type Artifact struct {
	Path    string
	Content []byte
}

// Options configures emission.
type Options struct {
	// Format is FormatJSON or FormatK8s.
	Format string

	// Name is the base name for generated Kubernetes objects. Empty
	// means DefaultName.
	Name string

	// Namespace is stamped on every generated CR.
	//
	// Optional, but strongly advised for the reason CLAUDE.md records
	// from PR #67: a rendered manifest with no namespace lands wherever
	// the applying tool defaults to, and under ArgoCD that is
	// frequently not where the operator intended.
	Namespace string

	// Labels are added to every generated CR's metadata.
	Labels map[string]string

	// InstanceSelector is the GrafanaDashboard spec.instanceSelector
	// matchLabels, naming the Grafana CR the operator should file the
	// dashboards into.
	//
	// Required for FormatK8s when there are dashboards to emit, and the
	// requirement is deliberate: the field is optional to the CRD in
	// the sense that omitting it does not error, but the operator then
	// has no target and the CR sits forever unreconciled with nothing
	// in `kubectl get` to say so. Refusing to render beats emitting
	// something inert — the same call RenderPrometheusRule makes for a
	// missing name.
	InstanceSelector map[string]string

	// AllowCrossNamespaceImport lets the operator file a dashboard into
	// a Grafana running in another namespace, which is the common shape
	// when repo-guardian and Grafana are deployed separately.
	AllowCrossNamespaceImport bool

	// ResyncPeriod re-applies the dashboard periodically so it
	// self-heals against edits made in the Grafana UI. Empty leaves the
	// operator's own default (10m0s).
	ResyncPeriod string
}

// name returns the base object name.
func (o *Options) name() string {
	if o.Name == "" {
		return DefaultName
	}

	return o.Name
}

// Generate renders the model's artifacts.
//
// Determinism is a contract, not an accident: the drift gate compares
// bytes, so nothing here may range over a map to decide an artifact's
// order, path or content. The inputs are already ordered (the alert
// catalogue is written in emit order and dashboard.Suite returns a
// fixed slice) and this function must not undo that.
func Generate(m *monitoring.Model, dashboards []dashboard.Dashboard, opts *Options) ([]Artifact, error) {
	if m == nil {
		return nil, fmt.Errorf("emit: nil model")
	}

	if opts == nil {
		return nil, fmt.Errorf("emit: nil options")
	}

	if opts.Format != FormatJSON && opts.Format != FormatK8s {
		return nil, fmt.Errorf("emit: unknown format %q; want %s or %s", opts.Format, FormatJSON, FormatK8s)
	}

	if err := dashboard.ValidateSuite(dashboards); err != nil {
		return nil, err
	}

	artifacts, err := dashboardArtifacts(dashboards, opts)
	if err != nil {
		return nil, err
	}

	alerts, err := alertArtifact(m, opts)
	if err != nil {
		return nil, err
	}

	return append(artifacts, alerts), nil
}

// dashboardArtifacts renders each dashboard in the requested format.
func dashboardArtifacts(dashboards []dashboard.Dashboard, opts *Options) ([]Artifact, error) {
	if opts.Format == FormatK8s && len(dashboards) > 0 && len(opts.InstanceSelector) == 0 {
		return nil, fmt.Errorf(
			"emit: --format %s needs an instance selector; without one the operator has no Grafana to file the dashboards into",
			FormatK8s)
	}

	out := make([]Artifact, 0, len(dashboards))

	for i := range dashboards {
		d := &dashboards[i]

		body, err := dashboard.Render(d.Builder)
		if err != nil {
			return nil, fmt.Errorf("emit: dashboard %s: %w", d.Slug, err)
		}

		if opts.Format == FormatJSON {
			out = append(out, Artifact{Path: path.Join(dashboardDir, d.Slug+".json"), Content: body})

			continue
		}

		cr, err := renderGrafanaDashboard(d, body, opts)
		if err != nil {
			return nil, err
		}

		out = append(out, Artifact{Path: path.Join(dashboardDir, d.Slug+".yaml"), Content: cr})
	}

	return out, nil
}

// alertArtifact renders the alert manifest.
//
// Consumes alert.Generate's kept set, NEVER alert.Catalogue(). The two
// are a rename apart and confusing them ships every alert regardless of
// whether anything produces its series — which is the INV-0012
// finding-A failure this whole generator exists to prevent.
func alertArtifact(m *monitoring.Model, opts *Options) (Artifact, error) {
	kept, _ := alert.Generate(m)
	groups := alert.Groups(kept)

	if opts.Format == FormatJSON {
		body, err := alert.RenderGroups(groups)
		if err != nil {
			return Artifact{}, fmt.Errorf("emit: %w", err)
		}

		return Artifact{Path: path.Join(alertDir, "rules.yaml"), Content: body}, nil
	}

	body, err := alert.RenderPrometheusRule(alert.PrometheusRuleMeta{
		Name:      opts.name(),
		Namespace: opts.Namespace,
		Labels:    opts.Labels,
	}, groups)
	if err != nil {
		return Artifact{}, fmt.Errorf("emit: %w", err)
	}

	return Artifact{Path: path.Join(alertDir, "prometheusrule.yaml"), Content: body}, nil
}
