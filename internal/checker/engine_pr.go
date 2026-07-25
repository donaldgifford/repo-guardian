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

func hasExistingPRForPolicy(openPRs []*ghclient.PullRequest, rule *policy.FileRuleConfig) bool {
	if rule.PR == nil {
		return false
	}

	for _, pr := range openPRs {
		titleLower := strings.ToLower(pr.Title)
		branchLower := strings.ToLower(pr.Head)

		for _, term := range rule.PR.SearchTerms {
			termLower := strings.ToLower(term)
			if strings.Contains(titleLower, termLower) || strings.Contains(branchLower, termLower) {
				return true
			}
		}
	}

	return false
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

	// WARN: This loop syncs file content to the reconcile branch but does not
	//   re-base the branch onto the current default-branch HEAD. If the PR sits
	//   open while default advances, the branch's base SHA goes stale. Two
	//   risks if auto-merge is enabled on the repo-guardian PR:
	//   (1) base drift — usually safe (diff is "add this file") but loses
	//       linear-history aesthetic; (2) content drift — if someone manually
	//       writes a different version of the file to default in the gap, the
	//       file rule's existence check stops flagging it, this loop no-ops,
	//       and a squash-merge of the stale branch overwrites the manual
	//       edit. Mitigations to consider: rebase the branch onto current
	//       default before reconcile, close+reopen PRs older than N days,
	//       or recommend operators don't enable auto-merge on
	//       repo-guardian/* branches. See conversation in PR #71.
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
	orphans := discoverOrphans(ctx, log, client, e.policy.FileRules, actionable, owner, repo)

	var removedOrphans []string

	if len(orphans) > 0 {
		log.Info("cleaning up orphan files", "count", len(orphans))
		removedOrphans = cleanupOrphans(ctx, log, client, orphans, owner, repo)
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

	if rendered.Title == existingPR.Title {
		// Body is not exposed on the PR struct today; conservatively
		// always issue the patch when the title is stable but the
		// actionable set changed. The cost is one PATCH per sweep on
		// touched repos, which fits inside the per-installation rate
		// budget.
		_ = rendered.Body
	}

	if err := client.UpdatePullRequest(ctx, owner, repo, existingPR.Number, rendered.Title, rendered.Body); err != nil {
		return fmt.Errorf("update PR #%d: %w", existingPR.Number, err)
	}

	return nil
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
