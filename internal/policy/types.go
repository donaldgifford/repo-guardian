// Package policy provides HCL-based policy configuration for repo-guardian.
// It parses guardian.hcl files into typed Go structs and supports three check
// modes (exists, contains, exact) with typed content assertions.
package policy

import (
	"time"

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
	Org                        string  `hcl:"org,optional"`
	DryRun                     bool    `hcl:"dry_run,optional"`
	ScheduleInterval           string  `hcl:"schedule_interval,optional"`
	WorkerCount                int     `hcl:"worker_count,optional"`
	QueueSize                  int     `hcl:"queue_size,optional"`
	LogLevel                   string  `hcl:"log_level,optional"`
	SkipForks                  bool    `hcl:"skip_forks,optional"`
	SkipArchived               bool    `hcl:"skip_archived,optional"`
	RateLimitThreshold         float64 `hcl:"rate_limit_threshold,optional"`
	WebhookIPAllowlist         bool    `hcl:"webhook_ip_allowlist,optional"`
	WebhookIPAllowlistFailOpen bool    `hcl:"webhook_ip_allowlist_fail_open,optional"`
	TrustProxyHeaders          bool    `hcl:"trust_proxy_headers,optional"`

	// ParsedScheduleInterval is the parsed duration from ScheduleInterval.
	// It is not set from HCL directly but computed after loading.
	ParsedScheduleInterval time.Duration `hcl:"-"`
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
	Target      string             `hcl:"target"`
	Template    string             `hcl:"template"`
	PR          *PRConfig          `hcl:"pr,block"`
	Assertions  []AssertionConfig  `hcl:"assertion,block"`
	Ignore      *IgnoreConfig      `hcl:"ignore,block"`
	Scope       *ScopeConfig       `hcl:"scope,block"`
	Reconcilers []ReconcilerConfig `hcl:"reconcile,block"`
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
	default:
		return CheckExists
	}
}

// PRConfig is the HCL-decoded form of a `pr { }` block. It carries both
// PR-detection settings (SearchTerms — used to find an open PR for a
// rule) and PR-templating fields (Title, Body, Labels, Inherits — used
// to customize the rendered PR text). The same block type appears in
// three scopes:
//
//   - `defaults { pr { } }` — process-wide template defaults.
//   - `rule "file" { pr { } }` — per-rule template plus search_terms.
//   - `reconcile "<type>" { pr { } }` — per-reconciler template.
//
// Title and Body use *string so the loader can distinguish "absent"
// (nil) from explicit empty string (`title = ""`). Labels uses []string
// with a sidecar LabelsSet bool because HCL's []string decoder cannot
// distinguish `labels = []` (explicit empty) from absence (both yield
// nil under the manual element-iterator decode path).
//
// Inherits uses *bool: nil means "not declared" (defaults to true at
// resolve time); explicit false stops propagation from the parent
// scope; explicit true is the same as absence.
//
// CompiledTitle and CompiledBody are populated by the loader after HCL
// decode by parsing Title/Body strings via the package-level renderer.
// Engine hot-path callers consume these compiled forms directly via
// the resolved *PRTemplate returned from pr.go.
type PRConfig struct {
	SearchTerms []string `hcl:"search_terms,optional"`

	Title    *string  `hcl:"title,optional"`
	Body     *string  `hcl:"body,optional"`
	Labels   []string `hcl:"labels,optional"`
	Inherits *bool    `hcl:"inherits,optional"`

	// LabelsSet is true when the `labels` attribute was explicitly
	// present in HCL (including `labels = []`). The loader populates
	// this from the raw attribute map before discarding the body.
	LabelsSet bool `hcl:"-"`

	// CompiledTitle is the parsed Title template. Nil when Title is
	// nil. Populated by the loader; engine consumes via Resolve.
	CompiledTitle *tmpl.Compiled `hcl:"-"`

	// CompiledBody is the parsed Body template. Nil when Body is nil.
	CompiledBody *tmpl.Compiled `hcl:"-"`
}

// PRTemplate is the resolved, render-ready form of a `pr { }` block.
// It is produced by ResolveRulePR / ResolveReconcilerPR after merging
// HCL scopes (defaults → rule → reconciler) field by field. Each of
// Title, Body, Labels, and Inherits independently inherits from the
// parent scope when unset and Inherits=true; an explicit Inherits=false
// at any scope stops propagation from above and falls through directly
// to engine built-in values for fields the scope did not set.
type PRTemplate struct {
	Title    *tmpl.Compiled
	Body     *tmpl.Compiled
	Labels   []string
	Inherits bool

	// LabelsSet mirrors PRConfig.LabelsSet — true means Labels was
	// explicitly set at this scope (including the empty-list override
	// `labels = []`). Resolution honors LabelsSet to distinguish
	// "explicit empty override" from "absent, inherit from parent".
	LabelsSet bool
}

// AssertionConfig defines a content assertion for a file rule.
// Pattern and YAMLPath are mutually exclusive.
// When YAMLPath is set, either Contains or Equals must also be set.
type AssertionConfig struct {
	Pattern    string `hcl:"pattern,optional"`
	NotPattern string `hcl:"not_pattern,optional"`
	YAMLPath   string `hcl:"yaml_path,optional"`
	Contains   string `hcl:"contains,optional"`
	Equals     string `hcl:"equals,optional"`
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
	"vulnerability_alerts_enabled": true,
	"default_branch":               true,
	"has_issues":                   true,
	"has_wiki":                     true,
	"delete_branch_on_merge":       true,
	"allow_merge_commit":           true,
	"allow_squash_merge":           true,
	"allow_rebase_merge":           true,
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
}
