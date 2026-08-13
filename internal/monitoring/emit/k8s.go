package emit

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/donaldgifford/repo-guardian/internal/monitoring/dashboard"
)

// The grafana-operator v5 dashboard CR.
const (
	grafanaAPIVersion = "grafana.integreatly.org/v1beta1"
	grafanaKind       = "GrafanaDashboard"
)

// yamlIndent matches the repo's yamlfmt configuration.
const yamlIndent = 2

// renderGrafanaDashboard wraps a dashboard's JSON in a
// GrafanaDashboard CR.
//
// Hand-built rather than importing grafana-operator's API types, for
// the reason alert.RenderPrometheusRule gives: the module drags in the
// whole k8s.io/api and apimachinery tree for a manifest of six fields
// this package fully controls.
//
// spec.datasources is deliberately absent. It exists to remap
// `${DS_X}` import placeholders onto concrete datasources, and the
// panel library bakes concrete UIDs precisely so no placeholder is ever
// emitted (see dashboard.Datasources). Adding the field back would
// reintroduce the prompt-on-import problem the concrete-UID decision
// exists to avoid.
func renderGrafanaDashboard(d *dashboard.Dashboard, dashboardJSON []byte, opts *Options) ([]byte, error) {
	if len(opts.InstanceSelector) == 0 {
		return nil, fmt.Errorf("emit: dashboard %s: no instance selector", d.Slug)
	}

	metadata := map[string]any{"name": d.Slug}

	if opts.Namespace != "" {
		metadata["namespace"] = opts.Namespace
	}

	if len(opts.Labels) > 0 {
		metadata["labels"] = opts.Labels
	}

	spec := map[string]any{
		"instanceSelector": map[string]any{"matchLabels": opts.InstanceSelector},
		// The operator takes the dashboard as a JSON string, not as a
		// nested object.
		"json": string(dashboardJSON),
	}

	if d.Folder != "" {
		spec["folder"] = d.Folder
	}

	if opts.AllowCrossNamespaceImport {
		spec["allowCrossNamespaceImport"] = true
	}

	if opts.ResyncPeriod != "" {
		spec["resyncPeriod"] = opts.ResyncPeriod
	}

	return marshal(map[string]any{
		"apiVersion": grafanaAPIVersion,
		"kind":       grafanaKind,
		"metadata":   metadata,
		"spec":       spec,
	})
}

// marshal encodes v with the repo's YAML conventions.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)

	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("emit: encode yaml: %w", err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("emit: close yaml encoder: %w", err)
	}

	// The document-start marker is required by the repo's yamllint
	// configuration; generated files are linted by the same rules as
	// hand-written ones.
	return append([]byte("---\n"), buf.Bytes()...), nil
}
