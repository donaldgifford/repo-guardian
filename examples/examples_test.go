package examples_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/policy"
	"github.com/donaldgifford/repo-guardian/internal/rules"
)

func examplesDir() string {
	_, file, _, _ := runtime.Caller(0)

	return filepath.Dir(file)
}

// allExampleConfigs lists every loadable example config (files and
// directories). New examples MUST be added here so the sweeping
// validity tests below cover them.
func allExampleConfigs() []string {
	return []string{
		"guardian-minimal.hcl",
		"guardian-renovate.hcl",
		"guardian-full.hcl",
		"guardian-multi-org.hcl",
		"guardian-enterprise.hcl",
		"guardian-multi-org", // directory-style config
	}
}

// TestExampleHCL_AllExamples_PRTemplatesStrict runs the strict PR
// template validator over every example. This catches variables that
// don't exist in the PR render context (e.g. `.Org`, which is
// file-template-only) — without this lock an example deploys clean and
// fails at render time on the first actionable repo.
func TestExampleHCL_AllExamples_PRTemplatesStrict(t *testing.T) {
	for _, name := range allExampleConfigs() {
		t.Run(name, func(t *testing.T) {
			cfg, err := policy.Load(filepath.Join(examplesDir(), name))
			if err != nil {
				t.Fatalf("Load %s: %v", name, err)
			}

			if err := policy.ValidatePRTemplates(cfg); err != nil {
				t.Errorf("ValidatePRTemplates(%s): %v", name, err)
			}
		})
	}
}

// TestExampleHCL_AllExamples_TemplateNamesResolve checks that every
// file-rule template name referenced by an example resolves against the
// embedded TemplateStore. Template resolution happens at check time,
// not load time, so a typo (e.g. "renovate-config" instead of
// "renovate") loads clean and then errors on the first actionable repo.
// Examples that intentionally reference operator-supplied templates
// (chart templates.files) must be allowlisted here with a comment.
func TestExampleHCL_AllExamples_TemplateNamesResolve(t *testing.T) {
	store := rules.NewTemplateStore()
	if err := store.Load(""); err != nil {
		t.Fatalf("loading embedded templates: %v", err)
	}

	// name → reason it is operator-supplied rather than embedded.
	operatorSupplied := map[string]string{}

	for _, name := range allExampleConfigs() {
		t.Run(name, func(t *testing.T) {
			cfg, err := policy.Load(filepath.Join(examplesDir(), name))
			if err != nil {
				t.Fatalf("Load %s: %v", name, err)
			}

			for i := range cfg.FileRules {
				r := &cfg.FileRules[i]

				// Absent rules forbid templates at load; nothing to resolve.
				if r.CheckMode() == policy.CheckAbsent {
					continue
				}

				if _, ok := operatorSupplied[r.Template]; ok {
					continue
				}

				if _, err := store.Get(r.Template); err != nil {
					t.Errorf("rule %q references template %q, not in the embedded set: %v", r.Name, r.Template, err)
				}
			}
		})
	}
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
}

func TestExampleHCL_Full(t *testing.T) {
	cfg, err := policy.Load(filepath.Join(examplesDir(), "guardian-full.hcl"))
	if err != nil {
		t.Fatalf("Load guardian-full.hcl: %v", err)
	}

	if len(cfg.FileRules) != 7 {
		t.Errorf("FileRules count = %d, want 7 (incl. no_dependabot absent rule)", len(cfg.FileRules))
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

func TestExampleHCL_Enterprise(t *testing.T) {
	cfg, err := policy.Load(filepath.Join(examplesDir(), "guardian-enterprise.hcl"))
	if err != nil {
		t.Fatalf("Load guardian-enterprise.hcl: %v", err)
	}

	if cfg.Scope == nil {
		t.Fatal("expected top-level scope to engage strict mode")
	}

	if len(cfg.Scope.Orgs) != 3 {
		t.Errorf("Scope.Orgs count = %d, want 3", len(cfg.Scope.Orgs))
	}

	// Three universal-scope baseline rules (codeowners + dependabot +
	// catalog_info) and the myent-product trio (renovate workflow +
	// config + gated no_dependabot absent rule).
	if len(cfg.FileRules) != 6 {
		t.Errorf("FileRules count = %d, want 6", len(cfg.FileRules))
	}

	if len(cfg.SettingRules) != 1 {
		t.Errorf("SettingRules count = %d, want 1 (platform vuln-alerts)", len(cfg.SettingRules))
	}

	if len(cfg.BranchProtectionRules) != 1 {
		t.Errorf("BranchProtectionRules count = %d, want 1 (platform main)", len(cfg.BranchProtectionRules))
	}

	// Strict mode: every rule must declare its own scope.
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

	// PR template grammar: defaults.pr, per-rule partial override, and
	// per-reconciler inherits=false — the surface the enterprise example
	// documents (PRVars vs FileVars variable sets).
	if cfg.Defaults == nil || cfg.Defaults.PR == nil {
		t.Fatal("expected defaults.pr block to be parsed")
	}

	if cfg.Defaults.PR.CompiledTitle == nil {
		t.Error("defaults.pr.title not compiled")
	}

	if rulePR := cfg.RulePR("codeowners"); rulePR == nil || rulePR.Title == nil {
		t.Error("expected RulePR(codeowners) to resolve with a title")
	}

	if recPR := cfg.ReconcilerPR("catalog_info", "custom_properties"); recPR == nil || recPR.Title == nil {
		t.Error("expected ReconcilerPR(catalog_info, custom_properties) to resolve with a title")
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
