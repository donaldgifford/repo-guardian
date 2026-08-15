package policy

import (
	"os"
	"time"
)

const (
	defaultScheduleInterval = 168 * time.Hour
	defaultWorkerCount      = 5
	defaultQueueSize        = 1000
	defaultLogLevel         = "info"
	defaultRateLimitThresh  = 0.10
)

// Built-in file rule names. Operators reference these from HCL or via
// rule lookups; tests assert against them.
const (
	RuleNameCodeowners       = "codeowners"
	RuleNameDependabot       = "dependabot"
	RuleNameRenovateConfig   = "renovate_config"
	RuleNameRenovateWorkflow = "renovate_workflow"
	RuleNameCatalogInfo      = "catalog_info"
)

// Reconciler type identifiers — second label of `reconcile {}` blocks.
const (
	ReconcilerCustomProperties = "custom_properties"
	ReconcilerLabelSync        = "label_sync"
	ReconcilerBranchProtection = "branch_protection"
	ReconcilerWorkflowSync     = "workflow_sync"
)

// Default file paths referenced by built-in rules.
const (
	pathCodeownersGitHub    = ".github/CODEOWNERS"
	pathCodeownersRoot      = "CODEOWNERS"
	pathDependabotYml       = ".github/dependabot.yml"
	pathRenovateWorkflowYml = ".github/workflows/renovate.yml"
	pathCatalogInfoYaml     = "catalog-info.yaml"
	pathCatalogInfoYml      = "catalog-info.yml"
)

const (
	// renovateOrgPresetPattern is the default regex assertion for the
	// renovate_config rule: requires `renovate.json` to extend an org
	// preset matching `github>.*renovate-config`.
	renovateOrgPresetPattern = `github>.*renovate-config`
	renovateOrgPresetMessage = "renovate.json must extend org preset"
)

// BuiltinDefaults returns the default PolicyConfig that mirrors the current
// hardcoded behavior when no HCL configuration file is present.
// If CUSTOM_PROPERTIES_MODE is set, a catalog_info rule with a
// custom_properties reconciler is included for backward compatibility.
func BuiltinDefaults() *PolicyConfig {
	rules := []FileRuleConfig{
		defaultCodeownersRule(),
		defaultDependabotRule(),
		defaultRenovateRule(),
		defaultRenovateWorkflowRule(),
	}

	if mode := os.Getenv("CUSTOM_PROPERTIES_MODE"); mode != "" {
		rules = append(rules, defaultCatalogInfoRule(mode))
	}

	return &PolicyConfig{
		Guardian:  defaultGuardianConfig(),
		FileRules: rules,
	}
}

func defaultCatalogInfoRule(mode string) FileRuleConfig {
	enabled := true

	return FileRuleConfig{
		Type:     RuleTypeFile,
		Name:     RuleNameCatalogInfo,
		Enabled:  &enabled,
		Check:    string(CheckExists),
		Paths:    []string{pathCatalogInfoYaml, pathCatalogInfoYml},
		Target:   pathCatalogInfoYaml,
		Template: "catalog-info",
		PR: &PRConfig{
			SearchTerms: []string{"catalog-info"},
		},
		Reconcilers: []ReconcilerConfig{
			{
				Type:  ReconcilerCustomProperties,
				Mode:  mode,
				Watch: false,
			},
		},
	}
}

func defaultGuardianConfig() GuardianConfig {
	return GuardianConfig{
		DryRun:                 false,
		ScheduleInterval:       "168h",
		ParsedScheduleInterval: defaultScheduleInterval,
		WorkerCount:            defaultWorkerCount,
		QueueSize:              defaultQueueSize,
		LogLevel:               defaultLogLevel,
		SkipForks:              true,
		SkipArchived:           true,
		RateLimitThreshold:     defaultRateLimitThresh,
	}
}

func defaultCodeownersRule() FileRuleConfig {
	enabled := true

	return FileRuleConfig{
		Type:     RuleTypeFile,
		Name:     RuleNameCodeowners,
		Enabled:  &enabled,
		Check:    string(CheckExists),
		Paths:    []string{pathCodeownersRoot, pathCodeownersGitHub, "docs/CODEOWNERS"},
		Target:   pathCodeownersGitHub,
		Template: RuleNameCodeowners,
		PR: &PRConfig{
			SearchTerms: []string{RuleNameCodeowners, pathCodeownersRoot},
		},
	}
}

func defaultDependabotRule() FileRuleConfig {
	enabled := true

	return FileRuleConfig{
		Type:     RuleTypeFile,
		Name:     RuleNameDependabot,
		Enabled:  &enabled,
		Check:    string(CheckExists),
		Paths:    []string{pathDependabotYml, ".github/dependabot.yaml"},
		Target:   pathDependabotYml,
		Template: "dependabot",
		PR: &PRConfig{
			SearchTerms: []string{RuleNameDependabot},
		},
	}
}

func defaultRenovateRule() FileRuleConfig {
	enabled := false

	return FileRuleConfig{
		Type:    RuleTypeFile,
		Name:    RuleNameRenovateConfig,
		Enabled: &enabled,
		Check:   string(CheckContains),
		Paths: []string{
			"renovate.json",
			"renovate.json5",
			".renovaterc",
			".renovaterc.json",
			".github/renovate.json",
			".github/renovate.json5",
		},
		Target:   "renovate.json",
		Template: "renovate",
		PR: &PRConfig{
			SearchTerms: []string{"renovate"},
		},
		Assertions: []AssertionConfig{
			{
				Pattern: renovateOrgPresetPattern,
				Message: renovateOrgPresetMessage,
			},
		},
	}
}

func defaultRenovateWorkflowRule() FileRuleConfig {
	enabled := false

	return FileRuleConfig{
		Type:     RuleTypeFile,
		Name:     RuleNameRenovateWorkflow,
		Enabled:  &enabled,
		Check:    string(CheckExact),
		Paths:    []string{pathRenovateWorkflowYml},
		Target:   pathRenovateWorkflowYml,
		Template: "renovate-workflow",
		Reconcilers: []ReconcilerConfig{
			{
				Type:  ReconcilerWorkflowSync,
				Watch: true,
			},
		},
	}
}
