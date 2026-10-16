package policy

// Scope evaluation, shared by the engine and by anything that has to
// predict what the engine will do. See DESIGN-0010 for the two-gate
// model and IMPL-0023 task 5.1 for why it moved here.
//
// These three predicates decide which rules apply to which org. They
// used to be unexported helpers in internal/checker, which was fine
// while the engine was their only caller. It stopped being fine when
// the monitoring generator arrived: a dashboard row is a claim about
// which rules the engine evaluates for an org, so a second copy of this
// logic would not fail loudly — it would render a plausible, wrong row.
//
// internal/monitoring cannot import internal/checker (the engine drags
// in the reconcilers and their metric registrations, which a read-only
// CLI must not do), so the shared predicate lives in the package both
// already depend on. Same shape as ExtractWatchedPaths: a projection of
// the policy, living with the policy, consumed elsewhere.

// IsStrictScope reports whether the config declared a top-level scope
// block.
//
// Strict mode changes what an absent rule-level scope MEANS: in legacy
// mode a rule with no scope applies everywhere, in strict mode it
// applies nowhere. That inversion is why callers must thread the answer
// rather than each deciding for themselves.
func IsStrictScope(cfg *PolicyConfig) bool {
	return cfg != nil && cfg.Scope != nil
}

// ScopeAllowsOrg is the policy-level (top-level) gate.
//
// Returns true in legacy mode so existing single-org configs pass
// through unchanged. In strict mode, true only when owner matches at
// least one pattern in the top-level scope.orgs list.
func ScopeAllowsOrg(cfg *PolicyConfig, owner string) bool {
	if cfg == nil || cfg.Scope == nil {
		return true
	}

	return cfg.Scope.Matches(owner)
}

// RuleScopeAllowsOrg is the rule-level gate.
//
// Always true in legacy mode. In strict mode the top-level gate has
// already passed for this repo, so a rule carrying the universal "*"
// applies to every in-scope owner; otherwise the rule's own scope.orgs
// list must match. A rule with no scope at all applies to nothing —
// that is the contract strict mode exists to enforce.
func RuleScopeAllowsOrg(rs *ScopeConfig, owner string, strict bool) bool {
	if !strict {
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
