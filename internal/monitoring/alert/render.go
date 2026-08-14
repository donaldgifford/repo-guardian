package alert

import (
	"bytes"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Rule is one entry in a PrometheusRule group, shaped for YAML.
type Rule struct {
	Alert       string            `yaml:"alert"`
	Expr        string            `yaml:"expr"`
	For         string            `yaml:"for,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// Group is a PrometheusRule rule group.
type Group struct {
	Name  string `yaml:"name"`
	Rules []Rule `yaml:"rules"`
}

// Groups projects specs onto PrometheusRule groups.
//
// Groups appear in first-seen order and rules keep catalogue order, so
// the output is stable without a sort — the catalogue is already
// written in the order it should render, and re-sorting here would let
// a catalogue reordering silently become a byte diff nobody intended.
func Groups(specs []Spec) []Group {
	var (
		out   []Group
		index = make(map[string]int, len(specs))
	)

	for i := range specs {
		s := &specs[i]

		g, ok := index[s.Group]
		if !ok {
			g = len(out)
			index[s.Group] = g

			out = append(out, Group{Name: s.Group})
		}

		out[g].Rules = append(out[g].Rules, s.rule())
	}

	return out
}

// rule projects one spec.
func (s *Spec) rule() Rule {
	r := Rule{
		Alert:  s.Name,
		Expr:   s.Expr,
		Labels: map[string]string{"severity": s.Severity},
		Annotations: map[string]string{
			"summary": s.Summary,
		},
	}

	if s.For > 0 {
		r.For = formatDuration(s.For)
	}

	if s.Description != "" {
		r.Annotations["description"] = s.Description
	}

	return r
}

// formatDuration renders a duration the way Prometheus writes them.
//
// time.Duration.String() produces "1h0m0s" and "30m0s", which Prometheus
// parses but which reads as noise in a manifest a human reviews. The
// durations in this catalogue are all whole hours or whole minutes.
func formatDuration(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	default:
		return d.String()
	}
}

// yamlIndent matches the repo's yamlfmt configuration.
const yamlIndent = 2

// RenderGroups emits the groups as YAML.
func RenderGroups(groups []Group) ([]byte, error) {
	return marshal(map[string]any{"groups": groups})
}

// PrometheusRuleMeta is the identity of a generated PrometheusRule CR.
type PrometheusRuleMeta struct {
	Name      string
	Namespace string
	Labels    map[string]string
}

// RenderPrometheusRule emits a monitoring.coreos.com/v1 PrometheusRule.
//
// Hand-built rather than importing prometheus-operator's API types: the
// module pulls in the whole k8s.io/api and apimachinery tree for four
// fields of a manifest this package fully controls. The CRD's shape is
// stable and the render test pins it.
func RenderPrometheusRule(meta PrometheusRuleMeta, groups []Group) ([]byte, error) {
	if meta.Name == "" {
		return nil, fmt.Errorf("alert: PrometheusRule needs a name")
	}

	metadata := map[string]any{"name": meta.Name}

	// Namespace is stamped explicitly, per the chart-template convention
	// recorded in CLAUDE.md: a rendered manifest without it lands in
	// whatever namespace the applying tool defaults to, which under
	// ArgoCD is frequently the wrong one.
	if meta.Namespace != "" {
		metadata["namespace"] = meta.Namespace
	}

	if len(meta.Labels) > 0 {
		metadata["labels"] = meta.Labels
	}

	return marshal(map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata":   metadata,
		"spec":       map[string]any{"groups": groups},
	})
}

// marshal encodes v with the repo's YAML conventions.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(yamlIndent)

	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("alert: encode yaml: %w", err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("alert: close yaml encoder: %w", err)
	}

	// The document-start marker is required by the repo's chart-testing
	// yamllint configuration, and generated files are linted by the same
	// rules as hand-written ones.
	return append([]byte("---\n"), buf.Bytes()...), nil
}
