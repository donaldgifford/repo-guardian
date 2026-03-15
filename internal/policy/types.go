// Package policy provides HCL-based policy configuration for repo-guardian.
// It parses guardian.hcl files into typed Go structs and supports three check
// modes (exists, contains, exact) with typed content assertions.
package policy

import "time"

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
	Guardian   GuardianConfig   `hcl:"guardian,block"`
	IgnoreList IgnoreConfig     `hcl:"ignore,block"`
	FileRules  []FileRuleConfig `hcl:"rule,block"`
}

// GuardianConfig holds operational settings for the guardian application.
type GuardianConfig struct {
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

// PRConfig holds PR detection settings.
type PRConfig struct {
	SearchTerms []string `hcl:"search_terms"`
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
// This is a placeholder that will be fully implemented in IMPL-0007.
type IgnoreConfig struct {
	Repos []string `hcl:"repos,optional"`
}

// ReconcilerConfig holds configuration for a reconciler attached to a rule.
// This is a placeholder that will be fully implemented in IMPL-0006.
type ReconcilerConfig struct {
	Type  string `hcl:"type,label"`
	Watch bool   `hcl:"watch,optional"`
	Mode  string `hcl:"mode,optional"`
}
