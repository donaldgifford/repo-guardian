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
	errs = append(errs, validateWhenGates(cfg)...)
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

	errs = append(errs, validateSearchTerms(r.PR, prefix)...)

	// Absent rules delete by path and have nothing to render, assert, or
	// reconcile: target/template/assertion/reconcile are all forbidden,
	// and none of the content sub-validation below applies (DESIGN-0020).
	if r.CheckMode() == CheckAbsent {
		return append(errs, validateAbsentRule(r, prefix)...)
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

// validateSearchTerms rejects blank `pr { search_terms }` entries. A
// blank term substring-matches every open pull request, so the rule
// would skip itself on every repo that has any PR open at all — a
// silently disabled rule with no log line and no metric to notice it by
// (INV-0011 B4). Failing at load is the same stance IMPL-0018 took for
// unknown `guardian {}` attributes.
func validateSearchTerms(pr *PRConfig, prefix string) []error {
	if pr == nil {
		return nil
	}

	var errs []error

	for i, term := range pr.SearchTerms {
		if strings.TrimSpace(term) == "" {
			errs = append(errs, fmt.Errorf(
				"%s pr: search_terms[%d] must be non-blank (a blank term matches every open PR)",
				prefix, i,
			))
		}
	}

	return errs
}

// validateAbsentRule enforces the absent-mode restrictions: an absent
// rule deletes files by path and has nothing to render or assert, so
// target, template, assertion blocks, and reconcile blocks are all
// rejected (DESIGN-0020 validation matrix). The non-empty paths check
// and the cross-rule when {} gate validation live in the caller and
// validateWhenGates respectively, so they are not repeated here.
func validateAbsentRule(r *FileRuleConfig, prefix string) []error {
	var errs []error

	if r.Target != "" {
		errs = append(errs, fmt.Errorf(
			"%s: check = \"absent\" forbids target (deletions operate on paths)", prefix,
		))
	}

	if r.Template != "" {
		errs = append(errs, fmt.Errorf(
			"%s: check = \"absent\" forbids template (nothing to render)", prefix,
		))
	}

	if len(r.Assertions) > 0 {
		errs = append(errs, fmt.Errorf(
			"%s: check = \"absent\" forbids assertion blocks (no content to assert)", prefix,
		))
	}

	if len(r.Reconcilers) > 0 {
		errs = append(errs, fmt.Errorf(
			"%s: check = \"absent\" forbids reconcile blocks (reconcilers consume file content that must not exist)",
			prefix,
		))
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
// property names: alphanumeric, and the special characters hyphen,
// underscore, dollar sign and number sign, up to 75 characters (GitHub
// REST: organization custom properties).
//
// The original pattern here allowed a period and omitted `$` and `#`,
// which got both edges wrong (INV-0011 A6): `$`/`#` names were rejected
// at load even though GitHub accepts them, while a dotted name loaded
// cleanly and then failed with a 422 at sync time. Failing loudly at
// load is the better trade — see docs/operations/property-name-charset.md.
var githubPropertyNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_$#-]{1,75}$`)

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

// validateWhenGates validates every file rule's when {} gate against the
// full policy: the referenced rule must exist among file rules (not a
// setting/branch-protection rule), be enabled, and not be the rule
// itself; the when {} block must name a rule (empty is an error); and
// the gate graph must be acyclic. Cross-rule checks (existence, cycles)
// cannot live in the per-rule validateFileRule, so they run here over
// the whole rule set (DESIGN-0020 validation matrix).
func validateWhenGates(cfg *PolicyConfig) []error {
	fileRuleByName := make(map[string]*FileRuleConfig, len(cfg.FileRules))
	for i := range cfg.FileRules {
		fileRuleByName[cfg.FileRules[i].Name] = &cfg.FileRules[i]
	}

	nonFileKind := make(map[string]string)
	for i := range cfg.SettingRules {
		nonFileKind[cfg.SettingRules[i].Name] = RuleTypeSetting
	}

	for i := range cfg.BranchProtectionRules {
		nonFileKind[cfg.BranchProtectionRules[i].Name] = RuleTypeBranchProtection
	}

	var errs []error

	// graph maps a gated rule to the rule it references; only well-formed
	// gates (existing, enabled, non-self) enter it, so cycle detection
	// never trips over an already-reported malformed gate.
	graph := make(map[string]string)

	for i := range cfg.FileRules {
		r := &cfg.FileRules[i]
		if r.When == nil {
			continue
		}

		prefix := fmt.Sprintf("rule %q %q when", r.Type, r.Name)
		if err := validateWhenReference(r, prefix, fileRuleByName, nonFileKind, cfg.FileRules); err != nil {
			errs = append(errs, err)
			continue
		}

		graph[r.Name] = r.When.RuleSatisfied
	}

	return append(errs, detectGateCycles(graph)...)
}

// validateWhenReference checks a single rule's when reference and returns
// the first problem found (empty, self-reference, wrong kind, nonexistent,
// disabled), or nil when the gate is well-formed and safe to add to the
// cycle graph.
func validateWhenReference(
	r *FileRuleConfig,
	prefix string,
	fileRuleByName map[string]*FileRuleConfig,
	nonFileKind map[string]string,
	fileRules []FileRuleConfig,
) error {
	ref := r.When.RuleSatisfied

	switch ref {
	case "":
		return fmt.Errorf("%s: rule_satisfied must be set (empty when {} block)", prefix)
	case r.Name:
		return fmt.Errorf("%s: rule_satisfied cannot reference its own rule %q", prefix, ref)
	}

	referee, ok := fileRuleByName[ref]
	if !ok {
		if kind, isNonFile := nonFileKind[ref]; isNonFile {
			return fmt.Errorf(
				"%s: rule_satisfied %q is a %q rule; when gates reference file rules only",
				prefix, ref, kind,
			)
		}

		return fmt.Errorf(
			"%s: rule_satisfied %q names no file rule; known file rules: %s",
			prefix, ref, knownFileRuleNames(fileRules),
		)
	}

	if !referee.IsEnabled() {
		return fmt.Errorf(
			"%s: rule_satisfied %q references a disabled rule (a permanently-closed gate is a policy bug)",
			prefix, ref,
		)
	}

	return nil
}

// detectGateCycles reports every cycle in the gate graph. Each node has
// at most one outgoing edge (a rule gates on exactly one referee), so
// the graph is functional and a single forward walk per unseen node
// finds any cycle: a node revisited while still on the current walk
// closes a loop. Nodes are finalized (state 2) after each walk, so a
// state-1 node is always on the current path.
func detectGateCycles(graph map[string]string) []error {
	const (
		inProgress = 1
		done       = 2
	)

	state := make(map[string]int, len(graph))

	starts := make([]string, 0, len(graph))
	for name := range graph {
		starts = append(starts, name)
	}

	sort.Strings(starts)

	var errs []error

	for _, start := range starts {
		if state[start] != 0 {
			continue
		}

		var path []string

		for node := start; ; {
			switch state[node] {
			case done:
				node = "" // joins an already-verified acyclic chain
			case inProgress:
				errs = append(errs, gateCycleError(path, node))
				node = ""
			default:
				state[node] = inProgress
				path = append(path, node)

				next, ok := graph[node]
				if !ok {
					node = ""
					break
				}

				node = next
			}

			if node == "" {
				break
			}
		}

		for _, n := range path {
			state[n] = done
		}
	}

	return errs
}

// gateCycleError formats the cycle closed when node is re-encountered on
// path, as "a -> b -> a".
func gateCycleError(path []string, node string) error {
	start := 0

	for i, n := range path {
		if n == node {
			start = i
			break
		}
	}

	cycle := make([]string, 0, len(path)-start+1)
	cycle = append(cycle, path[start:]...)
	cycle = append(cycle, node)

	return fmt.Errorf("when gate cycle detected: %s", strings.Join(cycle, " -> "))
}

// knownFileRuleNames returns the sorted, comma-separated file-rule names
// for the "no such rule" diagnostic.
func knownFileRuleNames(rules []FileRuleConfig) string {
	names := make([]string, 0, len(rules))
	for i := range rules {
		names = append(names, rules[i].Name)
	}

	sort.Strings(names)

	return strings.Join(names, ", ")
}
