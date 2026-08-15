package policy

import (
	"testing"
	"time"
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

	// 4 rules: codeowners, dependabot, renovate_config, renovate_workflow.
	const wantRuleCount = 4
	if len(cfg.FileRules) != wantRuleCount {
		t.Errorf("FileRules count = %d, want %d", len(cfg.FileRules), wantRuleCount)
	}
}

func TestBuiltinDefaults_RenovateConfigRule(t *testing.T) {
	cfg := BuiltinDefaults()

	var rule *FileRuleConfig
	for i := range cfg.FileRules {
		if cfg.FileRules[i].Name == "renovate_config" {
			rule = &cfg.FileRules[i]
			break
		}
	}

	if rule == nil {
		t.Fatal("renovate_config rule not found")
	}

	if rule.IsEnabled() {
		t.Error("renovate_config should be disabled by default")
	}

	if rule.CheckMode() != CheckContains {
		t.Errorf("CheckMode() = %v, want %v", rule.CheckMode(), CheckContains)
	}

	if len(rule.Assertions) != 1 {
		t.Fatalf("Assertions count = %d, want 1", len(rule.Assertions))
	}

	a := rule.Assertions[0]
	if a.Pattern != `github>.*renovate-config` {
		t.Errorf("Assertion.Pattern = %q, want %q", a.Pattern, `github>.*renovate-config`)
	}

	if a.Message != "renovate.json must extend org preset" {
		t.Errorf("Assertion.Message = %q, want %q", a.Message, "renovate.json must extend org preset")
	}
}

func TestBuiltinDefaults_RenovateWorkflowRule(t *testing.T) {
	cfg := BuiltinDefaults()

	var rule *FileRuleConfig
	for i := range cfg.FileRules {
		if cfg.FileRules[i].Name == "renovate_workflow" {
			rule = &cfg.FileRules[i]
			break
		}
	}

	if rule == nil {
		t.Fatal("renovate_workflow rule not found")
	}

	if rule.IsEnabled() {
		t.Error("renovate_workflow should be disabled by default")
	}

	if rule.CheckMode() != CheckExact {
		t.Errorf("CheckMode() = %v, want %v", rule.CheckMode(), CheckExact)
	}

	if rule.Target != ".github/workflows/renovate.yml" {
		t.Errorf("Target = %q, want %q", rule.Target, ".github/workflows/renovate.yml")
	}

	if rule.Template != "renovate-workflow" {
		t.Errorf("Template = %q, want %q", rule.Template, "renovate-workflow")
	}

	if len(rule.Reconcilers) != 1 {
		t.Fatalf("Reconcilers count = %d, want 1", len(rule.Reconcilers))
	}

	rec := rule.Reconcilers[0]
	if rec.Type != "workflow_sync" {
		t.Errorf("Reconciler.Type = %q, want %q", rec.Type, "workflow_sync")
	}

	if !rec.Watch {
		t.Error("Reconciler.Watch should be true")
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

	// Should have 5 rules: 4 defaults + catalog_info.
	if len(cfg.FileRules) != 5 {
		t.Fatalf("FileRules count = %d, want 5", len(cfg.FileRules))
	}

	catalogRule := cfg.FileRules[4]

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

	if rec.AnnotationProperties != nil {
		t.Errorf("Reconciler.AnnotationProperties = %v, want nil (Owner/Component-only default)", rec.AnnotationProperties)
	}
}

func TestBuiltinDefaults_CustomPropertiesModeEmpty(t *testing.T) {
	t.Setenv("CUSTOM_PROPERTIES_MODE", "")

	cfg := BuiltinDefaults()

	// Should have 4 rules (no catalog_info).
	if len(cfg.FileRules) != 4 {
		t.Fatalf("FileRules count = %d, want 4", len(cfg.FileRules))
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

	if len(cfg.FileRules) != 5 {
		t.Fatalf("FileRules count = %d, want 5", len(cfg.FileRules))
	}

	catalogRule := cfg.FileRules[4]
	rec := catalogRule.Reconcilers[0]

	if rec.Mode != "github-action" {
		t.Errorf("Reconciler.Mode = %q, want %q", rec.Mode, "github-action")
	}
}
