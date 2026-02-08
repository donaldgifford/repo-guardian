package rules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultRulesCount(t *testing.T) {
	t.Parallel()

	if got := len(DefaultRules); got != 3 {
		t.Fatalf("expected 3 default rules, got %d", got)
	}

	enabledCount := 0
	disabledCount := 0

	for _, r := range DefaultRules {
		if r.Enabled {
			enabledCount++
		} else {
			disabledCount++
		}
	}

	if enabledCount != 2 {
		t.Errorf("expected 2 enabled rules, got %d", enabledCount)
	}

	if disabledCount != 1 {
		t.Errorf("expected 1 disabled rule, got %d", disabledCount)
	}
}

func TestEnabledRulesFiltering(t *testing.T) {
	t.Parallel()

	reg := NewRegistry(DefaultRules)
	enabled := reg.EnabledRules()

	if len(enabled) != 2 {
		t.Fatalf("expected 2 enabled rules, got %d", len(enabled))
	}

	names := make(map[string]bool)
	for _, r := range enabled {
		names[r.Name] = true
	}

	if !names["CODEOWNERS"] {
		t.Error("expected CODEOWNERS to be enabled")
	}

	if !names["Dependabot"] {
		t.Error("expected Dependabot to be enabled")
	}

	if names["Renovate"] {
		t.Error("expected Renovate to be disabled")
	}
}

func TestRuleByName(t *testing.T) {
	t.Parallel()

	reg := NewRegistry(DefaultRules)

	tests := []struct {
		name      string
		lookup    string
		wantFound bool
	}{
		{name: "exact match", lookup: "CODEOWNERS", wantFound: true},
		{name: "case insensitive", lookup: "codeowners", wantFound: true},
		{name: "dependabot", lookup: "Dependabot", wantFound: true},
		{name: "renovate", lookup: "renovate", wantFound: true},
		{name: "missing", lookup: "nonexistent", wantFound: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rule, found := reg.RuleByName(tt.lookup)
			if found != tt.wantFound {
				t.Errorf("RuleByName(%q) found = %v, want %v", tt.lookup, found, tt.wantFound)
			}

			if tt.wantFound && rule.Name == "" {
				t.Errorf("RuleByName(%q) returned empty rule", tt.lookup)
			}
		})
	}
}

func TestAllRules(t *testing.T) {
	t.Parallel()

	reg := NewRegistry(DefaultRules)
	all := reg.AllRules()

	if len(all) != len(DefaultRules) {
		t.Fatalf("expected %d rules, got %d", len(DefaultRules), len(all))
	}

	// Verify it's a copy, not a reference to the internal slice.
	all[0].Name = "modified"

	original := reg.AllRules()
	if original[0].Name == "modified" {
		t.Error("AllRules should return a copy, not a reference")
	}
}

func TestTemplateStoreEmbeddedFallback(t *testing.T) {
	t.Parallel()

	ts := NewTemplateStore()

	// Load with empty dir — should use embedded defaults.
	if err := ts.Load(""); err != nil {
		t.Fatalf("Load with empty dir: %v", err)
	}

	for _, name := range []string{"codeowners", "dependabot", "renovate"} {
		content, err := ts.Get(name)
		if err != nil {
			t.Errorf("Get(%q): %v", name, err)
			continue
		}

		if content == "" {
			t.Errorf("Get(%q) returned empty content", name)
		}
	}
}

func TestTemplateStoreDirectoryOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	overrideContent := "# Custom CODEOWNERS override\n* @myorg/myteam\n"
	if err := os.WriteFile(filepath.Join(dir, "codeowners.tmpl"), []byte(overrideContent), 0o644); err != nil {
		t.Fatalf("writing override template: %v", err)
	}

	ts := NewTemplateStore()
	if err := ts.Load(dir); err != nil {
		t.Fatalf("Load(%q): %v", dir, err)
	}

	// codeowners should use the override.
	content, err := ts.Get("codeowners")
	if err != nil {
		t.Fatalf("Get(codeowners): %v", err)
	}

	if content != overrideContent {
		t.Errorf("expected override content, got %q", content)
	}

	// dependabot should still use embedded default.
	content, err = ts.Get("dependabot")
	if err != nil {
		t.Fatalf("Get(dependabot): %v", err)
	}

	if content == "" {
		t.Error("Get(dependabot) returned empty content")
	}
}

func TestTemplateStoreGetMissing(t *testing.T) {
	t.Parallel()

	ts := NewTemplateStore()
	if err := ts.Load(""); err != nil {
		t.Fatalf("Load: %v", err)
	}

	_, err := ts.Get("nonexistent")
	if err == nil {
		t.Error("expected error for missing template, got nil")
	}
}

func TestTemplateStoreNonexistentDir(t *testing.T) {
	t.Parallel()

	ts := NewTemplateStore()

	// Non-existent directory should fall through to embedded.
	if err := ts.Load("/nonexistent/dir/that/does/not/exist"); err != nil {
		t.Fatalf("Load with nonexistent dir should not error: %v", err)
	}

	content, err := ts.Get("codeowners")
	if err != nil {
		t.Fatalf("Get(codeowners): %v", err)
	}

	if content == "" {
		t.Error("expected embedded fallback content")
	}
}
