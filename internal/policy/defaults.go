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
		Type:     "file",
		Name:     "catalog_info",
		Enabled:  &enabled,
		Check:    "exists",
		Paths:    []string{"catalog-info.yaml", "catalog-info.yml"},
		Target:   "catalog-info.yaml",
		Template: "catalog-info",
		PR: &PRConfig{
			SearchTerms: []string{"catalog-info"},
		},
		Reconcilers: []ReconcilerConfig{
			{
				Type:  "custom_properties",
				Mode:  mode,
				Watch: false,
			},
		},
	}
}

func defaultGuardianConfig() GuardianConfig {
	return GuardianConfig{
		DryRun:                     false,
		ScheduleInterval:           "168h",
		ParsedScheduleInterval:     defaultScheduleInterval,
		WorkerCount:                defaultWorkerCount,
		QueueSize:                  defaultQueueSize,
		LogLevel:                   defaultLogLevel,
		SkipForks:                  true,
		SkipArchived:               true,
		RateLimitThreshold:         defaultRateLimitThresh,
		WebhookIPAllowlist:         true,
		WebhookIPAllowlistFailOpen: false,
		TrustProxyHeaders:          false,
	}
}

func defaultCodeownersRule() FileRuleConfig {
	enabled := true

	return FileRuleConfig{
		Type:     "file",
		Name:     "codeowners",
		Enabled:  &enabled,
		Check:    "exists",
		Paths:    []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"},
		Target:   ".github/CODEOWNERS",
		Template: "codeowners",
		PR: &PRConfig{
			SearchTerms: []string{"codeowners", "CODEOWNERS"},
		},
	}
}

func defaultDependabotRule() FileRuleConfig {
	enabled := true

	return FileRuleConfig{
		Type:     "file",
		Name:     "dependabot",
		Enabled:  &enabled,
		Check:    "exists",
		Paths:    []string{".github/dependabot.yml", ".github/dependabot.yaml"},
		Target:   ".github/dependabot.yml",
		Template: "dependabot",
		PR: &PRConfig{
			SearchTerms: []string{"dependabot"},
		},
	}
}

func defaultRenovateRule() FileRuleConfig {
	enabled := false

	return FileRuleConfig{
		Type:    "file",
		Name:    "renovate_config",
		Enabled: &enabled,
		Check:   "contains",
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
				Pattern: `github>.*renovate-config`,
				Message: "renovate.json must extend org preset",
			},
		},
	}
}

func defaultRenovateWorkflowRule() FileRuleConfig {
	enabled := false

	return FileRuleConfig{
		Type:     "file",
		Name:     "renovate_workflow",
		Enabled:  &enabled,
		Check:    "exact",
		Paths:    []string{".github/workflows/renovate.yml"},
		Target:   ".github/workflows/renovate.yml",
		Template: "renovate-workflow",
		Reconcilers: []ReconcilerConfig{
			{
				Type:  "workflow_sync",
				Watch: true,
			},
		},
	}
}
