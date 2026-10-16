package policy

import (
	"errors"
	"fmt"
)

// validateStrictScope enforces the strict-mode validation table from
// DESIGN-0010. It runs only when a top-level scope is declared
// (cfg.Scope != nil); legacy mode performs no scope validation.
//
// In strict mode, every FileRule, SettingRule, and BranchProtectionRule
// must declare its own non-empty scope block. The top-level scope's
// orgs list must also be non-empty.
//
// Duplicate top-level scope blocks across a merged config are caught
// earlier in loadFile / loadDirectory before reaching this function.
func validateStrictScope(cfg *PolicyConfig) error {
	if cfg.Scope == nil {
		return nil
	}

	errs := make([]error, 0, 1+len(cfg.FileRules)+len(cfg.SettingRules)+len(cfg.BranchProtectionRules))

	if len(cfg.Scope.Orgs) == 0 {
		errs = append(errs, errors.New("top-level scope must declare at least one org"))
	}

	for i := range cfg.FileRules {
		errs = append(errs, validateRuleScope("rule", cfg.FileRules[i].Name, cfg.FileRules[i].Scope)...)
	}

	for i := range cfg.SettingRules {
		errs = append(errs, validateRuleScope("rule", cfg.SettingRules[i].Name, cfg.SettingRules[i].Scope)...)
	}

	for i := range cfg.BranchProtectionRules {
		errs = append(errs, validateRuleScope("rule", cfg.BranchProtectionRules[i].Name, cfg.BranchProtectionRules[i].Scope)...)
	}

	return errors.Join(errs...)
}

func validateRuleScope(kind, name string, sc *ScopeConfig) []error {
	if sc == nil {
		return []error{fmt.Errorf("%s %q must declare scope in strict mode", kind, name)}
	}

	if len(sc.Orgs) == 0 {
		return []error{fmt.Errorf("%s %q scope.orgs must not be empty", kind, name)}
	}

	return nil
}
