package checker

// Branch-protection rule evaluation via the GitHub rulesets API.
// Split out of engine_policy.go in IMPL-0021 (INV-0011 B1); pure
// move, no behavior change.

import (
	"context"
	"fmt"
	"log/slog"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// evaluateBranchProtectionRules checks all branch protection rules against the repository.
func (e *Engine) evaluateBranchProtectionRules(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
) error {
	if len(e.policy.BranchProtectionRules) == 0 {
		return nil
	}

	ownerRepo := owner + "/" + repo
	strict := strictMode(e.policy)

	for i := range e.policy.BranchProtectionRules {
		r := &e.policy.BranchProtectionRules[i]

		if !r.IsEnabled() {
			continue
		}

		ruleLog := log.With("bp_rule", r.Name, "branch", r.Branch)

		if !ruleScopeAllows(r.Scope, owner, strict) {
			ruleLog.Info("branch protection rule out of scope for org, skipping")
			metrics.OutOfScopeTotal.WithLabelValues("rule", owner).Inc()

			continue
		}

		if r.Ignore != nil && r.Ignore.Matches(ownerRepo) {
			ruleLog.Info("repository matched per-rule ignore list, skipping branch protection rule")
			metrics.IgnoredTotal.WithLabelValues("rule", owner).Inc()

			continue
		}

		if err := e.evaluateBranchProtectionRule(ctx, ruleLog, client, owner, repo, r); err != nil {
			return fmt.Errorf("evaluating branch protection rule %q: %w", r.Name, err)
		}
	}

	return nil
}

// evaluateBranchProtectionRule checks a single branch protection rule.
func (e *Engine) evaluateBranchProtectionRule(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	rule *policy.BranchProtectionRuleConfig,
) error {
	metrics.BranchProtectionCheckedTotal.WithLabelValues(rule.Name, owner).Inc()

	// Check if the branch exists.
	sha, err := client.GetBranchSHA(ctx, owner, repo, rule.Branch)
	if err != nil {
		return fmt.Errorf("checking branch %s: %w", rule.Branch, err)
	}

	if sha == "" {
		log.Warn("branch does not exist, skipping branch protection rule")
		return nil
	}

	// Fetch existing rulesets.
	rulesets, err := client.ListRepositoryRulesets(ctx, owner, repo)
	if err != nil {
		return fmt.Errorf("listing rulesets: %w", err)
	}

	// Find a matching ruleset for this branch.
	existing := findMatchingRuleset(rulesets, rule)

	mismatches := compareBranchProtection(existing, rule)
	if len(mismatches) == 0 {
		log.Debug("branch protection matches expected configuration")
		return nil
	}

	log.Info("branch protection mismatch", "mismatches", mismatches)

	if !rule.Remediate {
		return nil
	}

	if e.dryRun {
		log.Info("dry run: would remediate branch protection", "mismatches", mismatches)
		return nil
	}

	desired := buildDesiredRuleset(rule)

	if existing != nil {
		if _, err := client.UpdateRepositoryRuleset(ctx, owner, repo, existing.ID, desired); err != nil {
			return fmt.Errorf("updating ruleset: %w", err)
		}

		log.Info("updated branch protection ruleset")
	} else {
		if _, err := client.CreateRepositoryRuleset(ctx, owner, repo, desired); err != nil {
			return fmt.Errorf("creating ruleset: %w", err)
		}

		log.Info("created branch protection ruleset")
	}

	metrics.BranchProtectionRemediatedTotal.WithLabelValues(rule.Name, owner).Inc()

	return nil
}

// findMatchingRuleset finds a ruleset that targets the same branch pattern.
func findMatchingRuleset(
	rulesets []*ghclient.Ruleset,
	rule *policy.BranchProtectionRuleConfig,
) *ghclient.Ruleset {
	for _, rs := range rulesets {
		if rs.Conditions == nil {
			continue
		}

		for _, pattern := range rs.Conditions.IncludePatterns {
			if pattern == rule.Branch || pattern == "refs/heads/"+rule.Branch {
				return rs
			}
		}
	}

	return nil
}

// compareBranchProtection compares the existing ruleset against the desired config.
// Returns a list of mismatch descriptions.
func compareBranchProtection(
	existing *ghclient.Ruleset,
	rule *policy.BranchProtectionRuleConfig,
) []string {
	if existing == nil {
		if rule.RequirePR || rule.RequireLinearHistory || len(rule.RequireStatusChecks) > 0 {
			return []string{"no matching ruleset found"}
		}

		return nil
	}

	var mismatches []string

	if rule.RequirePR && existing.RequirePullRequest == nil {
		mismatches = append(mismatches, "pull request required but not configured")
	}

	if rule.RequirePR && existing.RequirePullRequest != nil {
		pr := existing.RequirePullRequest

		if pr.RequiredApprovals != rule.RequiredApprovals {
			mismatches = append(mismatches, fmt.Sprintf(
				"required_approvals: got %d, want %d",
				pr.RequiredApprovals, rule.RequiredApprovals,
			))
		}

		if pr.DismissStaleReviews != rule.DismissStaleReviews {
			mismatches = append(mismatches, fmt.Sprintf(
				"dismiss_stale_reviews: got %v, want %v",
				pr.DismissStaleReviews, rule.DismissStaleReviews,
			))
		}
	}

	if rule.RequireLinearHistory != existing.RequireLinearHistory {
		mismatches = append(mismatches, fmt.Sprintf(
			"require_linear_history: got %v, want %v",
			existing.RequireLinearHistory, rule.RequireLinearHistory,
		))
	}

	return mismatches
}

// buildDesiredRuleset creates a Ruleset from the branch protection config.
func buildDesiredRuleset(rule *policy.BranchProtectionRuleConfig) *ghclient.Ruleset {
	rs := &ghclient.Ruleset{
		Name:        "repo-guardian-" + rule.Name,
		Enforcement: "active",
		Target:      "branch",
		Conditions: &ghclient.RulesetConditions{
			IncludePatterns: []string{"refs/heads/" + rule.Branch},
		},
		RequireLinearHistory: rule.RequireLinearHistory,
	}

	if rule.RequirePR {
		rs.RequirePullRequest = &ghclient.RulesetPullRequest{
			RequiredApprovals:   rule.RequiredApprovals,
			DismissStaleReviews: rule.DismissStaleReviews,
		}
	}

	if len(rule.RequireStatusChecks) > 0 {
		rs.RequireStatusChecks = &ghclient.RulesetStatusChecks{
			RequiredChecks:     rule.RequireStatusChecks,
			StrictStatusChecks: true,
		}
	}

	return rs
}
