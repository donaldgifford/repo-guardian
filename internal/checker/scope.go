package checker

import (
	"github.com/donaldgifford/repo-guardian/internal/metrics"
	"github.com/donaldgifford/repo-guardian/internal/policy"
)

// The three scope predicates moved to internal/policy in IMPL-0023 task
// 5.1 so the monitoring generator can ask the same question the engine
// asks. These aliases keep the call sites in this package reading the
// way they always have; the answer comes from one place.
//
// Do NOT reintroduce local copies. A dashboard row is a claim about
// which rules the engine evaluates for an org, and a divergent copy
// would not fail loudly — it would render a plausible, wrong row.
var (
	policyScopeAllows = policy.ScopeAllowsOrg
	ruleScopeAllows   = policy.RuleScopeAllowsOrg
	strictMode        = policy.IsStrictScope
)

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
