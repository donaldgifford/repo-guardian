// Package policy provides HCL-based policy configuration for repo-guardian.
// It parses guardian.hcl files into typed Go structs and supports three check
// modes (exists, contains, exact) with typed content assertions.
package policy

import (
	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

// CheckMode defines how a file rule evaluates a repository.
type CheckMode string

const (
	// CheckExists checks only that the file is present.
	CheckExists CheckMode = "exists"

	// CheckContains checks that the file exists and passes content assertions.
	CheckContains CheckMode = "contains"

	// CheckExact checks that the file exists and matches the template exactly.
	// YAML files use semantic comparison; plaintext uses byte comparison.
	CheckExact CheckMode = "exact"

	// CheckAbsent checks that none of the rule's paths exist. The rule is
	// actionable when any path is present on the default branch, and its
	// remediation is a file-deletion PR on the reconcile branch rather
	// than an add/fix commit (DESIGN-0020). Absent rules carry no target,
	// template, assertions, or reconcilers — those are rejected at load.
	CheckAbsent CheckMode = "absent"
)

// Rule block type identifiers — the first label on a `rule {}` block.
const (
	RuleTypeFile             = "file"
	RuleTypeSetting          = "setting"
	RuleTypeBranchProtection = "branch_protection"
)

// Supported setting rule properties. Used as the second label of
// `rule "setting" "..." {}` blocks and as the keys of
// SupportedSettingProperties.
const (
	SettingVulnerabilityAlertsEnabled = "vulnerability_alerts_enabled"
	SettingDefaultBranch              = "default_branch"
	SettingHasIssues                  = "has_issues"
	SettingHasWiki                    = "has_wiki"
	SettingDeleteBranchOnMerge        = "delete_branch_on_merge"
	SettingAllowMergeCommit           = "allow_merge_commit"
	SettingAllowSquashMerge           = "allow_squash_merge"
	SettingAllowRebaseMerge           = "allow_rebase_merge"
)

// PolicyConfig is the top-level parsed configuration.
type PolicyConfig struct {
	Guardian              GuardianConfig               `hcl:"guardian,block"`
	IgnoreList            IgnoreConfig                 `hcl:"ignore,block"`
	Scope                 *ScopeConfig                 `hcl:"scope,block"`
	Defaults              *DefaultsConfig              `hcl:"defaults,block"`
	FileRules             []FileRuleConfig             `hcl:"rule,block"`
	SettingRules          []SettingRuleConfig          `hcl:"-"`
	BranchProtectionRules []BranchProtectionRuleConfig `hcl:"-"`
}

// DefaultsConfig is the parsed top-level `defaults { }` block. It carries
// process-wide defaults that propagate down into every rule and reconciler
// PR template via the resolution functions in pr.go.
type DefaultsConfig struct {
	PR *PRConfig `hcl:"pr,block"`
}

// GuardianConfig holds operational settings for the guardian application.
type GuardianConfig struct {
	DryRun             bool    `hcl:"dry_run,optional"`
	LogLevel           string  `hcl:"log_level,optional"`
	SkipForks          bool    `hcl:"skip_forks,optional"`
	SkipArchived       bool    `hcl:"skip_archived,optional"`
	RateLimitThreshold float64 `hcl:"rate_limit_threshold,optional"`

	// AutoClosePR controls whether repo-guardian closes its own open
	// pull request when every file rule has been satisfied on the
	// default branch (IMPL-0013 Phase 3). Default true; set to false
	// to preserve the legacy behaviour where the PR stays open until
	// a human closes it. Override via env AUTO_CLOSE_PR.
	AutoClosePR *bool `hcl:"auto_close_pr,optional"`

	// OrphanCleanup controls whether repo-guardian removes files from
	// its own reconcile branch once the rule that added them is
	// satisfied on the default branch (IMPL-0013 Phase 3). Default
	// true; set to false to disable the behaviour entirely.
	// Override via env ORPHAN_CLEANUP.
	//
	// The kill switch exists because this path deletes files, and a
	// defect in it (INV-0014) once produced PRs proposing to remove
	// files the repository legitimately owned. The defect is fixed —
	// discoverOrphans now skips any path present on the default branch
	// — so the default stays true, and turning it off costs only PR
	// bodies that keep listing rules already satisfied on main.
	OrphanCleanup *bool `hcl:"orphan_cleanup,optional"`
}

// OrphanCleanupEnabled returns whether orphan cleanup is active.
// Defaults to true when the field is unset — the behaviour is correct
// post-INV-0014, and defaulting it off would regress the INV-0005
// convergence fix for every operator.
func (g *GuardianConfig) OrphanCleanupEnabled() bool {
	if g == nil || g.OrphanCleanup == nil {
		return true
	}

	return *g.OrphanCleanup
}

// AutoClosePREnabled returns whether the auto-close behaviour is
// active. Defaults to true when the field is unset.
func (g *GuardianConfig) AutoClosePREnabled() bool {
	if g.AutoClosePR == nil {
		return true
	}

	return *g.AutoClosePR
}

// FileRuleConfig defines a file compliance rule.
type FileRuleConfig struct {
	// Type is the first label in the HCL block (e.g., "file").
	Type string `hcl:"type,label"`

	// Name is the second label in the HCL block (e.g., "codeowners").
	Name string `hcl:"name,label"`

	Enabled     *bool              `hcl:"enabled,optional"`
	Check       string             `hcl:"check,optional"`
	Paths       []string           `hcl:"paths"`
	Target      string             `hcl:"target,optional"`
	Template    string             `hcl:"template,optional"`
	PR          *PRConfig          `hcl:"pr,block"`
	Assertions  []AssertionConfig  `hcl:"assertion,block"`
	Ignore      *IgnoreConfig      `hcl:"ignore,block"`
	Scope       *ScopeConfig       `hcl:"scope,block"`
	Reconcilers []ReconcilerConfig `hcl:"reconcile,block"`

	// When, if set, makes this rule conditional on a sibling file rule
	// being satisfied on the repository's default branch (DESIGN-0020).
	// A closed gate skips the rule entirely for the current repo-check:
	// it is not actionable, produces no orphans, and its reconcilers do
	// not run. Legal on any check mode. See WhenConfig for the
	// evaluation contract.
	When *WhenConfig `hcl:"when,block"`
}

// WhenConfig gates a file rule on the state of a sibling file rule.
// The gate is open when the referenced rule (by its HCL name label) is
// satisfied on the default branch — its paths exist and, for contains/
// exact modes, its assertions/content match. The evaluation is:
//
//   - default-branch-only: the gate never reads the reconcile branch,
//     so repo-guardian never acts on a not-yet-merged referee state;
//   - content-only: the referenced rule's own scope/ignore never affect
//     the gate — the referee is a named bundle of paths+assertions, and
//     the gated rule's own scope/ignore control where it applies
//     (DESIGN-0020 Decision 2);
//   - fail-closed: if evaluating the referenced rule errors, the gate is
//     treated as closed and the rule is skipped this sweep, so a
//     transient API error never triggers a destructive remediation
//     against a repo whose referee state is unknown (Decision, INV-0011
//     A1 principle).
type WhenConfig struct {
	// RuleSatisfied is the HCL name label of the sibling file rule this
	// rule is gated on. Validated at load: the referee must exist among
	// file rules, be enabled, not be this rule, and not participate in a
	// gate cycle.
	RuleSatisfied string `hcl:"rule_satisfied,optional"`
}

// IsEnabled returns whether the rule is enabled, defaulting to true.
func (f *FileRuleConfig) IsEnabled() bool {
	if f.Enabled == nil {
		return true
	}

	return *f.Enabled
}

// CheckMode returns the parsed CheckMode, defaulting to CheckExists.
func (f *FileRuleConfig) CheckMode() CheckMode {
	switch f.Check {
	case string(CheckContains):
		return CheckContains
	case string(CheckExact):
		return CheckExact
	case string(CheckAbsent):
		return CheckAbsent
	default:
		return CheckExists
	}
}

// PRConfig is template.PRConfig, which moved to internal/template
// (DESIGN-0031 D17) so a control definition's pr {} block needs no import
// of this package. The alias keeps the rule engine compiling until
// IMPL-0029 Phase 7 deletes it.
type PRConfig = tmpl.PRConfig

// PRTemplate is template.PRTemplate; see PRConfig.
type PRTemplate = tmpl.PRTemplate

// AssertionConfig defines a content assertion for a file rule.
// Pattern and YAMLPath are mutually exclusive.
// When YAMLPath is set, exactly one of Contains, Equals, or NonEmpty
// must also be set.
type AssertionConfig struct {
	Pattern    string `hcl:"pattern,optional"`
	NotPattern string `hcl:"not_pattern,optional"`
	YAMLPath   string `hcl:"yaml_path,optional"`
	Contains   string `hcl:"contains,optional"`
	Equals     string `hcl:"equals,optional"`
	NonEmpty   bool   `hcl:"non_empty,optional"`
	Message    string `hcl:"message"`
}

// IgnoreConfig holds repository ignore patterns for global or per-rule use.
// Patterns support glob matching via path.Match (e.g., "myorg/terraform-*").
type IgnoreConfig struct {
	Repos []string `hcl:"repos,optional"`
}

// ScopeConfig holds org match patterns. Used in two places:
//   - PolicyConfig.Scope: top-level universe declaration. Presence engages
//     strict mode (every rule must declare its own Scope).
//   - FileRuleConfig.Scope / SettingRuleConfig.Scope /
//     BranchProtectionRuleConfig.Scope: rule-level subset of the universe.
//
// Patterns use the same glob model as IgnoreConfig (path.Match, lowercase
// normalization).
type ScopeConfig struct {
	Orgs []string `hcl:"orgs,optional"`
}

// SettingRuleConfig defines a repository setting compliance rule.
type SettingRuleConfig struct {
	Name      string        `hcl:"name,label"`
	Enabled   *bool         `hcl:"enabled,optional"`
	Property  string        `hcl:"property"`
	Expected  any           `hcl:"expected"`
	Remediate bool          `hcl:"remediate,optional"`
	Ignore    *IgnoreConfig `hcl:"ignore,block"`
	Scope     *ScopeConfig  `hcl:"scope,block"`
}

// IsEnabled returns whether the setting rule is enabled, defaulting to true.
func (s *SettingRuleConfig) IsEnabled() bool {
	if s.Enabled == nil {
		return true
	}

	return *s.Enabled
}

// SupportedSettingProperties is the set of valid property names for setting rules.
var SupportedSettingProperties = map[string]bool{
	SettingVulnerabilityAlertsEnabled: true,
	SettingDefaultBranch:              true,
	SettingHasIssues:                  true,
	SettingHasWiki:                    true,
	SettingDeleteBranchOnMerge:        true,
	SettingAllowMergeCommit:           true,
	SettingAllowSquashMerge:           true,
	SettingAllowRebaseMerge:           true,
}

// BranchProtectionRuleConfig defines a branch protection compliance rule.
type BranchProtectionRuleConfig struct {
	Name                 string        `hcl:"name,label"`
	Enabled              *bool         `hcl:"enabled,optional"`
	Branch               string        `hcl:"branch"`
	RequirePR            bool          `hcl:"require_pr,optional"`
	RequiredApprovals    int           `hcl:"required_approvals,optional"`
	DismissStaleReviews  bool          `hcl:"dismiss_stale_reviews,optional"`
	RequireStatusChecks  []string      `hcl:"require_status_checks,optional"`
	EnforceAdmins        bool          `hcl:"enforce_admins,optional"`
	RequireLinearHistory bool          `hcl:"require_linear_history,optional"`
	Remediate            bool          `hcl:"remediate,optional"`
	Ignore               *IgnoreConfig `hcl:"ignore,block"`
	Scope                *ScopeConfig  `hcl:"scope,block"`
}

// IsEnabled returns whether the branch protection rule is enabled, defaulting to true.
func (b *BranchProtectionRuleConfig) IsEnabled() bool {
	if b.Enabled == nil {
		return true
	}

	return *b.Enabled
}

// ReconcilerConfig holds configuration for a reconciler attached to a rule.
type ReconcilerConfig struct {
	Type        string    `hcl:"type,label"`
	Watch       bool      `hcl:"watch,optional"`
	Mode        string    `hcl:"mode,optional"`
	DeleteExtra bool      `hcl:"delete_extra,optional"`
	PR          *PRConfig `hcl:"pr,block"`

	// AnnotationProperties maps a catalog-info.yaml annotation key
	// (map key, e.g. "jira/project-key") to the GitHub custom property
	// name it should be synced to (map value, e.g. "JiraProject").
	// Consumed by the custom_properties reconciler (DESIGN-0019):
	// Owner and Component are always synced from the Component kind's
	// contract-guaranteed fields and cannot appear here; every other
	// property is sourced from an annotation via this map. Absent or
	// empty means only Owner/Component are synced. See
	// validateAnnotationProperties for the load-time constraints
	// (reserved names, duplicates, GitHub property-name charset).
	AnnotationProperties map[string]string `hcl:"annotation_properties,optional"`
}
