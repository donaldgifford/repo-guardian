package report

import (
	"embed"
	"fmt"
	"log/slog"
	"strings"
	"text/template"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/store"
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
			"since":   renderSince,
			"trend":   renderTrend,
			"pr":      renderPR,
			"compact": strings.TrimSpace,
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
// Pure: no I/O, no clock beyond the injected one, and no re-sorting.
// The SQL already orders rules by (org, kind, rule) and findings by
// (org, rule, repo) precisely so an unchanged database regenerates a
// byte-identical report.
func (r *Renderer) Build(data *store.ComplianceReport) []Org {
	generatedAt := r.now()
	previous := indexSnapshots(data.Previous)

	orgs := make([]Org, 0)
	index := make(map[string]int)

	// Current defines the org and rule set: it is what was actually
	// evaluated. A rule present only in history has stopped being
	// evaluated, so reporting a percentage for it would describe a
	// measurement nobody took today.
	for i := range data.Current {
		c := &data.Current[i]

		o, ok := index[c.Org]
		if !ok {
			o = len(orgs)
			index[c.Org] = o
			orgs = append(orgs, Org{Name: c.Org, GeneratedAt: generatedAt})
		}

		line := RuleLine{
			Name:          c.RuleName,
			Kind:          string(c.Kind),
			Compliant:     c.Compliant,
			NonCompliant:  c.NonCompliant,
			NotApplicable: c.NotApplicable,
			Unknown:       c.Unknown,
			Percent:       c.Percent,
		}

		if prev, ok := previous[ruleKey{org: c.Org, kind: string(c.Kind), rule: c.RuleName}]; ok {
			line.Delta = c.NonCompliant - prev.NonCompliant
			line.ComparedAt = prev.SnapshotAt
			line.Trend = classifyTrend(line.Delta)
			orgs[o].HasHistory = true
		}

		orgs[o].Rules = append(orgs[o].Rules, line)
	}

	for i := range data.Findings {
		f := &data.Findings[i]

		o, ok := index[f.Org]
		if !ok {
			continue
		}

		since := f.Since

		orgs[o].Findings = append(orgs[o].Findings, Finding{
			Repo:        f.Repo,
			RuleName:    f.RuleName,
			RuleKind:    string(f.Kind),
			Reason:      f.Reason,
			Remediation: f.Remediation,
			Since:       &since,
			PRURL:       f.PRURL,
		})
	}

	return orgs
}

// classifyTrend maps a delta in failing repositories to a direction.
// Fewer failures is an improvement, so the sign is inverted relative to
// the raw count.
func classifyTrend(delta int) TrendState {
	switch {
	case delta < 0:
		return TrendImproved
	case delta > 0:
		return TrendWorsened
	default:
		return TrendFlat
	}
}

// Render renders one org to markdown. Pure and deterministic.
func (r *Renderer) Render(o Org) (string, error) { //nolint:gocritic // value receiver: text/template cannot address a range variable
	var sb strings.Builder

	if err := r.tpl.Execute(&sb, o); err != nil {
		return "", fmt.Errorf("report: render %s: %w", o.Name, err)
	}

	return sb.String(), nil
}
