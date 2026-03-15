package policy

import (
	"testing"
)

func TestCheckModeConstants(t *testing.T) {
	tests := []struct {
		mode CheckMode
		want string
	}{
		{CheckExists, "exists"},
		{CheckContains, "contains"},
		{CheckExact, "exact"},
	}

	for _, tt := range tests {
		if string(tt.mode) != tt.want {
			t.Errorf("CheckMode %v = %q, want %q", tt.mode, string(tt.mode), tt.want)
		}
	}
}

func TestFileRuleConfig_IsEnabled(t *testing.T) {
	tests := []struct {
		name    string
		enabled *bool
		want    bool
	}{
		{
			name:    "nil defaults to true",
			enabled: nil,
			want:    true,
		},
		{
			name:    "explicit true",
			enabled: boolPtr(true),
			want:    true,
		},
		{
			name:    "explicit false",
			enabled: boolPtr(false),
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := FileRuleConfig{Enabled: tt.enabled}
			if got := rule.IsEnabled(); got != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFileRuleConfig_CheckMode(t *testing.T) {
	tests := []struct {
		name  string
		check string
		want  CheckMode
	}{
		{
			name:  "empty defaults to exists",
			check: "",
			want:  CheckExists,
		},
		{
			name:  "exists",
			check: "exists",
			want:  CheckExists,
		},
		{
			name:  "contains",
			check: "contains",
			want:  CheckContains,
		},
		{
			name:  "exact",
			check: "exact",
			want:  CheckExact,
		},
		{
			name:  "unknown defaults to exists",
			check: "unknown",
			want:  CheckExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := FileRuleConfig{Check: tt.check}
			if got := rule.CheckMode(); got != tt.want {
				t.Errorf("CheckMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPolicyConfig_ZeroValue(t *testing.T) {
	var cfg PolicyConfig

	if cfg.Guardian.WorkerCount != 0 {
		t.Errorf("zero-value WorkerCount = %d, want 0", cfg.Guardian.WorkerCount)
	}

	if cfg.Guardian.DryRun {
		t.Error("zero-value DryRun = true, want false")
	}

	if len(cfg.FileRules) != 0 {
		t.Errorf("zero-value FileRules len = %d, want 0", len(cfg.FileRules))
	}

	if len(cfg.IgnoreList.Repos) != 0 {
		t.Errorf("zero-value IgnoreList.Repos len = %d, want 0", len(cfg.IgnoreList.Repos))
	}
}

func TestGuardianConfig_ZeroValue(t *testing.T) {
	var cfg GuardianConfig

	if cfg.DryRun {
		t.Error("zero-value DryRun = true, want false")
	}

	if cfg.SkipForks {
		t.Error("zero-value SkipForks = true, want false")
	}

	if cfg.SkipArchived {
		t.Error("zero-value SkipArchived = true, want false")
	}

	if cfg.WebhookIPAllowlist {
		t.Error("zero-value WebhookIPAllowlist = true, want false")
	}

	if cfg.LogLevel != "" {
		t.Errorf("zero-value LogLevel = %q, want empty", cfg.LogLevel)
	}

	if cfg.RateLimitThreshold != 0.0 {
		t.Errorf("zero-value RateLimitThreshold = %f, want 0.0", cfg.RateLimitThreshold)
	}
}

func TestAssertionConfig_ZeroValue(t *testing.T) {
	var a AssertionConfig

	if a.Pattern != "" {
		t.Errorf("zero-value Pattern = %q, want empty", a.Pattern)
	}

	if a.NotPattern != "" {
		t.Errorf("zero-value NotPattern = %q, want empty", a.NotPattern)
	}

	if a.YAMLPath != "" {
		t.Errorf("zero-value YAMLPath = %q, want empty", a.YAMLPath)
	}

	if a.Message != "" {
		t.Errorf("zero-value Message = %q, want empty", a.Message)
	}
}

func TestReconcilerConfig_ZeroValue(t *testing.T) {
	var r ReconcilerConfig

	if r.Type != "" {
		t.Errorf("zero-value Type = %q, want empty", r.Type)
	}

	if r.Watch {
		t.Error("zero-value Watch = true, want false")
	}

	if r.Mode != "" {
		t.Errorf("zero-value Mode = %q, want empty", r.Mode)
	}
}

func boolPtr(b bool) *bool {
	return &b
}
