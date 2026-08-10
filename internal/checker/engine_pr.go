package checker

// Pull-request lifecycle for policy-driven file rules: discovery of an
// existing repo-guardian PR, syncing actionable files onto the
// reconcile branch, and creating or refreshing the PR itself. Split
// out of engine_policy.go in IMPL-0021 (INV-0011 B1); pure move, no
// behavior change.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

// foreignPRForRule returns the open pull request that already appears
// to be addressing this rule together with the search term that matched
// it, or (nil, "") when no open PR does.
//
// repo-guardian's own reconcile PR is deliberately excluded (INV-0011
// B4). It is already handled by the converge path — the PR is refreshed
// while rules stay actionable and auto-closed once they are all
// satisfied — and letting it match here breaks that in two ways:
//
//   - Self-suppression. IMPL-0012 lets an operator set a per-rule
//     `pr.title`, so a rule whose title naturally contains its own
//     search term ("chore: add CODEOWNERS" vs `["codeowners"]`) matches
//     the very PR it opened. The rule stops being actionable, the
//     actionable set empties, the PR auto-closes, and the next sweep
//     re-opens it — one oscillation per sweep, forever.
//   - Era collision. An absent rule forbidding a file that an earlier
//     add-era rule installed shares vocabulary with the add-era PR
//     title by construction; DESIGN-0020 could only warn operators to
//     hand-pick non-colliding terms. Both PRs are ours, so skipping
//     ours removes the hazard structurally instead of by documentation.
//
// Matching against third-party PRs stays deliberately loose
// (case-insensitive substring over title and head branch): the point is
// to yield to a human who is already doing the work, and a human's
// branch naming is not something we can match precisely.
func foreignPRForRule(
	openPRs []*ghclient.PullRequest,
	rule *policy.FileRuleConfig,
) (*ghclient.PullRequest, string) {
	if rule.PR == nil {
		return nil, ""
	}

	for _, pr := range openPRs {
		if pr.Head == BranchName {
			continue
		}

		titleLower := strings.ToLower(pr.Title)
		branchLower := strings.ToLower(pr.Head)

		for _, term := range rule.PR.SearchTerms {
			// An empty term substring-matches every PR, which would
			// disable the rule org-wide with no signal. Policy
			// validation rejects it at load; this is defense in depth
			// for rules built in Go rather than parsed from HCL.
			if term == "" {
				continue
			}

			termLower := strings.ToLower(term)
			if strings.Contains(titleLower, termLower) || strings.Contains(branchLower, termLower) {
				return pr, term
			}
		}
	}

	return nil, ""
}

// updateReconcileBranch merges the default branch into an open PR's
// reconcile branch, best-effort.
//
// Failure is never fatal: the branch may legitimately conflict with
// base (a human edited the same file on the reconcile branch), and
// refusing to reconcile over that would be worse than leaving the
// branch behind — the sync below still runs, and the next sweep
// retries. Mirrors the IMPL-0013 Q9 fail-safe stance.
func updateReconcileBranch(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	number int,
) {
	if err := client.UpdatePRBranch(ctx, owner, repo, number); err != nil {
		log.Warn("could not update reconcile branch from default; continuing with the branch as-is",
			"pr_number", number,
			"error", err,
		)
	}
}

// createOrUpdatePRFromPolicy creates or updates a PR for actionable policy rules.
func (e *Engine) createOrUpdatePRFromPolicy(
	ctx context.Context,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	actionable []policy.FileRuleConfig,
	openPRs []*ghclient.PullRequest,
	gate *gateEvaluator,
) error {
	log := e.logger.With("owner", owner, "repo", repo)

	branchSHA, err := client.GetBranchSHA(ctx, owner, repo, BranchName)
	if err != nil {
		return fmt.Errorf("checking for existing branch: %w", err)
	}

	existingPR := findOurPR(openPRs)

	if branchSHA != "" && existingPR == nil {
		log.Info("deleting stale branch from previously closed PR")

		if err := client.DeleteBranch(ctx, owner, repo, BranchName); err != nil {
			return fmt.Errorf("deleting stale branch: %w", err)
		}

		branchSHA = ""
	}

	baseSHA, err := client.GetBranchSHA(ctx, owner, repo, defaultBranch)
	if err != nil {
		return fmt.Errorf("getting default branch SHA: %w", err)
	}

	if baseSHA == "" {
		return fmt.Errorf("default branch %s has no SHA", defaultBranch)
	}

	if branchSHA == "" {
		if err := client.CreateBranch(ctx, owner, repo, BranchName, baseSHA); err != nil {
			return fmt.Errorf("creating branch: %w", err)
		}

		log.Info("created branch", "branch", BranchName)
	}

	// Bring an already-open PR's branch up to date with the default
	// branch before syncing files onto it (INV-0011 B4, PR #71).
	//
	// A branch that sits open while default advances goes stale in two
	// ways: its base SHA drifts, and — the dangerous one — if someone
	// writes a different version of the same file to default in the
	// gap, the rule stops being actionable, the sync below no-ops, and
	// a squash-merge of the stale branch silently reverts the manual
	// edit. Merging default in first means the sync always operates on
	// current content, so it can only add what is genuinely still
	// missing.
	if existingPR != nil {
		updateReconcileBranch(ctx, log, client, owner, repo, existingPR.Number)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	if err := e.syncActionableFiles(ctx, log, client, owner, repo, defaultBranch, now, actionable); err != nil {
		return err
	}

	if existingPR == nil {
		return e.createNewPolicyPR(ctx, log, client, owner, repo, defaultBranch, now, actionable)
	}

	// IMPL-0013 Phase 3: existing PR is being updated. Remove orphan
	// files (rules that were in a previous version of the PR but are
	// now satisfied on the default branch) and refresh the PR body.
	var removedOrphans []string

	// INV-0014 kill switch: orphan cleanup is the only path that deletes
	// files, so it is the only one with an off switch. Gated before
	// discovery, not before cleanup, so disabling it also stops the
	// default-branch probes it would otherwise issue.
	if e.policy.Guardian.OrphanCleanupEnabled() {
		orphans := discoverOrphans(ctx, log, client, e.policy.FileRules, actionable, owner, repo)

		if len(orphans) > 0 {
			log.Info("cleaning up orphan files", "count", len(orphans))
			removedOrphans = cleanupOrphans(ctx, log, client, orphans, owner, repo)
		}
	}

	// IMPL-0019 Phase 2: inverse orphans — absent rules that stopped being
	// actionable and whose forbidden file the branch deleted must be
	// restored so the PR stops proposing the deletion.
	restoredRules := restoreInverseOrphans(ctx, log, client, e.policy.FileRules, actionable, owner, repo)

	if err := e.refreshPolicyPR(ctx, log, client, owner, repo, defaultBranch, existingPR, actionable, now); err != nil {
		log.Warn("PR body refresh failed; PR text may be stale until next sweep", "err", err)
	}

	// IMPL-0013 Phase 4: sticky reconcile-log comment with per-rule
	// status. Best-effort — failures don't abort the sweep.
	events := buildReconcileLogEvents(e.policy.FileRules, actionable, removedOrphans, restoredRules, gate)
	upsertReconcileLog(ctx, log, client, owner, repo, existingPR, events)

	metrics.PRsUpdatedTotal.WithLabelValues(owner).Inc()
	log.Info("updated existing PR", "pr_number", existingPR.Number)

	return nil
}

// syncActionableFiles renders and commits the template content for
// every actionable rule. Extracted from createOrUpdatePRFromPolicy
// to keep cyclomatic complexity under the gocyclo threshold.
func (e *Engine) syncActionableFiles(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo, defaultBranch, now string,
	actionable []policy.FileRuleConfig,
) error {
	for i := range actionable {
		r := &actionable[i]

		if r.CheckMode() == policy.CheckAbsent {
			if err := e.removeForbiddenFiles(ctx, log, client, owner, repo, r); err != nil {
				return err
			}

			continue
		}

		compiled, err := e.templates.Get(r.Template)
		if err != nil {
			return fmt.Errorf("getting template for %s: %w", r.Name, err)
		}

		content, err := compiled.Render(tmpl.FileVars{
			Common: tmpl.Common{
				Owner:         owner,
				Repo:          repo,
				DefaultBranch: defaultBranch,
				Date:          now,
			},
			Rule: tmpl.Rule{Name: r.Name, Target: r.Target},
			Org:  owner,
		})
		if err != nil {
			return fmt.Errorf("rendering template for %s: %w", r.Name, err)
		}

		msg := fmt.Sprintf("chore: add %s", r.Target)

		if err := client.CreateOrUpdateFile(ctx, owner, repo, BranchName, r.Target, content, msg); err != nil {
			return fmt.Errorf("creating file %s: %w", r.Target, err)
		}

		log.Info("added file", "path", r.Target)
	}

	return nil
}

// removeForbiddenFiles deletes every path of an absent-mode rule that is
// present on the reconcile branch, mirroring the INV-0003 three-branch
// idempotency contract: probe the reconcile branch first, delete with the
// blob SHA when present, skip when already absent so a second sweep is a
// no-op. A path that is already gone is not an error — the branch has
// converged for that path (IMPL-0019 Phase 2 task 2.1).
func (*Engine) removeForbiddenFiles(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	r *policy.FileRuleConfig,
) error {
	for _, path := range r.Paths {
		sha, exists, err := client.GetContentsOnBranch(ctx, owner, repo, path, BranchName)
		if err != nil {
			return fmt.Errorf("checking %s on branch for absent rule %q: %w", path, r.Name, err)
		}

		if !exists {
			log.Debug("forbidden file already absent on branch", "path", path, "rule", r.Name)

			continue
		}

		msg := fmt.Sprintf("chore: remove %s (forbidden by rule %q)", path, r.Name)

		if err := client.DeleteFile(ctx, owner, repo, BranchName, path, sha, msg); err != nil {
			return fmt.Errorf("deleting %s for absent rule %q: %w", path, r.Name, err)
		}

		log.Info("removed forbidden file", "path", path, "rule", r.Name)
	}

	return nil
}

// refreshPolicyPR re-renders the title and body for the current
// actionable set and updates the PR if either drifted from the
// in-flight values. No-op when both match — avoids API churn on
// reconcile passes where nothing changed.
func (e *Engine) refreshPolicyPR(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo, defaultBranch string,
	existingPR *ghclient.PullRequest,
	actionable []policy.FileRuleConfig,
	now string,
) error {
	fallbackBody := buildPRBodyFromPolicy(actionable)
	vars := buildPRVars(owner, repo, defaultBranch, now, actionable)

	var (
		rendered renderedPR
		err      error
	)

	switch len(actionable) {
	case 1:
		rendered, err = e.resolveAndRenderRulePR(log, &actionable[0], &vars, PRTitle, fallbackBody)
	default:
		rendered, err = e.resolveAndRenderBundlePR(log, actionable, &vars, PRTitle, fallbackBody)
	}

	if err != nil {
		return fmt.Errorf("resolving PR template: %w", err)
	}

	if rendered.Title == existingPR.Title && sameBody(rendered.Body, existingPR.Body) {
		log.Debug("PR text unchanged; skipping refresh", "pr_number", existingPR.Number)

		return nil
	}

	if err := client.UpdatePullRequest(ctx, owner, repo, existingPR.Number, rendered.Title, rendered.Body); err != nil {
		return fmt.Errorf("update PR #%d: %w", existingPR.Number, err)
	}

	return nil
}

// sameBody reports whether two PR bodies are equal ignoring line-ending
// style. GitHub stores a body submitted through the web UI with CRLF
// while everything repo-guardian writes uses LF; without the
// normalization a body a human opened and re-saved unchanged would look
// different forever and draw a PATCH on every single sweep.
//
// Note this comparison only suppresses churn for bodies that render
// deterministically. A custom `defaults.pr.body` that interpolates
// `{{ .Date }}` re-renders differently every sweep and will keep
// patching — which is the pre-IMPL-0021 behavior, not a regression.
func sameBody(rendered, existing string) bool {
	return strings.ReplaceAll(rendered, "\r\n", "\n") == strings.ReplaceAll(existing, "\r\n", "\n")
}

// createNewPolicyPR resolves PR templates, renders title/body/labels,
// creates the PR, and applies labels. Extracted from
// createOrUpdatePRFromPolicy to keep cyclomatic complexity in check.
func (e *Engine) createNewPolicyPR(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo, defaultBranch, now string,
	actionable []policy.FileRuleConfig,
) error {
	fallbackBody := buildPRBodyFromPolicy(actionable)
	vars := buildPRVars(owner, repo, defaultBranch, now, actionable)

	var (
		rendered renderedPR
		err      error
	)

	switch len(actionable) {
	case 1:
		rendered, err = e.resolveAndRenderRulePR(log, &actionable[0], &vars, PRTitle, fallbackBody)
	default:
		rendered, err = e.resolveAndRenderBundlePR(log, actionable, &vars, PRTitle, fallbackBody)
	}

	if err != nil {
		return fmt.Errorf("resolving PR template: %w", err)
	}

	pr, err := client.CreatePullRequest(ctx, owner, repo, rendered.Title, rendered.Body, BranchName, defaultBranch)
	if err != nil {
		return fmt.Errorf("creating PR: %w", err)
	}

	if err := client.AddLabelsToPR(ctx, owner, repo, pr.Number, rendered.Labels); err != nil {
		log.Warn("adding labels to PR failed; continuing", "pr_number", pr.Number, "err", err)
	}

	metrics.PRsCreatedTotal.WithLabelValues(owner).Inc()
	log.Info("created PR", "pr_number", pr.Number)

	return nil
}

func buildPRBodyFromPolicy(actionable []policy.FileRuleConfig) string {
	var added, removed []policy.FileRuleConfig

	for i := range actionable {
		if actionable[i].CheckMode() == policy.CheckAbsent {
			removed = append(removed, actionable[i])
		} else {
			added = append(added, actionable[i])
		}
	}

	var sb strings.Builder

	sb.WriteString("## Repo Guardian — Configuration Drift\n\n")
	sb.WriteString("This PR was automatically created by **repo-guardian** to bring this\n")
	sb.WriteString("repository into line with the organization's file policy.\n\n")

	if len(added) > 0 {
		sb.WriteString("### Added Files\n\n")

		for i := range added {
			fmt.Fprintf(&sb, "- `%s` — %s\n", added[i].Target, added[i].Name)
		}

		// The CODEOWNERS placeholder note is only relevant when a file is
		// being added; a removal-only PR must not mention it.
		sb.WriteString("\n> **Note:** The CODEOWNERS file contains a placeholder (`@org/CHANGEME`).\n")
		sb.WriteString("> Please replace it with your actual team before merging.\n\n")
	}

	if len(removed) > 0 {
		sb.WriteString("### Removed Files\n\n")

		for i := range removed {
			for _, path := range removed[i].Paths {
				fmt.Fprintf(&sb, "- `%s` — %s\n", path, removed[i].Name)
			}
		}

		sb.WriteString("\n")
	}

	sb.WriteString("### What to do\n\n")
	sb.WriteString("1. Review the changes and adjust for your team's needs.\n")
	sb.WriteString("2. Merge when ready — these are sensible defaults, not one-size-fits-all.\n\n")
	sb.WriteString("---\n")
	sb.WriteString("*Automated by [repo-guardian](https://github.com/apps/repo-guardian). ")
	sb.WriteString("Questions? Reach out in #platform-engineering.*\n")

	return sb.String()
}
