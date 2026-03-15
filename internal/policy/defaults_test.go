package policy

import (
	"testing"
	"time"

	"github.com/donaldgifford/repo-guardian/internal/rules"
)

// --- Backward Compatibility Tests ---
// These tests use t.Setenv and cannot be parallel.

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

func TestBuiltinDefaults_CustomPropertiesModeAPI(t *testing.T) {
	t.Setenv("CUSTOM_PROPERTIES_MODE", "api")

	cfg := BuiltinDefaults()

	// Should have 4 rules: 3 defaults + catalog_info.
	if len(cfg.FileRules) != 4 {
		t.Fatalf("FileRules count = %d, want 4", len(cfg.FileRules))
	}

	catalogRule := cfg.FileRules[3]

	if catalogRule.Name != "catalog_info" {
		t.Errorf("Name = %q, want %q", catalogRule.Name, "catalog_info")
	}

	if len(catalogRule.Reconcilers) != 1 {
		t.Fatalf("Reconcilers count = %d, want 1", len(catalogRule.Reconcilers))
	}

	rec := catalogRule.Reconcilers[0]

	if rec.Type != "custom_properties" {
		t.Errorf("Reconciler.Type = %q, want %q", rec.Type, "custom_properties")
	}

	if rec.Mode != "api" {
		t.Errorf("Reconciler.Mode = %q, want %q", rec.Mode, "api")
	}

	if rec.Watch {
		t.Error("Reconciler.Watch should be false in legacy mode")
	}
}

func TestBuiltinDefaults_CustomPropertiesModeEmpty(t *testing.T) {
	t.Setenv("CUSTOM_PROPERTIES_MODE", "")

	cfg := BuiltinDefaults()

	// Should have 3 rules (no catalog_info).
	if len(cfg.FileRules) != 3 {
		t.Fatalf("FileRules count = %d, want 3", len(cfg.FileRules))
	}

	for _, r := range cfg.FileRules {
		if r.Name == "catalog_info" {
			t.Error("catalog_info rule should not be present when mode is empty")
		}
	}
}

func TestBuiltinDefaults_CustomPropertiesModeGHA(t *testing.T) {
	t.Setenv("CUSTOM_PROPERTIES_MODE", "github-action")

	cfg := BuiltinDefaults()

	if len(cfg.FileRules) != 4 {
		t.Fatalf("FileRules count = %d, want 4", len(cfg.FileRules))
	}

	catalogRule := cfg.FileRules[3]
	rec := catalogRule.Reconcilers[0]

	if rec.Mode != "github-action" {
		t.Errorf("Reconciler.Mode = %q, want %q", rec.Mode, "github-action")
	}
}
