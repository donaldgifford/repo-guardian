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
	result *CheckResult,
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

		actionable, err := e.evaluateBranchProtectionRule(ctx, ruleLog, client, owner, repo, r)
		if err != nil {
			return fmt.Errorf("evaluating branch protection rule %q: %w", r.Name, err)
		}

		result.add(r.Name, RuleKindBranchProtection, actionable)
	}

	return nil
}

// evaluateBranchProtectionRule checks a single branch protection rule
// and reports whether the repo is left non-compliant.
//
// This is branch protection's first actionable verdict of any kind:
// before IMPL-0023 it emitted `branch_protection_checked_total` and
// `..._remediated_total` but nothing that said "this repo's protection
// is wrong and still wrong" (INV-0013 Finding B). Remediation semantics
// match setting rules — a mismatch fixed in this pass is not
// actionable; one left standing is.
//
// A missing target branch returns false rather than true: the rule
// cannot be satisfied *or* violated on a branch that does not exist,
// and reporting it as failing would make every repo without a `develop`
// branch look non-compliant with a `develop` protection rule.
func (e *Engine) evaluateBranchProtectionRule(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	rule *policy.BranchProtectionRuleConfig,
) (bool, error) {
	metrics.BranchProtectionCheckedTotal.WithLabelValues(rule.Name, owner).Inc()

	// Check if the branch exists.
	sha, err := client.GetBranchSHA(ctx, owner, repo, rule.Branch)
	if err != nil {
		return false, fmt.Errorf("checking branch %s: %w", rule.Branch, err)
	}

	if sha == "" {
		log.Warn("branch does not exist, skipping branch protection rule")
		return false, nil
	}

	// Fetch existing rulesets.
	rulesets, err := client.ListRepositoryRulesets(ctx, owner, repo)
	if err != nil {
		return false, fmt.Errorf("listing rulesets: %w", err)
	}

	// Find a matching ruleset for this branch.
	existing := findMatchingRuleset(rulesets, rule)

	mismatches := compareBranchProtection(existing, rule)
	if len(mismatches) == 0 {
		log.Debug("branch protection matches expected configuration")
		return false, nil
	}

	log.Info("branch protection mismatch", "mismatches", mismatches)

	if !rule.Remediate {
		return true, nil
	}

	if e.dryRun {
		log.Info("dry run: would remediate branch protection", "mismatches", mismatches)
		return true, nil
	}

	desired := buildDesiredRuleset(rule)

	if existing != nil {
		if _, err := client.UpdateRepositoryRuleset(ctx, owner, repo, existing.ID, desired); err != nil {
			return false, fmt.Errorf("updating ruleset: %w", err)
		}

		log.Info("updated branch protection ruleset")
	} else {
		if _, err := client.CreateRepositoryRuleset(ctx, owner, repo, desired); err != nil {
			return false, fmt.Errorf("creating ruleset: %w", err)
		}

		log.Info("created branch protection ruleset")
	}

	metrics.BranchProtectionRemediatedTotal.WithLabelValues(rule.Name, owner).Inc()

	return false, nil
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
