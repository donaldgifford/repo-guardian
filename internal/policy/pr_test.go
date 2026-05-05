package policy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/policy"
	tmpl "github.com/donaldgifford/repo-guardian/internal/template"
)

func loadHCLString(t *testing.T, content string) (*policy.PolicyConfig, error) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "guardian.hcl")

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing test HCL: %v", err)
	}

	return policy.Load(path)
}

func mustLoadHCL(t *testing.T, content string) *policy.PolicyConfig {
	t.Helper()

	cfg, err := loadHCLString(t, content)
	if err != nil {
		t.Fatalf("loading HCL: %v", err)
	}

	return cfg
}

func compileTitle(t *testing.T, body string) *tmpl.Compiled {
	t.Helper()

	c, err := tmpl.NewRenderer().Parse("title", body)
	if err != nil {
		t.Fatalf("compiling title fixture: %v", err)
	}

	return c
}

func compileBody(t *testing.T, body string) *tmpl.Compiled {
	t.Helper()

	c, err := tmpl.NewRenderer().Parse("body", body)
	if err != nil {
		t.Fatalf("compiling body fixture: %v", err)
	}

	return c
}

func renderTitle(t *testing.T, pt *policy.PRTemplate) string {
	t.Helper()

	if pt == nil || pt.Title == nil {
		return ""
	}

	out, err := pt.Title.Render(tmpl.PRVars{})
	if err != nil {
		t.Fatalf("rendering title: %v", err)
	}

	return out
}

func renderBody(t *testing.T, pt *policy.PRTemplate) string {
	t.Helper()

	if pt == nil || pt.Body == nil {
		return ""
	}

	out, err := pt.Body.Render(tmpl.PRVars{})
	if err != nil {
		t.Fatalf("rendering body: %v", err)
	}

	return out
}

func TestResolveRulePR_NoRulePR_FallsBackToDefaults(t *testing.T) {
	t.Parallel()

	defaults := &policy.PRTemplate{
		Title:    compileTitle(t, "default title"),
		Body:     compileBody(t, "default body"),
		Inherits: true,
	}

	got := policy.ResolveRulePR(nil, defaults)

	if renderTitle(t, got) != "default title" {
		t.Errorf("expected defaults to flow through as title, got %q", renderTitle(t, got))
	}

	if renderBody(t, got) != "default body" {
		t.Errorf("expected defaults to flow through as body, got %q", renderBody(t, got))
	}
}

func TestResolveRulePR_NoDefaults_ReturnsRuleVerbatim(t *testing.T) {
	t.Parallel()

	rule := &policy.PRTemplate{
		Title:    compileTitle(t, "rule title"),
		Body:     compileBody(t, "rule body"),
		Inherits: true,
	}

	got := policy.ResolveRulePR(rule, nil)

	if renderTitle(t, got) != "rule title" {
		t.Errorf("got %q, want %q", renderTitle(t, got), "rule title")
	}
}

func TestResolveRulePR_FieldByFieldMerge(t *testing.T) {
	t.Parallel()

	defaults := &policy.PRTemplate{
		Title:     compileTitle(t, "default title"),
		Body:      compileBody(t, "default body"),
		Labels:    []string{"automated"},
		LabelsSet: true,
		Inherits:  true,
	}

	rule := &policy.PRTemplate{
		Title:    compileTitle(t, "rule title"),
		Inherits: true,
	}

	got := policy.ResolveRulePR(rule, defaults)

	if renderTitle(t, got) != "rule title" {
		t.Errorf("rule title should win, got %q", renderTitle(t, got))
	}

	if renderBody(t, got) != "default body" {
		t.Errorf("body should inherit from defaults, got %q", renderBody(t, got))
	}

	if len(got.Labels) != 1 || got.Labels[0] != "automated" {
		t.Errorf("labels should inherit from defaults, got %v", got.Labels)
	}
}

func TestResolveRulePR_InheritsFalse_ShortCircuits(t *testing.T) {
	t.Parallel()

	defaults := &policy.PRTemplate{
		Title:     compileTitle(t, "default title"),
		Body:      compileBody(t, "default body"),
		Labels:    []string{"automated"},
		LabelsSet: true,
		Inherits:  true,
	}

	rule := &policy.PRTemplate{
		Title:    compileTitle(t, "rule title"),
		Inherits: false,
	}

	got := policy.ResolveRulePR(rule, defaults)

	if renderTitle(t, got) != "rule title" {
		t.Errorf("rule title should win, got %q", renderTitle(t, got))
	}

	if renderBody(t, got) != "" {
		t.Errorf("body should NOT inherit when inherits=false, got %q", renderBody(t, got))
	}

	if got.LabelsSet {
		t.Errorf("labels should NOT inherit when inherits=false, got LabelsSet=true with %v", got.Labels)
	}
}

func TestResolveRulePR_LabelsExplicitEmpty_BlocksInheritance(t *testing.T) {
	t.Parallel()

	defaults := &policy.PRTemplate{
		Labels:    []string{"automated", "guardian"},
		LabelsSet: true,
		Inherits:  true,
	}

	rule := &policy.PRTemplate{
		Labels:    []string{},
		LabelsSet: true,
		Inherits:  true,
	}

	got := policy.ResolveRulePR(rule, defaults)

	if !got.LabelsSet {
		t.Fatal("LabelsSet should be true after explicit empty override")
	}

	if len(got.Labels) != 0 {
		t.Errorf("labels should be empty (explicit override), got %v", got.Labels)
	}
}

func TestResolveReconcilerPR_SkipsRulePR(t *testing.T) {
	t.Parallel()

	defaults := &policy.PRTemplate{
		Title:    compileTitle(t, "default title"),
		Inherits: true,
	}

	reconciler := &policy.PRTemplate{
		Body:     compileBody(t, "reconciler body"),
		Inherits: true,
	}

	got := policy.ResolveReconcilerPR(reconciler, defaults)

	if renderTitle(t, got) != "default title" {
		t.Errorf("title should inherit from defaults (reconciler skips rule.pr), got %q", renderTitle(t, got))
	}

	if renderBody(t, got) != "reconciler body" {
		t.Errorf("reconciler body should win, got %q", renderBody(t, got))
	}
}

func TestPolicyConfig_DefaultsPR(t *testing.T) {
	t.Parallel()

	titleStr := "default title"

	cfg := &policy.PolicyConfig{
		Defaults: &policy.DefaultsConfig{
			PR: &policy.PRConfig{
				Title:         &titleStr,
				CompiledTitle: compileTitle(t, "default title"),
			},
		},
	}

	got := cfg.DefaultsPR()

	if renderTitle(t, got) != "default title" {
		t.Errorf("DefaultsPR title: got %q", renderTitle(t, got))
	}
}

func TestPolicyConfig_DefaultsPR_NilWhenAbsent(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{}

	if got := cfg.DefaultsPR(); got != nil {
		t.Errorf("expected nil DefaultsPR for absent defaults, got %+v", got)
	}
}

func TestPolicyConfig_RulePR_MergesWithDefaults(t *testing.T) {
	t.Parallel()

	defTitle := "default"
	ruleTitle := "rule override"

	cfg := &policy.PolicyConfig{
		Defaults: &policy.DefaultsConfig{
			PR: &policy.PRConfig{
				Title:         &defTitle,
				CompiledTitle: compileTitle(t, defTitle),
			},
		},
		FileRules: []policy.FileRuleConfig{
			{
				Name: "codeowners",
				PR: &policy.PRConfig{
					Title:         &ruleTitle,
					CompiledTitle: compileTitle(t, ruleTitle),
				},
			},
		},
	}

	got := cfg.RulePR("codeowners")

	if renderTitle(t, got) != "rule override" {
		t.Errorf("RulePR title: got %q, want %q", renderTitle(t, got), "rule override")
	}
}

func TestPolicyConfig_ReconcilerPR_SkipsRule(t *testing.T) {
	t.Parallel()

	defTitle := "default"
	ruleTitle := "rule should not flow into reconciler"
	recBody := "reconciler body"

	cfg := &policy.PolicyConfig{
		Defaults: &policy.DefaultsConfig{
			PR: &policy.PRConfig{
				Title:         &defTitle,
				CompiledTitle: compileTitle(t, defTitle),
			},
		},
		FileRules: []policy.FileRuleConfig{
			{
				Name: "catalog_info",
				PR: &policy.PRConfig{
					Title:         &ruleTitle,
					CompiledTitle: compileTitle(t, ruleTitle),
				},
				Reconcilers: []policy.ReconcilerConfig{
					{
						Type: "custom_properties",
						PR: &policy.PRConfig{
							Body:         &recBody,
							CompiledBody: compileBody(t, recBody),
						},
					},
				},
			},
		},
	}

	got := cfg.ReconcilerPR("catalog_info", "custom_properties")

	if renderTitle(t, got) != "default" {
		t.Errorf("reconciler title should come from defaults (skipping rule.pr), got %q", renderTitle(t, got))
	}

	if renderBody(t, got) != "reconciler body" {
		t.Errorf("reconciler body should win, got %q", renderBody(t, got))
	}
}

func TestPolicyConfig_ReconcilerPR_FallsBackToDefaultsWhenAbsent(t *testing.T) {
	t.Parallel()

	defTitle := "default"

	cfg := &policy.PolicyConfig{
		Defaults: &policy.DefaultsConfig{
			PR: &policy.PRConfig{
				Title:         &defTitle,
				CompiledTitle: compileTitle(t, defTitle),
			},
		},
	}

	got := cfg.ReconcilerPR("missing_rule", "missing_reconciler")

	if renderTitle(t, got) != "default" {
		t.Errorf("expected defaults fallback for unknown rule, got %q", renderTitle(t, got))
	}
}

func TestLoad_PRBlock_Defaults_Compiles(t *testing.T) {
	t.Parallel()

	hcl := `
defaults {
  pr {
    title = "default title for {{ .Repo }}"
    body = "default body"
    labels = ["automated", "guardian"]
  }
}
`
	cfg := mustLoadHCL(t, hcl)

	if cfg.Defaults == nil || cfg.Defaults.PR == nil {
		t.Fatalf("expected defaults.pr to be parsed; got %+v", cfg.Defaults)
	}

	if cfg.Defaults.PR.CompiledTitle == nil {
		t.Fatal("expected CompiledTitle to be populated")
	}

	if cfg.Defaults.PR.CompiledBody == nil {
		t.Fatal("expected CompiledBody to be populated")
	}

	if !cfg.Defaults.PR.LabelsSet {
		t.Error("expected LabelsSet=true after explicit labels declaration")
	}

	if len(cfg.Defaults.PR.Labels) != 2 {
		t.Errorf("expected 2 labels, got %d", len(cfg.Defaults.PR.Labels))
	}
}

func TestLoad_PRBlock_RuleAndReconciler_Compile(t *testing.T) {
	t.Parallel()

	hcl := `
rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  pr {
    title    = "chore: add CODEOWNERS for {{ .Repo }}"
    inherits = false
  }

  reconcile "custom_properties" {
    mode = "api"

    pr {
      body = "Reconciler-specific body"
    }
  }
}
`
	cfg := mustLoadHCL(t, hcl)

	if len(cfg.FileRules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(cfg.FileRules))
	}

	rule := cfg.FileRules[0]

	if rule.PR == nil || rule.PR.CompiledTitle == nil {
		t.Fatal("rule.pr.CompiledTitle should be set")
	}

	if rule.PR.Inherits == nil || *rule.PR.Inherits {
		t.Errorf("expected inherits=false on rule.pr, got %v", rule.PR.Inherits)
	}

	if len(rule.Reconcilers) != 1 {
		t.Fatalf("expected 1 reconciler, got %d", len(rule.Reconcilers))
	}

	rec := rule.Reconcilers[0]
	if rec.PR == nil || rec.PR.CompiledBody == nil {
		t.Fatal("reconciler.pr.CompiledBody should be set")
	}
}

func TestLoad_PRBlock_ParseError_ReturnsLocationPrefix(t *testing.T) {
	t.Parallel()

	// Unterminated Go-template action — fails at parse time.
	hcl := `
defaults {
  pr {
    title = "broken {{ .Repo "
  }
}
`
	_, err := loadHCLString(t, hcl)
	if err == nil {
		t.Fatal("expected parse error for malformed Go-template action")
	}

	if !strings.Contains(err.Error(), "defaults.pr.title") {
		t.Errorf("expected error to include location 'defaults.pr.title', got: %v", err)
	}
}

func TestLoad_PRBlock_RuleParseError_LocationIncludesRuleName(t *testing.T) {
	t.Parallel()

	hcl := `
rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  pr {
    body = "{{ .Repo "
  }
}
`
	_, err := loadHCLString(t, hcl)
	if err == nil {
		t.Fatal("expected parse error for malformed body template")
	}

	if !strings.Contains(err.Error(), `rule "codeowners".pr.body`) {
		t.Errorf("expected error to include rule-scoped location, got: %v", err)
	}
}

func TestLoad_PRBlock_ReconcilerParseError_LocationIncludesReconcilerType(t *testing.T) {
	t.Parallel()

	hcl := `
rule "file" "catalog_info" {
  paths    = ["catalog-info.yaml"]
  target   = "catalog-info.yaml"
  template = "catalog-info"

  reconcile "custom_properties" {
    mode = "api"

    pr {
      title = "{{ .Repo "
    }
  }
}
`
	_, err := loadHCLString(t, hcl)
	if err == nil {
		t.Fatal("expected parse error for malformed reconciler template")
	}

	if !strings.Contains(err.Error(), `rule "catalog_info".reconcile "custom_properties".pr.title`) {
		t.Errorf("expected error to include reconciler-scoped location, got: %v", err)
	}
}
