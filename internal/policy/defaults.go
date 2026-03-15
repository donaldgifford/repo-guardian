package policy

import "time"

const (
	defaultScheduleInterval = 168 * time.Hour
	defaultWorkerCount      = 5
	defaultQueueSize        = 1000
	defaultLogLevel         = "info"
	defaultRateLimitThresh  = 0.10
)

// BuiltinDefaults returns the default PolicyConfig that mirrors the current
// hardcoded behavior when no HCL configuration file is present.
func BuiltinDefaults() *PolicyConfig {
	return &PolicyConfig{
		Guardian: defaultGuardianConfig(),
		FileRules: []FileRuleConfig{
			defaultCodeownersRule(),
			defaultDependabotRule(),
			defaultRenovateRule(),
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
		Template: "codeowners.tmpl",
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
		Template: "dependabot.tmpl",
		PR: &PRConfig{
			SearchTerms: []string{"dependabot"},
		},
	}
}

func defaultRenovateRule() FileRuleConfig {
	enabled := false

	return FileRuleConfig{
		Type:    "file",
		Name:    "renovate",
		Enabled: &enabled,
		Check:   "exists",
		Paths: []string{
			"renovate.json",
			"renovate.json5",
			".renovaterc",
			".renovaterc.json",
			".github/renovate.json",
			".github/renovate.json5",
		},
		Target:   "renovate.json",
		Template: "renovate.tmpl",
		PR: &PRConfig{
			SearchTerms: []string{"renovate"},
		},
	}
}
