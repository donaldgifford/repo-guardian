package policy

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	logLevelDebug = "debug"
	logLevelWarn  = "warn"
	logLevelError = "error"
)

// Validate checks the PolicyConfig for configuration errors.
// Returns a joined error with all validation failures.
func Validate(cfg *PolicyConfig) error {
	errs := make([]error, 0, len(cfg.FileRules)+len(cfg.SettingRules)+4)

	errs = append(errs, validateGuardian(&cfg.Guardian)...)
	errs = append(errs, validateFileRules(cfg.FileRules)...)
	errs = append(errs, validateNoDuplicateRules(cfg.FileRules)...)
	errs = append(errs, validateSettingRules(cfg.SettingRules)...)
	errs = append(errs, validateBranchProtectionRules(cfg.BranchProtectionRules)...)

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
		logLevelDebug:   true,
		defaultLogLevel: true,
		logLevelWarn:    true,
		logLevelError:   true,
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
		string(CheckExists):   true,
		string(CheckContains): true,
		string(CheckExact):    true,
		string(CheckAbsent):   true,
		"":                    true, // defaults to "exists"
	}

	if !validChecks[r.Check] {
		errs = append(errs, fmt.Errorf(
			"%s: check must be one of exists, contains, exact, absent; got %q",
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

	for j := range r.Reconcilers {
		rec := &r.Reconcilers[j]
		rPrefix := fmt.Sprintf("%s reconcile %q", prefix, rec.Type)
		errs = append(errs, validateAnnotationProperties(rec.AnnotationProperties, rPrefix)...)
	}

	return errs
}

// reservedPropertyNames are GitHub custom property names the
// custom_properties reconciler always manages from contract-guaranteed
// Component fields (spec.owner, metadata.name). annotation_properties
// may not target them (DESIGN-0019: built-in names are fixed, not
// renameable). Compared case-insensitively because GitHub property
// names are unique case-insensitively — an operator mapping to "owner"
// would collide with the built-in "Owner" at PATCH time anyway.
var reservedPropertyNames = map[string]bool{
	"owner":     true,
	"component": true,
}

// githubPropertyNamePattern matches GitHub's constraint on custom
// property names: alphanumeric, underscore, period, hyphen: up to 75
// characters (GitHub REST: organization custom properties).
var githubPropertyNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,75}$`)

// validateAnnotationProperties validates a reconciler's
// annotation_properties map (DESIGN-0019): annotation keys and property
// names must be non-empty, property names must not be reserved
// (Owner/Component), must match GitHub's property-name charset/length,
// and no two annotations may target the same property name. Keys are
// sorted before validation so the returned errors are deterministic.
func validateAnnotationProperties(props map[string]string, prefix string) []error {
	if len(props) == 0 {
		return nil
	}

	var errs []error

	annotations := make([]string, 0, len(props))
	for annotation := range props {
		annotations = append(annotations, annotation)
	}

	sort.Strings(annotations)

	seenProperties := make(map[string]string, len(props))

	for _, annotation := range annotations {
		property := props[annotation]

		if annotation == "" {
			errs = append(errs, fmt.Errorf(
				"%s: annotation_properties has an empty annotation key",
				prefix,
			))
		}

		if property == "" {
			errs = append(errs, fmt.Errorf(
				"%s: annotation_properties[%q] has an empty property name",
				prefix, annotation,
			))

			continue
		}

		lower := strings.ToLower(property)

		if reservedPropertyNames[lower] {
			errs = append(errs, fmt.Errorf(
				"%s: annotation_properties[%q] targets reserved property name %q (Owner/Component are built in and cannot be remapped)",
				prefix, annotation, property,
			))
		}

		if !githubPropertyNamePattern.MatchString(property) {
			errs = append(errs, fmt.Errorf(
				"%s: annotation_properties[%q] property name %q must match %s and be at most 75 characters",
				prefix, annotation, property, githubPropertyNamePattern.String(),
			))
		}

		if existing, ok := seenProperties[lower]; ok {
			errs = append(errs, fmt.Errorf(
				"%s: annotation_properties has duplicate property name %q targeted by both %q and %q",
				prefix, property, existing, annotation,
			))
		} else {
			seenProperties[lower] = annotation
		}
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

	if hasYAMLPath && a.Contains == "" && a.Equals == "" && !a.NonEmpty {
		errs = append(errs, fmt.Errorf(
			"%s: yaml_path requires one of contains, equals, or non_empty",
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
	case SettingDefaultBranch:
		if _, ok := r.Expected.(string); !ok {
			errs = append(errs, fmt.Errorf(
				"%s: expected must be a string for property %q, got %T",
				prefix, r.Property, r.Expected,
			))
		}
	case SettingVulnerabilityAlertsEnabled, SettingHasIssues, SettingHasWiki,
		SettingDeleteBranchOnMerge, SettingAllowMergeCommit,
		SettingAllowSquashMerge, SettingAllowRebaseMerge:
		if _, ok := r.Expected.(bool); !ok {
			errs = append(errs, fmt.Errorf(
				"%s: expected must be a bool for property %q, got %T",
				prefix, r.Property, r.Expected,
			))
		}
	}

	return errs
}

func validateBranchProtectionRules(rules []BranchProtectionRuleConfig) []error {
	errs := make([]error, 0, len(rules))

	seen := make(map[string]bool)

	for i := range rules {
		r := &rules[i]
		prefix := fmt.Sprintf("branch_protection rule %q", r.Name)

		if r.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name must be non-empty", prefix))
		}

		if r.Branch == "" {
			errs = append(errs, fmt.Errorf("%s: branch must be non-empty", prefix))
		}

		if r.RequiredApprovals < 0 {
			errs = append(errs, fmt.Errorf(
				"%s: required_approvals must be >= 0, got %d",
				prefix, r.RequiredApprovals,
			))
		}

		if seen[r.Name] {
			errs = append(errs, fmt.Errorf(
				"duplicate branch_protection rule %q defined more than once", r.Name,
			))
		}

		seen[r.Name] = true
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
