package report

import (
	"context"
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

// PRLinker resolves the open repo-guardian PR URL for one repository.
//
// The interface lives here, in the consumer, so internal/report never
// imports internal/github. That is what lets the golden-file tests
// exercise the real rendering path with no client, no credentials and
// no network — the report's format is the thing under test, and a
// GitHub fake would only add fake-parity maintenance for one column.
type PRLinker interface {
	PRURL(ctx context.Context, installationID int64, owner, repo string) (string, error)
}

// Options configures a Renderer.
type Options struct {
	// Links resolves PR URLs. nil omits the PR column entirely.
	Links PRLinker

	// Now stamps the report header. nil means time.Now; the golden
	// tests pin it so output is byte-stable.
	Now func() time.Time

	Logger *slog.Logger
}

// Renderer turns store state into per-org markdown.
type Renderer struct {
	links  PRLinker
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
			"compact": strings.TrimSpace,
		}).
		Option("missingkey=error").
		ParseFS(templateFS, "report.md.tmpl")
	if err != nil {
		return nil, fmt.Errorf("report: parse template: %w", err)
	}

	return &Renderer{links: opts.Links, now: now, logger: logger, tpl: tpl}, nil
}

// Build projects one store read onto the per-org view model.
//
// Pure: no I/O, no clock beyond the injected one, and no re-sorting.
// The SQL already orders findings by (owner, rule, repo) precisely so
// an unchanged database regenerates a byte-identical report; sorting
// again here would be redundant at best and would silently diverge from
// that contract at worst.
func (r *Renderer) Build(data *store.ReportData) []Org {
	generatedAt := r.now()
	previous := indexSnapshots(data.Previous)

	// Rule kinds live on the findings, not on the tallies, so a rule
	// with zero current failures still gets its kind from history when
	// it has one. Absent everywhere, the column is simply blank rather
	// than guessed.
	kinds := make(map[snapshotKey]string)
	for _, f := range data.Findings {
		kinds[snapshotKey{org: f.Owner, rule: f.RuleName}] = f.RuleKind
	}

	orgs := make([]Org, 0)
	index := make(map[string]int)

	// Current defines the org and rule set: it is what was actually
	// evaluated. A rule present only in history has stopped being
	// evaluated, so reporting a percentage for it would describe a
	// measurement nobody took today.
	for _, c := range data.Current {
		i, ok := index[c.Org]
		if !ok {
			i = len(orgs)
			index[c.Org] = i
			orgs = append(orgs, Org{
				Name:        c.Org,
				GeneratedAt: generatedAt,
				ShowLinks:   r.links != nil,
			})
		}

		key := snapshotKey{org: c.Org, rule: c.RuleName}

		line := RuleLine{
			Name:       c.RuleName,
			Kind:       kinds[key],
			Actionable: c.ActionableCount,
			Tracked:    c.TrackedCount,
		}

		if prev, ok := previous[key]; ok {
			line.Delta = c.ActionableCount - prev.ActionableCount
			line.ComparedAt = prev.SnapshotAt
			line.Trend = classifyTrend(line.Delta)
			orgs[i].HasHistory = true
		}

		orgs[i].Rules = append(orgs[i].Rules, line)
	}

	for _, f := range data.Findings {
		i, ok := index[f.Owner]
		if !ok {
			continue
		}

		orgs[i].Findings = append(orgs[i].Findings, Finding{
			Repo:     f.Repo,
			RuleName: f.RuleName,
			RuleKind: f.RuleKind,
			Since:    f.ActionableSince,
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

// Enrich fills in PR links, in place.
//
// Best-effort by design: a lookup failure costs one link, never the
// report. Failures are counted onto the org so the rendered output can
// say the links are incomplete — a silently short list would read as
// "these repositories have no open PR", which is a different and wrong
// statement.
//
// Lookups are per repository, not per finding. A repository failing
// five rules produces five findings and must still cost one API call.
func (r *Renderer) Enrich(ctx context.Context, data *store.ReportData, orgs []Org) {
	if r.links == nil {
		return
	}

	// installationFor lets the linker scope a client without the view
	// model carrying installation IDs into the rendered output.
	installationFor := make(map[string]int64, len(data.Findings))
	for _, f := range data.Findings {
		installationFor[f.Owner+"/"+f.Repo] = f.InstallationID
	}

	for i := range orgs {
		urls := make(map[string]string)

		for j := range orgs[i].Findings {
			repo := orgs[i].Findings[j].Repo

			url, cached := urls[repo]
			if !cached {
				var err error

				url, err = r.links.PRURL(ctx, installationFor[orgs[i].Name+"/"+repo], orgs[i].Name, repo)
				if err != nil {
					r.logger.Warn("report: PR link lookup failed",
						"org", orgs[i].Name, "repo", repo, "error", err)

					orgs[i].LinkFailures++
				}

				urls[repo] = url
			}

			orgs[i].Findings[j].PRURL = url
		}
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
