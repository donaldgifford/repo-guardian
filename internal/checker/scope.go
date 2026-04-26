package checker

import (
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// policyScopeAllows is the policy-level (top-level) scope gate. Returns
// true in legacy mode (cfg.Scope == nil) so existing single-org configs
// pass through unchanged. In strict mode, returns true only if the owner
// matches at least one pattern in the top-level scope.orgs list.
func policyScopeAllows(cfg *policy.PolicyConfig, owner string) bool {
	if cfg == nil || cfg.Scope == nil {
		return true
	}

	return cfg.Scope.Matches(owner)
}

// ruleScopeAllows is the rule-level scope gate. Always returns true in
// legacy mode. In strict mode, the top-level gate has already passed
// for this repo, so a rule with the universal "*" applies to every
// in-scope owner; otherwise the rule's own scope.orgs list must match.
func ruleScopeAllows(rs *policy.ScopeConfig, owner string, strictMode bool) bool {
	if !strictMode {
		return true
	}

	if rs == nil {
		return false
	}

	if rs.HasUniversal() {
		return true
	}

	return rs.Matches(owner)
}

// recordOutOfScopePolicy increments OutOfScopeTotal once per enabled rule
// across every rule type. The policy-level gate skips the entire repo, so
// the counter must reflect the rule evaluations that did not happen.
func recordOutOfScopePolicy(cfg *policy.PolicyConfig, owner string) {
	if cfg == nil {
		return
	}

	count := countEnabledRules(cfg)
	if count == 0 {
		return
	}

	metrics.OutOfScopeTotal.WithLabelValues("policy", owner).Add(float64(count))
}

func countEnabledRules(cfg *policy.PolicyConfig) int {
	count := 0

	for i := range cfg.FileRules {
		if cfg.FileRules[i].IsEnabled() {
			count++
		}
	}

	for i := range cfg.SettingRules {
		if cfg.SettingRules[i].IsEnabled() {
			count++
		}
	}

	for i := range cfg.BranchProtectionRules {
		if cfg.BranchProtectionRules[i].IsEnabled() {
			count++
		}
	}

	return count
}

// strictMode returns true when the top-level scope is declared.
func strictMode(cfg *policy.PolicyConfig) bool {
	return cfg != nil && cfg.Scope != nil
}
