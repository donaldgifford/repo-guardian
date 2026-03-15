package policy

import (
	"errors"
	"fmt"
)

// Validate checks the PolicyConfig for configuration errors.
// Returns a joined error with all validation failures.
func Validate(cfg *PolicyConfig) error {
	errs := make([]error, 0, len(cfg.FileRules)+len(cfg.SettingRules)+4)

	errs = append(errs, validateGuardian(&cfg.Guardian)...)
	errs = append(errs, validateFileRules(cfg.FileRules)...)
	errs = append(errs, validateNoDuplicateRules(cfg.FileRules)...)
	errs = append(errs, validateSettingRules(cfg.SettingRules)...)

	return errors.Join(errs...)
}

func validateGuardian(g *GuardianConfig) []error {
	var errs []error

	if g.WorkerCount <= 0 {
		errs = append(errs, fmt.Errorf("guardian.worker_count must be > 0, got %d", g.WorkerCount))
	}

	if g.QueueSize <= 0 {
		errs = append(errs, fmt.Errorf("guardian.queue_size must be > 0, got %d", g.QueueSize))
	}

	if g.RateLimitThreshold < 0 || g.RateLimitThreshold > 1.0 {
		errs = append(errs, fmt.Errorf(
			"guardian.rate_limit_threshold must be between 0.0 and 1.0, got %f",
			g.RateLimitThreshold,
		))
	}

	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}

	if !validLogLevels[g.LogLevel] {
		errs = append(errs, fmt.Errorf(
			"guardian.log_level must be one of debug, info, warn, error; got %q",
			g.LogLevel,
		))
	}

	return errs
}

func validateFileRules(rules []FileRuleConfig) []error {
	errs := make([]error, 0, len(rules))

	for i := range rules {
		r := &rules[i]
		prefix := fmt.Sprintf("rule %q %q", r.Type, r.Name)

		errs = append(errs, validateFileRule(r, prefix)...)
	}

	return errs
}

func validateFileRule(r *FileRuleConfig, prefix string) []error {
	var errs []error

	validChecks := map[string]bool{
		"exists":   true,
		"contains": true,
		"exact":    true,
		"":         true, // defaults to "exists"
	}

	if !validChecks[r.Check] {
		errs = append(errs, fmt.Errorf(
			"%s: check must be one of exists, contains, exact; got %q",
			prefix, r.Check,
		))
	}

	if len(r.Paths) == 0 {
		errs = append(errs, fmt.Errorf("%s: paths must be non-empty", prefix))
	}

	if r.Target == "" {
		errs = append(errs, fmt.Errorf("%s: target must be non-empty", prefix))
	}

	if r.Template == "" {
		errs = append(errs, fmt.Errorf("%s: template must be non-empty", prefix))
	}

	if len(r.Assertions) > 0 && r.CheckMode() != CheckContains {
		errs = append(errs, fmt.Errorf(
			"%s: assertions require check = \"contains\", got %q",
			prefix, r.Check,
		))
	}

	for j, a := range r.Assertions {
		aPrefix := fmt.Sprintf("%s assertion[%d]", prefix, j)
		errs = append(errs, validateAssertion(&a, aPrefix)...)
	}

	return errs
}

func validateAssertion(a *AssertionConfig, prefix string) []error {
	var errs []error

	if a.Message == "" {
		errs = append(errs, fmt.Errorf("%s: message is required", prefix))
	}

	hasPattern := a.Pattern != "" || a.NotPattern != ""
	hasYAMLPath := a.YAMLPath != ""

	if hasPattern && hasYAMLPath {
		errs = append(errs, fmt.Errorf(
			"%s: pattern/not_pattern and yaml_path are mutually exclusive",
			prefix,
		))
	}

	if hasYAMLPath && a.Contains == "" && a.Equals == "" {
		errs = append(errs, fmt.Errorf(
			"%s: yaml_path requires either contains or equals",
			prefix,
		))
	}

	if !hasPattern && !hasYAMLPath {
		errs = append(errs, fmt.Errorf(
			"%s: must set pattern, not_pattern, or yaml_path",
			prefix,
		))
	}

	return errs
}

func validateSettingRules(rules []SettingRuleConfig) []error {
	errs := make([]error, 0, len(rules))

	seen := make(map[string]bool)

	for i := range rules {
		r := &rules[i]
		prefix := fmt.Sprintf("setting rule %q", r.Name)

		if r.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name must be non-empty", prefix))
		}

		if !SupportedSettingProperties[r.Property] {
			errs = append(errs, fmt.Errorf(
				"%s: unsupported property %q; must be one of the supported setting properties",
				prefix, r.Property,
			))
		}

		if r.Expected == nil {
			errs = append(errs, fmt.Errorf("%s: expected must be set", prefix))
		} else {
			errs = append(errs, validateSettingExpectedType(r, prefix)...)
		}

		if seen[r.Name] {
			errs = append(errs, fmt.Errorf(
				"duplicate setting rule %q defined more than once", r.Name,
			))
		}

		seen[r.Name] = true
	}

	return errs
}

func validateSettingExpectedType(r *SettingRuleConfig, prefix string) []error {
	var errs []error

	switch r.Property {
	case "default_branch":
		if _, ok := r.Expected.(string); !ok {
			errs = append(errs, fmt.Errorf(
				"%s: expected must be a string for property %q, got %T",
				prefix, r.Property, r.Expected,
			))
		}
	case "vulnerability_alerts_enabled", "has_issues", "has_wiki",
		"delete_branch_on_merge", "allow_merge_commit",
		"allow_squash_merge", "allow_rebase_merge":
		if _, ok := r.Expected.(bool); !ok {
			errs = append(errs, fmt.Errorf(
				"%s: expected must be a bool for property %q, got %T",
				prefix, r.Property, r.Expected,
			))
		}
	}

	return errs
}

func validateNoDuplicateRules(rules []FileRuleConfig) []error {
	var errs []error

	seen := make(map[string]bool)

	for i := range rules {
		key := rules[i].Type + ":" + rules[i].Name
		if seen[key] {
			errs = append(errs, fmt.Errorf(
				"duplicate rule: %s %q defined more than once",
				rules[i].Type, rules[i].Name,
			))
		}

		seen[key] = true
	}

	return errs
}
