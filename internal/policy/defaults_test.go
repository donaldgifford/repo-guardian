package policy

import (
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/rules"
)

func TestBuiltinDefaults_ReturnsNonNil(t *testing.T) {
	cfg := BuiltinDefaults()
	if cfg == nil {
		t.Fatal("BuiltinDefaults() returned nil")
	}
}

func TestBuiltinDefaults_GuardianConfig(t *testing.T) {
	cfg := BuiltinDefaults()
	g := cfg.Guardian

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"DryRun", g.DryRun, false},
		{"ScheduleInterval", g.ScheduleInterval, "168h"},
		{"ParsedScheduleInterval", g.ParsedScheduleInterval, 168 * time.Hour},
		{"WorkerCount", g.WorkerCount, 5},
		{"QueueSize", g.QueueSize, 1000},
		{"LogLevel", g.LogLevel, "info"},
		{"SkipForks", g.SkipForks, true},
		{"SkipArchived", g.SkipArchived, true},
		{"RateLimitThreshold", g.RateLimitThreshold, 0.10},
		{"WebhookIPAllowlist", g.WebhookIPAllowlist, true},
		{"WebhookIPAllowlistFailOpen", g.WebhookIPAllowlistFailOpen, false},
		{"TrustProxyHeaders", g.TrustProxyHeaders, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestBuiltinDefaults_FileRuleCount(t *testing.T) {
	cfg := BuiltinDefaults()

	if len(cfg.FileRules) != len(rules.DefaultRules) {
		t.Errorf(
			"FileRules count = %d, want %d (matching rules.DefaultRules)",
			len(cfg.FileRules),
			len(rules.DefaultRules),
		)
	}
}

func TestBuiltinDefaults_MatchesDefaultRules(t *testing.T) {
	cfg := BuiltinDefaults()

	ruleMap := make(map[string]rules.FileRule)
	for _, r := range rules.DefaultRules {
		ruleMap[r.Name] = r
	}

	for _, policyRule := range cfg.FileRules {
		t.Run(policyRule.Name, func(t *testing.T) {
			// Map policy rule names (lowercase) to DefaultRules names.
			nameMap := map[string]string{
				"codeowners": "CODEOWNERS",
				"dependabot": "Dependabot",
				"renovate":   "Renovate",
			}

			defaultName, ok := nameMap[policyRule.Name]
			if !ok {
				t.Fatalf("unexpected policy rule name %q", policyRule.Name)
			}

			defaultRule, found := ruleMap[defaultName]
			if !found {
				t.Fatalf("no matching DefaultRule for %q", policyRule.Name)
			}

			// Compare enabled state.
			if policyRule.IsEnabled() != defaultRule.Enabled {
				t.Errorf("Enabled = %v, want %v", policyRule.IsEnabled(), defaultRule.Enabled)
			}

			// Compare paths.
			if len(policyRule.Paths) != len(defaultRule.Paths) {
				t.Errorf("Paths count = %d, want %d", len(policyRule.Paths), len(defaultRule.Paths))
			} else {
				for i, path := range policyRule.Paths {
					if path != defaultRule.Paths[i] {
						t.Errorf("Paths[%d] = %q, want %q", i, path, defaultRule.Paths[i])
					}
				}
			}

			// Compare target.
			if policyRule.Target != defaultRule.TargetPath {
				t.Errorf("Target = %q, want %q", policyRule.Target, defaultRule.TargetPath)
			}

			// Compare template name.
			if policyRule.Template != defaultRule.DefaultTemplateName {
				t.Errorf("Template = %q, want %q", policyRule.Template, defaultRule.DefaultTemplateName)
			}

			// Compare PR search terms.
			if policyRule.PR == nil {
				t.Fatal("PR config is nil")
			}

			if len(policyRule.PR.SearchTerms) != len(defaultRule.PRSearchTerms) {
				t.Errorf(
					"PR.SearchTerms count = %d, want %d",
					len(policyRule.PR.SearchTerms),
					len(defaultRule.PRSearchTerms),
				)
			} else {
				for i, term := range policyRule.PR.SearchTerms {
					if term != defaultRule.PRSearchTerms[i] {
						t.Errorf("PR.SearchTerms[%d] = %q, want %q", i, term, defaultRule.PRSearchTerms[i])
					}
				}
			}

			// All default rules use "exists" check mode.
			if policyRule.CheckMode() != CheckExists {
				t.Errorf("CheckMode() = %v, want %v", policyRule.CheckMode(), CheckExists)
			}
		})
	}
}

func TestBuiltinDefaults_EmptyIgnoreList(t *testing.T) {
	cfg := BuiltinDefaults()

	if len(cfg.IgnoreList.Repos) != 0 {
		t.Errorf("IgnoreList.Repos count = %d, want 0", len(cfg.IgnoreList.Repos))
	}
}
