package report

import (
	"embed"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"text/template"
	"time"
)

//go:embed report.md.tmpl
var templateFS embed.FS

// Options configures a Renderer.
type Options struct {
	// Now stamps the report header. nil means time.Now; the golden
	// tests pin it so output is byte-stable.
	Now func() time.Time

	Logger *slog.Logger
}

// Renderer turns store state into per-org markdown.
type Renderer struct {
	now    func() time.Time
	logger *slog.Logger
	tpl    *template.Template
}

// New builds a Renderer, parsing the embedded template.
func New(opts Options) (*Renderer, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	tpl, err := template.New("report.md.tmpl").
		Funcs(template.FuncMap{
			"mdcell":  mdcell,
			"pct":     renderPercent,
			"control": renderControl,
		}).
		Option("missingkey=error").
		ParseFS(templateFS, "report.md.tmpl")
	if err != nil {
		return nil, fmt.Errorf("report: parse template: %w", err)
	}

	return &Renderer{now: now, logger: logger, tpl: tpl}, nil
}

// Build projects one store read onto the per-org view model.
//
// Pure: no I/O and no clock beyond the injected one. Rows keep the
// SQL's order (control, then decision; repository, then control), and
// orgs are sorted by name, so an unchanged database regenerates a
// byte-identical report. An org with exclusions but no assigned control
// still gets a report: that it measures nothing is the finding.
func (r *Renderer) Build(data *Data) []Org {
	generatedAt := r.now()
	index := make(map[string]*Org)

	org := func(name string) *Org {
		o, ok := index[name]
		if !ok {
			o = &Org{Name: name, GeneratedAt: generatedAt}
			index[name] = o
		}

		return o
	}

	for i := range data.Posture {
		p := &data.Posture[i]
		line := ControlLine{
			Control: p.ControlID, Decision: p.Decision,
			Compliant: p.Compliant, NonCompliant: p.NonCompliant, Pending: p.Pending, Stale: p.Stale,
			Unmeasurable:  p.UnmeasurableAccessDenied + p.UnmeasurableSuspended + p.UnmeasurableConflict,
			NotApplicable: p.NotApplicable, Unknown: p.Unknown, Excluded: p.Excluded, Assigned: p.Assigned,
			Percent: p.Percent, Coverage: p.Coverage,
		}

		o := org(p.Org)
		if p.Decision == DecisionOrgAdded {
			o.OrgAdded = append(o.OrgAdded, line)
		} else {
			o.Baseline = append(o.Baseline, line)
		}
	}

	for i := range data.Exclusions {
		e := &data.Exclusions[i]
		o := org(e.Org)
		o.Exclusions = append(o.Exclusions, Exclusion{
			Repository: e.Repository, Control: e.ControlID, State: e.State, Reason: e.Reason, ExcludedBy: e.ExcludedBy,
		})
	}

	orgs := make([]Org, 0, len(index))
	for _, o := range index {
		orgs = append(orgs, *o)
	}

	slices.SortFunc(orgs, func(a, b Org) int { return strings.Compare(a.Name, b.Name) })

	return orgs
}

// Render renders one org to markdown. Pure and deterministic.
func (r *Renderer) Render(o Org) (string, error) { //nolint:gocritic // value receiver: text/template cannot address a range variable
	var sb strings.Builder

	if err := r.tpl.Execute(&sb, o); err != nil {
		return "", fmt.Errorf("report: render %s: %w", o.Name, err)
	}

	return sb.String(), nil
}
