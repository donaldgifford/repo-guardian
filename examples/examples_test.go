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
}
