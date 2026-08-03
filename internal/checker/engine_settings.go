package checker

// Setting-rule evaluation and remediation. Split out of
// engine_policy.go in IMPL-0021 (INV-0011 B1); pure move, no
// behavior change.

import (
	"context"
	"fmt"
	"log/slog"

	ghclient "github.com/donaldgifford/repo-guardian/internal/github"
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// evaluateSettingRules checks all setting rules against the repository.
func (e *Engine) evaluateSettingRules(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	result *CheckResult,
) error {
	if len(e.policy.SettingRules) == 0 {
		return nil
	}

	ownerRepo := owner + "/" + repo
	strict := strictMode(e.policy)

	for i := range e.policy.SettingRules {
		r := &e.policy.SettingRules[i]

		if !r.IsEnabled() {
			continue
		}

		ruleLog := log.With("setting_rule", r.Name, "property", r.Property)

		if !ruleScopeAllows(r.Scope, owner, strict) {
			ruleLog.Info("setting rule out of scope for org, skipping")
			metrics.OutOfScopeTotal.WithLabelValues("rule", owner).Inc()

			continue
		}

		if r.Ignore != nil && r.Ignore.Matches(ownerRepo) {
			ruleLog.Info("repository matched per-rule ignore list, skipping setting rule")
			metrics.IgnoredTotal.WithLabelValues("rule", owner).Inc()

			continue
		}

		actionable, err := e.evaluateSettingRule(ctx, ruleLog, client, owner, repo, r)
		if err != nil {
			return fmt.Errorf("evaluating setting rule %q: %w", r.Name, err)
		}

		result.add(r.Name, RuleKindSetting, actionable)
	}

	return nil
}

// evaluateSettingRule checks a single setting rule against the
// repository and reports whether the repo is left non-compliant.
//
// A mismatch that this pass successfully remediated returns false: by
// the time the check ends the repo complies, and reporting it as
// actionable would stamp actionable_since on one tick only to clear it
// on the next, turning self-healing into a phantom flap. A mismatch
// left in place — remediation disabled, or dry-run — returns true.
func (e *Engine) evaluateSettingRule(
	ctx context.Context,
	log *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	rule *policy.SettingRuleConfig,
) (bool, error) {
	metrics.SettingsCheckedTotal.WithLabelValues(rule.Name, owner).Inc()

	currentValue, err := e.getSettingValue(ctx, client, owner, repo, rule.Property)
	if err != nil {
		return false, fmt.Errorf("getting current value for %s: %w", rule.Property, err)
	}

	if settingMatches(currentValue, rule.Expected) {
		log.Debug("setting matches expected value", "current", currentValue)
		return false, nil
	}

	metrics.SettingsMismatchedTotal.WithLabelValues(rule.Name, owner).Inc()
	log.Info("setting mismatch", "current", currentValue, "expected", rule.Expected)

	if !rule.Remediate {
		return true, nil
	}

	if e.dryRun {
		log.Info("dry run: would remediate setting", "current", currentValue, "expected", rule.Expected)
		return true, nil
	}

	if err := e.remediateSetting(ctx, log, client, owner, repo, rule); err != nil {
		return false, fmt.Errorf("remediating %s: %w", rule.Property, err)
	}

	metrics.SettingsRemediatedTotal.WithLabelValues(rule.Name, owner).Inc()
	log.Info("remediated setting", "property", rule.Property)

	return false, nil
}

// getSettingValue reads the current value of a repository setting.
func (*Engine) getSettingValue(
	ctx context.Context,
	client ghclient.Client,
	owner, repo, property string,
) (any, error) {
	if property == policy.SettingVulnerabilityAlertsEnabled {
		return client.GetVulnerabilityAlertsEnabled(ctx, owner, repo)
	}

	settings, err := client.GetRepoSettings(ctx, owner, repo)
	if err != nil {
		return nil, err
	}

	switch property {
	case policy.SettingDefaultBranch:
		return settings.DefaultBranch, nil
	case policy.SettingHasIssues:
		return settings.HasIssues, nil
	case policy.SettingHasWiki:
		return settings.HasWiki, nil
	case policy.SettingDeleteBranchOnMerge:
		return settings.DeleteBranchOnMerge, nil
	case policy.SettingAllowMergeCommit:
		return settings.AllowMergeCommit, nil
	case policy.SettingAllowSquashMerge:
		return settings.AllowSquashMerge, nil
	case policy.SettingAllowRebaseMerge:
		return settings.AllowRebaseMerge, nil
	default:
		return nil, fmt.Errorf("unsupported property: %s", property)
	}
}

// settingMatches compares the current value against the expected value.
func settingMatches(current, expected any) bool {
	return fmt.Sprintf("%v", current) == fmt.Sprintf("%v", expected)
}

// remediateSetting applies the expected setting value via the GitHub API.
func (*Engine) remediateSetting(
	ctx context.Context,
	_ *slog.Logger,
	client ghclient.Client,
	owner, repo string,
	rule *policy.SettingRuleConfig,
) error {
	switch rule.Property {
	case policy.SettingVulnerabilityAlertsEnabled:
		expected, ok := rule.Expected.(bool)
		if !ok {
			return fmt.Errorf("expected bool for %s, got %T", policy.SettingVulnerabilityAlertsEnabled, rule.Expected)
		}

		if expected {
			return client.EnableVulnerabilityAlerts(ctx, owner, repo)
		}

		return client.DisableVulnerabilityAlerts(ctx, owner, repo)

	case policy.SettingDefaultBranch:
		expected, ok := rule.Expected.(string)
		if !ok {
			return fmt.Errorf("expected string for %s, got %T", policy.SettingDefaultBranch, rule.Expected)
		}

		return client.UpdateRepository(ctx, owner, repo, &ghclient.RepoUpdateOpts{
			DefaultBranch: &expected,
		})

	default:
		return remediateBoolSetting(ctx, client, owner, repo, rule)
	}
}

// remediateBoolSetting handles remediation for boolean repository settings.
func remediateBoolSetting(
	ctx context.Context,
	client ghclient.Client,
	owner, repo string,
	rule *policy.SettingRuleConfig,
) error {
	expected, ok := rule.Expected.(bool)
	if !ok {
		return fmt.Errorf("expected bool for %s, got %T", rule.Property, rule.Expected)
	}

	opts := &ghclient.RepoUpdateOpts{}

	switch rule.Property {
	case policy.SettingHasIssues:
		opts.HasIssues = &expected
	case policy.SettingHasWiki:
		opts.HasWiki = &expected
	case policy.SettingDeleteBranchOnMerge:
		opts.DeleteBranchOnMerge = &expected
	case policy.SettingAllowMergeCommit:
		opts.AllowMergeCommit = &expected
	case policy.SettingAllowSquashMerge:
		opts.AllowSquashMerge = &expected
	case policy.SettingAllowRebaseMerge:
		opts.AllowRebaseMerge = &expected
	default:
		return fmt.Errorf("unsupported bool property: %s", rule.Property)
	}

	return client.UpdateRepository(ctx, owner, repo, opts)
}
