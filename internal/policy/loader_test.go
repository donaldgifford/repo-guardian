package policy

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

func TestLoad_StrictMode_TopLevelEmptyOrgs(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
scope {
  orgs = []
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(hclFile)
	if err == nil {
		t.Fatal("expected error for empty top-level scope.orgs")
	}

	if !strings.Contains(err.Error(), "top-level scope must declare at least one org") {
		t.Errorf("error %q does not match expected message", err)
	}
}

func TestLoad_StrictMode_DuplicateTopLevelScope_Directory(t *testing.T) {
	dir := t.TempDir()

	scope1 := `
scope {
  orgs = ["myorg-prod"]
}
`

	scope2 := `
scope {
  orgs = ["myorg-staging"]
}
`

	if err := os.WriteFile(filepath.Join(dir, "scope1.hcl"), []byte(scope1), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "scope2.hcl"), []byte(scope2), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for duplicate top-level scope blocks")
	}

	if !strings.Contains(err.Error(), "only one top-level scope block allowed") {
		t.Errorf("error %q does not match expected message", err)
	}
}

func TestLoad_StrictMode_FileRuleMissingScope(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
scope {
  orgs = ["myorg-prod"]
}

rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(hclFile)
	if err == nil {
		t.Fatal("expected error for file rule missing scope in strict mode")
	}

	if !strings.Contains(err.Error(), `"codeowners"`) ||
		!strings.Contains(err.Error(), "must declare scope in strict mode") {
		t.Errorf("error %q does not match expected message", err)
	}
}

func TestLoad_StrictMode_SettingRuleMissingScope(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
scope {
  orgs = ["myorg-prod"]
}

rule "setting" "vuln_alerts" {
  property = "vulnerability_alerts_enabled"
  expected = true
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(hclFile)
	if err == nil {
		t.Fatal("expected error for setting rule missing scope in strict mode")
	}

	if !strings.Contains(err.Error(), `"vuln_alerts"`) ||
		!strings.Contains(err.Error(), "must declare scope in strict mode") {
		t.Errorf("error %q does not match expected message", err)
	}
}

func TestLoad_StrictMode_BranchProtectionRuleMissingScope(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
scope {
  orgs = ["myorg-prod"]
}

rule "branch_protection" "main_protected" {
  branch     = "main"
  require_pr = true
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(hclFile)
	if err == nil {
		t.Fatal("expected error for branch_protection rule missing scope in strict mode")
	}

	if !strings.Contains(err.Error(), `"main_protected"`) ||
		!strings.Contains(err.Error(), "must declare scope in strict mode") {
		t.Errorf("error %q does not match expected message", err)
	}
}

func TestLoad_StrictMode_RuleEmptyScopeOrgs(t *testing.T) {
	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
scope {
  orgs = ["myorg-prod"]
}

rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  scope {
    orgs = []
  }
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(hclFile)
	if err == nil {
		t.Fatal("expected error for rule with empty scope.orgs")
	}

	if !strings.Contains(err.Error(), `"codeowners"`) ||
		!strings.Contains(err.Error(), "scope.orgs must not be empty") {
		t.Errorf("error %q does not match expected message", err)
	}
}

func TestLoad_LegacyMode_PerRuleScopePresent_Warns(t *testing.T) {
	// Capture slog output to verify the warning fires.
	var buf bytes.Buffer

	originalLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	t.Cleanup(func() {
		slog.SetDefault(originalLogger)
	})

	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	// No top-level scope, but two rules declare per-rule scope. The warning
	// should fire exactly once even with multiple offending rules.
	content := `
rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"
  scope {
    orgs = ["myorg-prod"]
  }
}

rule "file" "dependabot" {
  paths    = [".github/dependabot.yml"]
  target   = ".github/dependabot.yml"
  template = "dependabot"
  scope {
    orgs = ["myorg-staging"]
  }
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	output := buf.String()
	count := strings.Count(output, "per-rule scope ignored")

	if count != 1 {
		t.Errorf("expected exactly 1 legacy-mode warning, got %d. Output:\n%s", count, output)
	}
}

func TestLoad_LegacyMode_NoPerRuleScope_NoWarning(t *testing.T) {
	var buf bytes.Buffer

	originalLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	t.Cleanup(func() {
		slog.SetDefault(originalLogger)
	})

	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")

	content := `
rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if strings.Contains(buf.String(), "per-rule scope ignored") {
		t.Errorf("unexpected legacy-mode warning when no per-rule scope present:\n%s", buf.String())
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

// writeGuardianHCL writes a single-file policy whose guardian block body is
// the given snippet and returns the file path.
func writeGuardianHCL(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")
	content := "guardian {\n" + body + "\n}\n"

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	return hclFile
}

func TestLoad_AutoClosePR_FromHCL(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantSet     bool
		wantEnabled bool
	}{
		{name: "explicit false", body: `auto_close_pr = false`, wantSet: true, wantEnabled: false},
		{name: "explicit true", body: `auto_close_pr = true`, wantSet: true, wantEnabled: true},
		{name: "unset defaults true", body: `log_level = "info"`, wantSet: false, wantEnabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(writeGuardianHCL(t, tt.body))
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}

			if got := cfg.Guardian.AutoClosePR != nil; got != tt.wantSet {
				t.Errorf("AutoClosePR set = %v, want %v", got, tt.wantSet)
			}

			if got := cfg.Guardian.AutoClosePREnabled(); got != tt.wantEnabled {
				t.Errorf("AutoClosePREnabled() = %v, want %v", got, tt.wantEnabled)
			}
		})
	}
}

// Env override must win over the HCL attribute because applyEnvOverrides
// runs last in Load. Cannot use t.Parallel with t.Setenv.
func TestLoad_AutoClosePR_EnvOverridesHCL(t *testing.T) {
	t.Setenv("AUTO_CLOSE_PR", "true")

	cfg, err := Load(writeGuardianHCL(t, `auto_close_pr = false`))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if !cfg.Guardian.AutoClosePREnabled() {
		t.Error("AutoClosePREnabled() = false, want true (env must override HCL)")
	}
}

// The guardian block decodes through a strict schema (INV-0010): unknown
// attributes — typos or stale config like the historical `org` — must fail
// load instead of being silently ignored.
func TestLoad_Guardian_UnknownAttribute_Fails(t *testing.T) {
	_, err := Load(writeGuardianHCL(t, `org = "myorg"`))
	if err == nil {
		t.Fatal("Load() succeeded, want unsupported-argument error")
	}

	if !strings.Contains(err.Error(), "org") {
		t.Errorf("error %q does not name the offending attribute", err)
	}
}

// writeReconcileHCL writes a single-file policy with one catalog_info file
// rule whose reconcile "custom_properties" block body is the given snippet.
func writeReconcileHCL(t *testing.T, reconcileBody string) string {
	t.Helper()

	dir := t.TempDir()
	hclFile := filepath.Join(dir, "guardian.hcl")
	content := `
rule "file" "catalog_info" {
  paths    = ["catalog-info.yaml"]
  target   = "catalog-info.yaml"
  template = "catalog-info"

  reconcile "custom_properties" {
    mode = "api"
` + reconcileBody + `
  }
}
`

	if err := os.WriteFile(hclFile, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	return hclFile
}

func TestLoad_AnnotationProperties_Decodes(t *testing.T) {
	hclFile := writeReconcileHCL(t, `
    annotation_properties = {
      "jira/project-key" = "JiraProject"
      "jira/label"        = "JiraLabel"
    }
`)

	cfg, err := Load(hclFile)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	rec := cfg.FileRules[0].Reconcilers[0]

	want := map[string]string{
		"jira/project-key": "JiraProject",
		"jira/label":       "JiraLabel",
	}

	if len(rec.AnnotationProperties) != len(want) {
		t.Fatalf("AnnotationProperties = %v, want %v", rec.AnnotationProperties, want)
	}

	for k, v := range want {
		if rec.AnnotationProperties[k] != v {
			t.Errorf("AnnotationProperties[%q] = %q, want %q", k, rec.AnnotationProperties[k], v)
		}
	}
}

func TestLoad_AnnotationProperties_AbsentIsNil(t *testing.T) {
	cfg, err := Load(writeReconcileHCL(t, ""))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	rec := cfg.FileRules[0].Reconcilers[0]

	if rec.AnnotationProperties != nil {
		t.Errorf("AnnotationProperties = %v, want nil when attribute absent", rec.AnnotationProperties)
	}
}

func TestLoad_AnnotationProperties_EmptyMapIsEmpty(t *testing.T) {
	cfg, err := Load(writeReconcileHCL(t, `annotation_properties = {}`))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	rec := cfg.FileRules[0].Reconcilers[0]

	if len(rec.AnnotationProperties) != 0 {
		t.Errorf("AnnotationProperties = %v, want empty", rec.AnnotationProperties)
	}
}

func TestLoad_AnnotationProperties_NonStringValue_Fails(t *testing.T) {
	_, err := Load(writeReconcileHCL(t, `
    annotation_properties = {
      "jira/project-key" = 42
    }
`))
	if err == nil {
		t.Fatal("Load() succeeded, want error for non-string annotation_properties value")
	}

	if !strings.Contains(err.Error(), "annotation_properties") {
		t.Errorf("error %q does not mention annotation_properties", err)
	}
}

// TestLoad_AnnotationProperties_NullValues_FailCleanly is the INV-0011
// A8 regression. Two cty shapes reach a panicking accessor without a
// dedicated null/unknown check: a *typed* null map (from a conditional
// whose branches unify) satisfies IsMapType/IsObjectType and then
// panics in AsValueMap, and a null string value satisfies
// `v.Type() == cty.String` and then panics in AsString. A bad policy
// must fail load with a diagnostic, never take the process down.
func TestLoad_AnnotationProperties_NullValues_FailCleanly(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "bare null",
			body: `annotation_properties = null`,
		},
		{
			name: "typed-null map from a conditional",
			body: `annotation_properties = false ? { "jira/label" = "JiraLabel" } : null`,
		},
		{
			name: "null string value",
			body: `annotation_properties = { "jira/label" = null }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A panic here fails the test rather than crashing the run.
			_, err := Load(writeReconcileHCL(t, "\n    "+tt.body+"\n"))
			if err == nil {
				t.Fatalf("Load() succeeded, want error for %s", tt.name)
			}

			if !strings.Contains(err.Error(), "annotation_properties") {
				t.Errorf("error %q does not mention annotation_properties", err)
			}
		})
	}
}

// TestLoad_AnnotationProperties_ListValue_FailsCleanly guards against a
// panic regression: cty.Value.CanIterateElements() is also true for
// List/Set/Tuple types, whose AsValueMap() panics internally (it calls
// AsString on a numeric index key, not a string key). The decode guard
// must check IsObjectType/IsMapType specifically instead.
func TestLoad_AnnotationProperties_ListValue_FailsCleanly(t *testing.T) {
	_, err := Load(writeReconcileHCL(t, `
    annotation_properties = ["a", "b"]
`))
	if err == nil {
		t.Fatal("Load() succeeded, want error for list-typed annotation_properties")
	}

	if !strings.Contains(err.Error(), "annotation_properties") {
		t.Errorf("error %q does not mention annotation_properties", err)
	}
}

// TestLoad_OrphanCleanup_FromHCL closes the INV-0010 trap for the
// INV-0014 kill switch: adding a GuardianConfig field needs the schema
// attribute, the setGuardianAttr case AND the mergeGuardianConfig carry
// in lockstep. auto_close_pr shipped with the field and the env override
// but neither of the other two, so the HCL attribute silently did
// nothing for four releases. This test drives the HCL path specifically,
// which is the one that broke.
func TestLoad_OrphanCleanup_FromHCL(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantSet     bool
		wantEnabled bool
	}{
		{name: "explicit false", body: `orphan_cleanup = false`, wantSet: true, wantEnabled: false},
		{name: "explicit true", body: `orphan_cleanup = true`, wantSet: true, wantEnabled: true},
		{name: "unset defaults true", body: `log_level = "info"`, wantSet: false, wantEnabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(writeGuardianHCL(t, tt.body))
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}

			if got := cfg.Guardian.OrphanCleanup != nil; got != tt.wantSet {
				t.Errorf("OrphanCleanup set = %v, want %v", got, tt.wantSet)
			}

			if got := cfg.Guardian.OrphanCleanupEnabled(); got != tt.wantEnabled {
				t.Errorf("OrphanCleanupEnabled() = %v, want %v", got, tt.wantEnabled)
			}
		})
	}
}

// Env override must win over the HCL attribute because applyEnvOverrides
// runs last in Load. Cannot use t.Parallel with t.Setenv.
func TestLoad_OrphanCleanup_EnvOverridesHCL(t *testing.T) {
	t.Setenv("ORPHAN_CLEANUP", "false")

	cfg, err := Load(writeGuardianHCL(t, `orphan_cleanup = true`))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Guardian.OrphanCleanupEnabled() {
		t.Error("ORPHAN_CLEANUP=false must override orphan_cleanup = true in HCL")
	}
}
