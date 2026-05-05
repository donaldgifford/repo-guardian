package reconciler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

// maxPRBodyChars is the upper bound on the rendered PR body length
// for reconciler-opened PRs. Mirrors the engine's limit so the same
// truncation marker shape is emitted at both sites.
const maxPRBodyChars = 65000

// truncationMarker is appended when a body exceeds maxPRBodyChars.
const truncationMarker = "\n\n<!-- truncated by repo-guardian: original length=%d chars, max=65535 -->\n"

// renderedPR carries the rendered title, body, and labels for a
// single reconciler-opened PR. All fields are post-resolution and
// post-render — the reconciler passes them straight to the GitHub
// client.
type renderedPR struct {
	Title  string
	Body   string
	Labels []string
}

// resolveReconcilerPR renders the params.PRTemplate (already
// pre-resolved by the engine to (reconciler.pr → defaults.pr)) into
// concrete title/body/labels strings. fallbackTitle and fallbackBody
// are substituted per-field when the template has no compiled value
// at that field — reconcilers always have a working hardcoded
// default to fall through to.
//
// reconcilerName is included in vars.Reconciler so per-reconciler
// branches in template bodies can reference it.
func resolveReconcilerPR(
	log *slog.Logger,
	params *ReconcileParams,
	reconcilerName, fallbackTitle, fallbackBody string,
) (renderedPR, error) {
	vars := tmpl.PRVars{
		Common: tmpl.Common{
			Owner:         params.Owner,
			Repo:          params.Repo,
			DefaultBranch: params.DefaultBranch,
			Date:          time.Now().UTC().Format(time.RFC3339),
		},
		Reconciler: reconcilerName,
	}

	title, err := renderOrFallback(prTitle(params.PRTemplate), &vars, fallbackTitle, "pr.title")
	if err != nil {
		return renderedPR{}, err
	}

	body, err := renderOrFallback(prBody(params.PRTemplate), &vars, fallbackBody, "pr.body")
	if err != nil {
		return renderedPR{}, err
	}

	body = truncateBody(log, body, &vars)

	var labels []string
	if prLabelsSet(params.PRTemplate) {
		labels = params.PRTemplate.Labels
	}

	return renderedPR{Title: title, Body: body, Labels: labels}, nil
}

// applyLabels attaches labels to a freshly created PR. A nil/empty
// labels slice is a no-op. Failures log but do not fail the
// reconciler — labels are best-effort metadata.
func applyLabels(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	prNumber int,
	labels []string,
) {
	if len(labels) == 0 {
		return
	}

	if err := client.AddLabelsToPR(ctx, owner, repo, prNumber, labels); err != nil {
		log.Warn("adding labels to reconciler PR failed; continuing",
			"pr_number", prNumber, "err", err)
	}
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

// truncateBody trims body to maxPRBodyChars and appends the
// truncation marker when the body exceeds the limit.
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

// prTitle returns pt.Title or nil when pt is nil. Avoids a nil
// dereference on callers that don't track which scopes set the field.
func prTitle(pt *policy.PRTemplate) *tmpl.Compiled {
	if pt == nil {
		return nil
	}

	return pt.Title
}

// prBody returns pt.Body or nil when pt is nil.
func prBody(pt *policy.PRTemplate) *tmpl.Compiled {
	if pt == nil {
		return nil
	}

	return pt.Body
}

// prLabelsSet reports whether pt explicitly declared a labels list
// (including the empty-list override). False when pt is nil.
func prLabelsSet(pt *policy.PRTemplate) bool {
	return pt != nil && pt.LabelsSet
}
