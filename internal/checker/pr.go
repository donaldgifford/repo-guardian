package checker

import (
	"fmt"
	"log/slog"

	"github.com/donaldgifford/repo-guardian/internal/policy"
	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

// maxPRBodyChars is the upper bound on the rendered PR body length.
// GitHub accepts up to 65535 chars in the issue/PR body field; we
// truncate at 65000 to leave headroom for the truncation marker that
// gets appended.
const maxPRBodyChars = 65000

// truncationMarker is appended to a body that exceeds maxPRBodyChars.
// %d records the original length so reviewers can tell at a glance how
// much was lost.
const truncationMarker = "\n\n<!-- truncated by repo-guardian: original length=%d chars, max=65535 -->\n"

// renderedPR carries the title, body, and labels for a single PR
// creation call. All fields are post-resolution and post-render — the
// engine passes them straight to the GitHub client.
type renderedPR struct {
	Title  string
	Body   string
	Labels []string
}

// resolveAndRenderRulePR returns the renderedPR for a single firing
// rule. Resolution chain: rule.pr → defaults.pr → engine built-ins
// (fallbackTitle and fallbackBody). The vars context is rendered into
// every compiled template that exists at the resolved scope; an
// engine-built-in fallback is used per-field when the resolved
// PRTemplate has no compiled value for that field.
func (e *Engine) resolveAndRenderRulePR(
	log *slog.Logger,
	rule *policy.FileRuleConfig,
	vars *tmpl.PRVars,
	fallbackTitle, fallbackBody string,
) (renderedPR, error) {
	resolved := policy.ResolveRulePR(rulePRTemplate(rule), defaultsPRTemplate(e.policy))

	return renderResolved(log, resolved, vars, fallbackTitle, fallbackBody)
}

// resolveAndRenderBundlePR returns the renderedPR for a multi-rule
// bundled PR. Title resolution: every actionable rule's resolved title
// is rendered; if all match, that title is used; if any disagree, the
// defaults.pr.title is used (or fallbackTitle when defaults is unset)
// and an slog.Info records the ignored titles. Body always resolves
// from defaults.pr.body only — per-rule pr.body is implicitly
// single-rule (Open Q5). Labels resolve from defaults only for the
// same reason.
func (e *Engine) resolveAndRenderBundlePR(
	log *slog.Logger,
	actionable []policy.FileRuleConfig,
	vars *tmpl.PRVars,
	fallbackTitle, fallbackBody string,
) (renderedPR, error) {
	defaults := defaultsPRTemplate(e.policy)

	title, err := bundleTitle(log, actionable, defaults, vars, fallbackTitle)
	if err != nil {
		return renderedPR{}, err
	}

	body, err := bundleBody(defaults, vars, fallbackBody)
	if err != nil {
		return renderedPR{}, err
	}

	body = truncateBody(log, body, vars)

	var labels []string
	if defaults != nil && defaults.LabelsSet {
		labels = defaults.Labels
	}

	return renderedPR{Title: title, Body: body, Labels: labels}, nil
}

// renderResolved renders a single resolved PRTemplate into a renderedPR,
// substituting fallback strings for any field the resolved template
// does not provide. Truncation is applied to the body unconditionally.
func renderResolved(
	log *slog.Logger,
	resolved *policy.PRTemplate,
	vars *tmpl.PRVars,
	fallbackTitle, fallbackBody string,
) (renderedPR, error) {
	title, body, labels, err := resolvedFields(resolved, vars, fallbackTitle, fallbackBody)
	if err != nil {
		return renderedPR{}, err
	}

	body = truncateBody(log, body, vars)

	return renderedPR{Title: title, Body: body, Labels: labels}, nil
}

// resolvedFields extracts (title, body, labels) from a resolved
// PRTemplate, falling back per-field to the supplied built-ins when
// the resolved template has no compiled value for that field. Split
// out from renderResolved so the renderer's nested branches don't
// trip the nestif linter.
func resolvedFields(
	resolved *policy.PRTemplate,
	vars *tmpl.PRVars,
	fallbackTitle, fallbackBody string,
) (string, string, []string, error) {
	if resolved == nil {
		return fallbackTitle, fallbackBody, nil, nil
	}

	title, err := renderOrFallback(resolved.Title, vars, fallbackTitle, "pr.title")
	if err != nil {
		return "", "", nil, err
	}

	body, err := renderOrFallback(resolved.Body, vars, fallbackBody, "pr.body")
	if err != nil {
		return "", "", nil, err
	}

	var labels []string
	if resolved.LabelsSet {
		labels = resolved.Labels
	}

	return title, body, labels, nil
}

// renderOrFallback renders compiled against vars and returns the
// result; when compiled is nil, fallback is returned verbatim.
// scope is included in error context (e.g. "pr.title", "pr.body").
func renderOrFallback(
	compiled *tmpl.Compiled,
	vars *tmpl.PRVars,
	fallback, scope string,
) (string, error) {
	if compiled == nil {
		return fallback, nil
	}

	rendered, err := compiled.Render(*vars)
	if err != nil {
		return "", fmt.Errorf("rendering %s: %w", scope, err)
	}

	return rendered, nil
}

// bundleTitle returns the rendered title for a multi-rule bundle. When
// every actionable rule's resolved title renders to the same string,
// that string wins. Otherwise, the defaults.pr.title is rendered (or
// fallbackTitle when defaults has no title), and an slog.Info logs the
// per-rule titles that were ignored so operators can spot conflicts in
// production traffic.
func bundleTitle(
	log *slog.Logger,
	actionable []policy.FileRuleConfig,
	defaults *policy.PRTemplate,
	vars *tmpl.PRVars,
	fallbackTitle string,
) (string, error) {
	titles, err := renderRuleTitles(actionable, defaults, vars)
	if err != nil {
		return "", err
	}

	if len(titles) == 0 {
		return defaultsTitle(defaults, vars, fallbackTitle)
	}

	first := titles[0]
	for _, t := range titles[1:] {
		if t != first {
			log.Info("multi-rule PR title conflict; falling back to defaults",
				"ignored_titles", titles)

			return defaultsTitle(defaults, vars, fallbackTitle)
		}
	}

	return first, nil
}

// renderRuleTitles renders each actionable rule's resolved pr.title
// against vars and returns the rendered strings (rules without a
// resolved title are skipped).
func renderRuleTitles(
	actionable []policy.FileRuleConfig,
	defaults *policy.PRTemplate,
	vars *tmpl.PRVars,
) ([]string, error) {
	titles := make([]string, 0, len(actionable))

	for i := range actionable {
		resolved := policy.ResolveRulePR(rulePRTemplate(&actionable[i]), defaults)
		if resolved == nil || resolved.Title == nil {
			continue
		}

		rendered, err := resolved.Title.Render(*vars)
		if err != nil {
			return nil, fmt.Errorf("rendering pr.title for rule %q: %w", actionable[i].Name, err)
		}

		titles = append(titles, rendered)
	}

	return titles, nil
}

// bundleBody renders defaults.pr.body when set; otherwise returns the
// engine built-in fallbackBody. Per-rule pr.body is intentionally
// skipped for bundled PRs (Open Q5 resolution).
func bundleBody(defaults *policy.PRTemplate, vars *tmpl.PRVars, fallbackBody string) (string, error) {
	if defaults == nil || defaults.Body == nil {
		return fallbackBody, nil
	}

	rendered, err := defaults.Body.Render(*vars)
	if err != nil {
		return "", fmt.Errorf("rendering defaults.pr.body: %w", err)
	}

	return rendered, nil
}

// defaultsTitle renders the defaults.pr.title if set; otherwise returns
// the engine built-in fallbackTitle.
func defaultsTitle(defaults *policy.PRTemplate, vars *tmpl.PRVars, fallbackTitle string) (string, error) {
	if defaults == nil || defaults.Title == nil {
		return fallbackTitle, nil
	}

	rendered, err := defaults.Title.Render(*vars)
	if err != nil {
		return "", fmt.Errorf("rendering defaults.pr.title: %w", err)
	}

	return rendered, nil
}

// truncateBody trims body to maxPRBodyChars and appends the truncation
// marker when the body exceeds the limit. An slog.Warn logs the
// original length and the rendering identity so operators can find the
// over-long template. When the body is already under the limit, the
// input is returned unchanged.
func truncateBody(log *slog.Logger, body string, vars *tmpl.PRVars) string {
	if len(body) <= maxPRBodyChars {
		return body
	}

	original := len(body)
	marker := fmt.Sprintf(truncationMarker, original)

	cut := max(maxPRBodyChars-len(marker), 0)
	cut = min(cut, len(body))

	log.Warn("PR body exceeds GitHub limit, truncating",
		"owner", vars.Owner,
		"repo", vars.Repo,
		"original_length", original,
		"max_length", maxPRBodyChars,
	)

	return body[:cut] + marker
}

// rulePRTemplate lifts a FileRuleConfig.PR into the resolved PRTemplate
// form expected by policy.ResolveRulePR. Returns nil when the rule
// declares no pr {} block.
func rulePRTemplate(rule *policy.FileRuleConfig) *policy.PRTemplate {
	if rule == nil || rule.PR == nil {
		return nil
	}

	return prConfigToTemplate(rule.PR)
}

// defaultsPRTemplate lifts the policy's top-level defaults.pr block
// into a PRTemplate for use as the parent scope in resolution.
// Returns nil when no defaults are set.
func defaultsPRTemplate(cfg *policy.PolicyConfig) *policy.PRTemplate {
	if cfg == nil {
		return nil
	}

	return cfg.DefaultsPR()
}

// prConfigToTemplate converts a PRConfig (HCL form) into the resolved
// PRTemplate form. This duplicates policy.asTemplate (which is
// unexported) because the engine needs to call into policy.Resolve*
// with caller-supplied values; pr.go's package-private helper is not
// reachable from this package.
func prConfigToTemplate(pr *policy.PRConfig) *policy.PRTemplate {
	if pr == nil {
		return nil
	}

	inherits := true
	if pr.Inherits != nil {
		inherits = *pr.Inherits
	}

	return &policy.PRTemplate{
		Title:     pr.CompiledTitle,
		Body:      pr.CompiledBody,
		Labels:    pr.Labels,
		LabelsSet: pr.LabelsSet,
		Inherits:  inherits,
	}
}

// buildPRVars constructs the template.PRVars context from the engine's
// known per-job state. Single-rule PRs leave Rules nil and populate
// Rule; bundled multi-rule PRs leave Rule zero-valued and populate the
// Rules slice. files lists every target path included in the PR.
func buildPRVars(owner, repo, defaultBranch, date string, actionable []policy.FileRuleConfig) tmpl.PRVars {
	vars := tmpl.PRVars{
		Common: tmpl.Common{
			Owner:         owner,
			Repo:          repo,
			DefaultBranch: defaultBranch,
			Date:          date,
		},
	}

	files := make([]string, 0, len(actionable))
	rules := make([]tmpl.Rule, 0, len(actionable))

	for i := range actionable {
		files = append(files, actionable[i].Target)
		rules = append(rules, tmpl.Rule{
			Name:   actionable[i].Name,
			Target: actionable[i].Target,
		})
	}

	vars.Files = files

	switch len(actionable) {
	case 0:
		// Leave Rule and Rules zero-valued.
	case 1:
		vars.Rule = rules[0]
	default:
		vars.Rules = rules
	}

	return vars
}
