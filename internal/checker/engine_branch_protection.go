package checker

// Branch-protection rule evaluation via the GitHub rulesets API.
// Split out of engine_policy.go in IMPL-0021 (INV-0011 B1); pure
// move, no behavior change.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/donaldgifford/repo-guardian/internal/findings"
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
			recordRule(result, r.Name, RuleKindBranchProtection, notApplicable(findings.OutOfScopeRuleEvidence{}))

			continue
		}

		if pattern, ignored := r.Ignore.MatchPattern(owner, repo); ignored {
			ruleLog.Info("repository matched per-rule ignore list, skipping branch protection rule")
			metrics.IgnoredTotal.WithLabelValues("rule", owner).Inc()
			recordRule(result, r.Name, RuleKindBranchProtection, notApplicable(findings.IgnoredRuleEvidence{Pattern: pattern}))

			continue
		}

		detail, err := e.evaluateBranchProtectionRule(ctx, ruleLog, client, owner, repo, r)
		if err != nil {
			return fmt.Errorf("evaluating branch protection rule %q: %w", r.Name, err)
		}

		recordRule(result, r.Name, RuleKindBranchProtection, detail)
	}

	return nil
}

// evaluateBranchProtectionRule checks a single branch protection rule
// and returns the rule's outcome detail.
//
// This is branch protection's first actionable verdict of any kind:
// before IMPL-0023 it emitted `branch_protection_checked_total` and
// `..._remediated_total` but nothing that said "this repo's protection
// is wrong and still wrong" (INV-0013 Finding B). Remediation semantics
// match setting rules — a mismatch fixed in this pass is not
// actionable; one left standing is.
//
// A missing target branch is not_applicable / branch_missing rather than
// non-compliant: the rule cannot be satisfied *or* violated on a branch
// that does not exist, and reporting it as failing would make every repo
// without a `develop` branch look non-compliant with a `develop`
// protection rule.
func (e *Engine) evaluateBranchProtectionRule(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	rule *policy.BranchProtectionRuleConfig,
) (outcomeDetail, error) {
	metrics.BranchProtectionCheckedTotal.WithLabelValues(rule.Name, owner).Inc()

	// Check if the branch exists.
	sha, err := client.GetBranchSHA(ctx, owner, repo, rule.Branch)
	if err != nil {
		return outcomeDetail{}, fmt.Errorf("checking branch %s: %w", rule.Branch, err)
	}

	if sha == "" {
		log.Warn("branch does not exist, skipping branch protection rule")
		return notApplicable(findings.BranchMissingEvidence{Branch: rule.Branch}), nil
	}

	// Fetch existing rulesets.
	rulesets, err := client.ListRepositoryRulesets(ctx, owner, repo)
	if err != nil {
		return outcomeDetail{}, fmt.Errorf("listing rulesets: %w", err)
	}

	// Find a matching ruleset for this branch.
	existing := findMatchingRuleset(rulesets, rule)

	mismatches := compareBranchProtection(existing, rule)
	if len(mismatches) == 0 {
		log.Debug("branch protection matches expected configuration")
		return compliantDetail(), nil
	}

	log.Info("branch protection mismatch", "mismatches", mismatches)

	detail := rulesetMismatchDetail(rule.Branch, existing, mismatches)

	if !rule.Remediate {
		detail.remediation = findings.RemediationDisabled

		return detail, nil
	}

	if e.dryRun {
		log.Info("dry run: would remediate branch protection", "mismatches", mismatches)

		detail.remediation = findings.RemediationDryRun

		return detail, nil
	}

	desired := buildDesiredRuleset(rule)

	if existing != nil {
		if _, err := client.UpdateRepositoryRuleset(ctx, owner, repo, existing.ID, desired); err != nil {
			return outcomeDetail{}, fmt.Errorf("updating ruleset: %w", err)
		}

		log.Info("updated branch protection ruleset")
	} else {
		if _, err := client.CreateRepositoryRuleset(ctx, owner, repo, desired); err != nil {
			return outcomeDetail{}, fmt.Errorf("creating ruleset: %w", err)
		}

		log.Info("created branch protection ruleset")
	}

	metrics.BranchProtectionRemediatedTotal.WithLabelValues(rule.Name, owner).Inc()

	return appliedDetail(), nil
}

// rulesetMismatchDetail records a branch with no matching ruleset as
// ruleset_missing, and a ruleset that differs as ruleset_mismatch.
func rulesetMismatchDetail(branch string, existing *ghclient.Ruleset, mismatches []string) outcomeDetail {
	if existing == nil {
		return nonCompliant(findings.RulesetMissingEvidence{Branch: branch})
	}

	return nonCompliant(findings.RulesetMismatchEvidence{Branch: branch, RulesetID: existing.ID, Mismatches: mismatches})
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
