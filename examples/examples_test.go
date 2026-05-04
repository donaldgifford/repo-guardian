package examples_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/policy"
)

func examplesDir() string {
	_, file, _, _ := runtime.Caller(0)

	return filepath.Dir(file)
}

func TestExampleHCL_Minimal(t *testing.T) {
	cfg, err := policy.Load(filepath.Join(examplesDir(), "guardian-minimal.hcl"))
	if err != nil {
		t.Fatalf("Load guardian-minimal.hcl: %v", err)
	}

	if len(cfg.FileRules) != 2 {
		t.Errorf("FileRules count = %d, want 2 (codeowners + dependabot)", len(cfg.FileRules))
	}
}

func TestExampleHCL_Renovate(t *testing.T) {
	cfg, err := policy.Load(filepath.Join(examplesDir(), "guardian-renovate.hcl"))
	if err != nil {
		t.Fatalf("Load guardian-renovate.hcl: %v", err)
	}

	if len(cfg.FileRules) != 4 {
		t.Errorf("FileRules count = %d, want 4", len(cfg.FileRules))
	}

	if cfg.Guardian.Org != "myorg" {
		t.Errorf("Guardian.Org = %q, want %q", cfg.Guardian.Org, "myorg")
	}
}

func TestExampleHCL_Full(t *testing.T) {
	cfg, err := policy.Load(filepath.Join(examplesDir(), "guardian-full.hcl"))
	if err != nil {
		t.Fatalf("Load guardian-full.hcl: %v", err)
	}

	if len(cfg.FileRules) != 6 {
		t.Errorf("FileRules count = %d, want 6", len(cfg.FileRules))
	}

	if len(cfg.SettingRules) != 4 {
		t.Errorf("SettingRules count = %d, want 4", len(cfg.SettingRules))
	}

	if len(cfg.BranchProtectionRules) != 1 {
		t.Errorf("BranchProtectionRules count = %d, want 1", len(cfg.BranchProtectionRules))
	}

	if len(cfg.IgnoreList.Repos) != 3 {
		t.Errorf("IgnoreList.Repos count = %d, want 3", len(cfg.IgnoreList.Repos))
	}

	// IMPL-0012 grammar assertions: defaults.pr, per-rule pr (partial
	// override), per-reconciler pr (inherits=false). Locks in that
	// the example actually exercises the Phase 4 grammar.
	if cfg.Defaults == nil || cfg.Defaults.PR == nil {
		t.Fatal("expected defaults.pr block to be parsed")
	}

	if cfg.Defaults.PR.CompiledTitle == nil {
		t.Error("defaults.pr.title not compiled")
	}

	if len(cfg.Defaults.PR.Labels) == 0 {
		t.Error("defaults.pr.labels is empty")
	}

	rulePR := cfg.RulePR("codeowners")
	if rulePR == nil {
		t.Fatal("expected RulePR(codeowners) to resolve")
	}

	if rulePR.Title == nil {
		t.Error("rule \"codeowners\".pr.title not resolved")
	}

	// Reconciler PR with inherits=false should NOT inherit defaults.
	recPR := cfg.ReconcilerPR("catalog_info", "custom_properties")
	if recPR == nil {
		t.Fatal("expected ReconcilerPR(catalog_info, custom_properties) to resolve")
	}

	if recPR.Title == nil {
		t.Error("reconciler.pr.title not resolved")
	}
}

func TestExampleHCL_MultiOrg(t *testing.T) {
	cfg, err := policy.Load(filepath.Join(examplesDir(), "guardian-multi-org.hcl"))
	if err != nil {
		t.Fatalf("Load guardian-multi-org.hcl: %v", err)
	}

	if cfg.Scope == nil {
		t.Fatal("expected top-level scope to engage strict mode")
	}

	if len(cfg.Scope.Orgs) != 2 {
		t.Errorf("Scope.Orgs count = %d, want 2", len(cfg.Scope.Orgs))
	}

	if len(cfg.FileRules) != 2 {
		t.Errorf("FileRules count = %d, want 2", len(cfg.FileRules))
	}

	for _, r := range cfg.FileRules {
		if r.Scope == nil {
			t.Errorf("FileRule %q missing scope (strict mode)", r.Name)
		}
	}

	for _, r := range cfg.SettingRules {
		if r.Scope == nil {
			t.Errorf("SettingRule %q missing scope (strict mode)", r.Name)
		}
	}

	for _, r := range cfg.BranchProtectionRules {
		if r.Scope == nil {
			t.Errorf("BranchProtectionRule %q missing scope (strict mode)", r.Name)
		}
	}
}

func TestExampleHCL_MultiOrgDirectory(t *testing.T) {
	cfg, err := policy.Load(filepath.Join(examplesDir(), "guardian-multi-org"))
	if err != nil {
		t.Fatalf("Load guardian-multi-org/: %v", err)
	}

	if cfg.Scope == nil {
		t.Fatal("expected top-level scope merged from scope.hcl")
	}

	if len(cfg.FileRules) != 2 {
		t.Errorf("FileRules count = %d, want 2 (from shared.hcl)", len(cfg.FileRules))
	}

	if len(cfg.SettingRules) != 2 {
		t.Errorf("SettingRules count = %d, want 2 (prod + staging)", len(cfg.SettingRules))
	}

	if len(cfg.BranchProtectionRules) != 1 {
		t.Errorf("BranchProtectionRules count = %d, want 1 (prod only)", len(cfg.BranchProtectionRules))
	}
}
