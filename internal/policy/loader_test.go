package policy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_EmptyPath_ReturnsDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load('') error: %v", err)
	}

	if cfg == nil {
		t.Fatal("Load('') returned nil")
	}

	if len(cfg.FileRules) != 4 {
		t.Errorf("FileRules count = %d, want 4", len(cfg.FileRules))
	}
}

func TestLoad_NonexistentPath_ReturnsDefaults(t *testing.T) {
	cfg, err := Load("/nonexistent/path/guardian.hcl")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.FileRules) != 4 {
		t.Errorf("FileRules count = %d, want 4", len(cfg.FileRules))
	}
}

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
guardian {
  dry_run       = true
  worker_count  = 10
  log_level     = "debug"
}

rule "file" "codeowners" {
  paths    = ["CODEOWNERS", ".github/CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners.tmpl"
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if !cfg.Guardian.DryRun {
		t.Error("Guardian.DryRun = false, want true")
	}

	if cfg.Guardian.WorkerCount != 10 {
		t.Errorf("Guardian.WorkerCount = %d, want 10", cfg.Guardian.WorkerCount)
	}

	if cfg.Guardian.LogLevel != "debug" {
		t.Errorf("Guardian.LogLevel = %q, want %q", cfg.Guardian.LogLevel, "debug")
	}

	if len(cfg.FileRules) != 1 {
		t.Fatalf("FileRules count = %d, want 1", len(cfg.FileRules))
	}

	if cfg.FileRules[0].Name != "codeowners" {
		t.Errorf("FileRules[0].Name = %q, want %q", cfg.FileRules[0].Name, "codeowners")
	}
}

func TestLoad_DirectoryMultipleFiles(t *testing.T) {
	dir := t.TempDir()

	file1 := `
guardian {
  dry_run      = true
  worker_count = 10
}
`

	file2 := `
rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners.tmpl"
}
`

	file3 := `
rule "file" "dependabot" {
  paths    = [".github/dependabot.yml"]
  target   = ".github/dependabot.yml"
  template = "dependabot.tmpl"
}
`

	for name, content := range map[string]string{
		"01-guardian.hcl":   file1,
		"02-codeowners.hcl": file2,
		"03-dependabot.hcl": file3,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if !cfg.Guardian.DryRun {
		t.Error("Guardian.DryRun = false, want true")
	}

	if cfg.Guardian.WorkerCount != 10 {
		t.Errorf("Guardian.WorkerCount = %d, want 10", cfg.Guardian.WorkerCount)
	}

	if len(cfg.FileRules) != 2 {
		t.Errorf("FileRules count = %d, want 2", len(cfg.FileRules))
	}
}

func TestLoad_EmptyDirectory_ReturnsDefaults(t *testing.T) {
	dir := t.TempDir()

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.FileRules) != 4 {
		t.Errorf("FileRules count = %d, want 4 (defaults)", len(cfg.FileRules))
	}
}

func TestLoad_EnvVarOverrides(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
guardian {
  worker_count = 10
  log_level    = "debug"
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	t.Setenv("WORKER_COUNT", "20")
	t.Setenv("LOG_LEVEL", "warn")

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Guardian.WorkerCount != 20 {
		t.Errorf("Guardian.WorkerCount = %d, want 20 (env override)", cfg.Guardian.WorkerCount)
	}

	if cfg.Guardian.LogLevel != "warn" {
		t.Errorf("Guardian.LogLevel = %q, want %q (env override)", cfg.Guardian.LogLevel, "warn")
	}
}

func TestLoad_OrgFromHCL(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
guardian {
  org = "testorg"
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Guardian.Org != "testorg" {
		t.Errorf("Guardian.Org = %q, want %q", cfg.Guardian.Org, "testorg")
	}
}

func TestLoad_OrgEnvOverridesHCL(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
guardian {
  org = "hcl-org"
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	t.Setenv("GITHUB_ORG", "env-org")

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Guardian.Org != "env-org" {
		t.Errorf("Guardian.Org = %q, want %q (env override)", cfg.Guardian.Org, "env-org")
	}
}

func TestLoad_InvalidHCLSyntax(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `this is not valid HCL {{{`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	_, err := Load(hclFile)
	if err == nil {
		t.Fatal("Load() expected error for invalid HCL, got nil")
	}
}

func TestLoad_FileWithAssertions(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
rule "file" "codeowners" {
  check    = "contains"
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners.tmpl"

  assertion {
    pattern = "@[a-z]+"
    message = "CODEOWNERS must have a team owner"
  }

  assertion {
    not_pattern = "@org/CHANGEME"
    message     = "CODEOWNERS must not contain placeholder"
  }
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.FileRules) != 1 {
		t.Fatalf("FileRules count = %d, want 1", len(cfg.FileRules))
	}

	rule := cfg.FileRules[0]

	if rule.CheckMode() != CheckContains {
		t.Errorf("CheckMode() = %v, want %v", rule.CheckMode(), CheckContains)
	}

	if len(rule.Assertions) != 2 {
		t.Fatalf("Assertions count = %d, want 2", len(rule.Assertions))
	}

	if rule.Assertions[0].Pattern != "@[a-z]+" {
		t.Errorf("Assertions[0].Pattern = %q, want %q", rule.Assertions[0].Pattern, "@[a-z]+")
	}

	if rule.Assertions[1].NotPattern != "@org/CHANGEME" {
		t.Errorf("Assertions[1].NotPattern = %q, want %q", rule.Assertions[1].NotPattern, "@org/CHANGEME")
	}
}

func TestLoad_GuardianDefaults_PreservedWithoutHCL(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load('') error: %v", err)
	}

	if !cfg.Guardian.SkipForks {
		t.Error("SkipForks should default to true")
	}

	if !cfg.Guardian.SkipArchived {
		t.Error("SkipArchived should default to true")
	}

	if !cfg.Guardian.WebhookIPAllowlist {
		t.Error("WebhookIPAllowlist should default to true")
	}

	if cfg.Guardian.WorkerCount != 5 {
		t.Errorf("WorkerCount = %d, want 5", cfg.Guardian.WorkerCount)
	}

	if cfg.Guardian.QueueSize != 1000 {
		t.Errorf("QueueSize = %d, want 1000", cfg.Guardian.QueueSize)
	}
}

func TestLoad_FileWithReconciler(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
rule "file" "catalog_info" {
  paths    = ["catalog-info.yaml", "catalog-info.yml"]
  target   = "catalog-info.yaml"
  template = "catalog-info.tmpl"

  reconcile "custom_properties" {
    watch = true
    mode  = "api"
  }
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.FileRules) != 1 {
		t.Fatalf("FileRules count = %d, want 1", len(cfg.FileRules))
	}

	rule := cfg.FileRules[0]

	if len(rule.Reconcilers) != 1 {
		t.Fatalf("Reconcilers count = %d, want 1", len(rule.Reconcilers))
	}

	rec := rule.Reconcilers[0]

	if rec.Type != "custom_properties" {
		t.Errorf("Reconciler.Type = %q, want %q", rec.Type, "custom_properties")
	}

	if !rec.Watch {
		t.Error("Reconciler.Watch = false, want true")
	}

	if rec.Mode != "api" {
		t.Errorf("Reconciler.Mode = %q, want %q", rec.Mode, "api")
	}
}

func TestLoad_ScheduleIntervalParsed(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
guardian {
  schedule_interval = "24h"
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	want := 24 * 60 * 60 * 1e9 // 24h in nanoseconds
	got := float64(cfg.Guardian.ParsedScheduleInterval)

	if got != want {
		t.Errorf("ParsedScheduleInterval = %v, want 24h", cfg.Guardian.ParsedScheduleInterval)
	}
}

func TestLoad_LocalsBlock(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
locals {
  org         = "myorg"
  tmpl_suffix = ".tmpl"
}

rule "file" "codeowners" {
  paths    = ["CODEOWNERS", ".github/CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners${local.tmpl_suffix}"
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.FileRules) != 1 {
		t.Fatalf("FileRules count = %d, want 1", len(cfg.FileRules))
	}

	if cfg.FileRules[0].Template != "codeowners.tmpl" {
		t.Errorf("Template = %q, want %q", cfg.FileRules[0].Template, "codeowners.tmpl")
	}
}

func TestLoad_HCLTakesPrecedenceOverCustomPropertiesEnv(t *testing.T) {
	t.Setenv("CUSTOM_PROPERTIES_MODE", "api")

	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	// HCL defines only a codeowners rule — no catalog_info.
	content := `
rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners.tmpl"
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// HCL defines 1 rule; CUSTOM_PROPERTIES_MODE should be ignored.
	if len(cfg.FileRules) != 1 {
		t.Fatalf("FileRules count = %d, want 1 (HCL should override defaults)", len(cfg.FileRules))
	}

	if cfg.FileRules[0].Name != "codeowners" {
		t.Errorf("FileRules[0].Name = %q, want %q", cfg.FileRules[0].Name, "codeowners")
	}

	for _, r := range cfg.FileRules {
		if r.Name == "catalog_info" {
			t.Error("catalog_info rule should not be present when HCL config defines rules")
		}
	}
}

func TestLoad_DirectoryIgnoresNonHCLFiles(t *testing.T) {
	dir := t.TempDir()

	hclContent := `
rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners.tmpl"
}
`

	if err := os.WriteFile(filepath.Join(dir, "rules.hcl"), []byte(hclContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# docs"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.FileRules) != 1 {
		t.Errorf("FileRules count = %d, want 1", len(cfg.FileRules))
	}
}

func TestLoad_TopLevelScope_Decoded(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
scope {
  orgs = ["myorg-prod", "myorg-staging"]
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Scope == nil {
		t.Fatal("cfg.Scope is nil, want populated")
	}

	if len(cfg.Scope.Orgs) != 2 {
		t.Fatalf("Scope.Orgs count = %d, want 2", len(cfg.Scope.Orgs))
	}

	if cfg.Scope.Orgs[0] != "myorg-prod" || cfg.Scope.Orgs[1] != "myorg-staging" {
		t.Errorf("Scope.Orgs = %v, want [myorg-prod myorg-staging]", cfg.Scope.Orgs)
	}
}

func TestLoad_NoTopLevelScope_NilScope(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
guardian {
  log_level = "info"
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Scope != nil {
		t.Errorf("cfg.Scope = %+v, want nil (legacy mode)", cfg.Scope)
	}
}

func TestLoad_TopLevelScope_AcrossDirectoryFiles(t *testing.T) {
	dir := t.TempDir()

	scopeFile := `
scope {
  orgs = ["myorg-prod"]
}
`

	rulesFile := `
guardian {
  log_level = "info"
}
`

	if err := os.WriteFile(filepath.Join(dir, "scope.hcl"), []byte(scopeFile), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "rules.hcl"), []byte(rulesFile), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Scope == nil {
		t.Fatal("cfg.Scope is nil, want populated from scope.hcl")
	}

	if len(cfg.Scope.Orgs) != 1 || cfg.Scope.Orgs[0] != "myorg-prod" {
		t.Errorf("Scope.Orgs = %v, want [myorg-prod]", cfg.Scope.Orgs)
	}
}

func TestLoad_FileRuleScope_Decoded(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  scope {
    orgs = ["myorg-prod", "myorg-staging"]
  }
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.FileRules) != 1 {
		t.Fatalf("FileRules count = %d, want 1", len(cfg.FileRules))
	}

	fr := cfg.FileRules[0]
	if fr.Scope == nil {
		t.Fatal("FileRules[0].Scope is nil, want populated")
	}

	if len(fr.Scope.Orgs) != 2 {
		t.Fatalf("FileRules[0].Scope.Orgs count = %d, want 2", len(fr.Scope.Orgs))
	}

	if fr.Scope.Orgs[0] != "myorg-prod" || fr.Scope.Orgs[1] != "myorg-staging" {
		t.Errorf("FileRules[0].Scope.Orgs = %v, want [myorg-prod myorg-staging]", fr.Scope.Orgs)
	}
}

func TestLoad_SettingRuleScope_Decoded(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
rule "setting" "vuln_alerts" {
  property = "vulnerability_alerts_enabled"
  expected = true

  scope {
    orgs = ["myorg-prod"]
  }
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.SettingRules) != 1 {
		t.Fatalf("SettingRules count = %d, want 1", len(cfg.SettingRules))
	}

	sr := cfg.SettingRules[0]
	if sr.Scope == nil {
		t.Fatal("SettingRules[0].Scope is nil, want populated")
	}

	if len(sr.Scope.Orgs) != 1 || sr.Scope.Orgs[0] != "myorg-prod" {
		t.Errorf("SettingRules[0].Scope.Orgs = %v, want [myorg-prod]", sr.Scope.Orgs)
	}
}

func TestLoad_BranchProtectionRuleScope_Decoded(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
rule "branch_protection" "main_protected" {
  branch     = "main"
  require_pr = true

  scope {
    orgs = ["myorg-prod"]
  }
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.BranchProtectionRules) != 1 {
		t.Fatalf("BranchProtectionRules count = %d, want 1", len(cfg.BranchProtectionRules))
	}

	bp := cfg.BranchProtectionRules[0]
	if bp.Scope == nil {
		t.Fatal("BranchProtectionRules[0].Scope is nil, want populated")
	}

	if len(bp.Scope.Orgs) != 1 || bp.Scope.Orgs[0] != "myorg-prod" {
		t.Errorf("BranchProtectionRules[0].Scope.Orgs = %v, want [myorg-prod]", bp.Scope.Orgs)
	}
}

func TestLoad_RuleScopeWithUniversal(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  scope {
    orgs = ["*"]
  }
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	fr := cfg.FileRules[0]
	if fr.Scope == nil {
		t.Fatal("FileRules[0].Scope is nil, want populated")
	}

	// "*" must be preserved verbatim — the runtime gate uses HasUniversal
	// to detect it, but the loader does not expand it.
	if len(fr.Scope.Orgs) != 1 || fr.Scope.Orgs[0] != "*" {
		t.Errorf("FileRules[0].Scope.Orgs = %v, want [*] preserved verbatim", fr.Scope.Orgs)
	}

	if !fr.Scope.HasUniversal() {
		t.Error("HasUniversal() returned false for orgs = [\"*\"]")
	}
}
