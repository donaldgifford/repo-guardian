package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadHCL writes content to a temp guardian.hcl and loads it, returning
// the config and error for assertion.
func loadHCL(t *testing.T, content string) (*PolicyConfig, error) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "guardian.hcl")

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing test HCL: %v", err)
	}

	return Load(path)
}

// TestLoad_AbsentAndWhen_DesignExample loads the DESIGN-0020 HCL-surface
// example (a renovate_config rule plus a no_dependabot absent rule gated
// on it) and asserts it parses and validates without error.
func TestLoad_AbsentAndWhen_DesignExample(t *testing.T) {
	content := `
rule "file" "renovate_config" {
  check    = "exists"
  paths    = ["renovate.json", ".github/renovate.json"]
  target   = "renovate.json"
  template = "renovate.tmpl"
}

rule "file" "no_dependabot" {
  check = "absent"
  paths = [".github/dependabot.yml", ".github/dependabot.yaml"]

  when {
    rule_satisfied = "renovate_config"
  }

  pr {
    search_terms = ["remove dependabot"]
    title        = "chore({{ .Repo }}): remove dependabot config (repo uses Renovate)"
  }
}
`

	cfg, err := loadHCL(t, content)
	if err != nil {
		t.Fatalf("Load() error on valid design example: %v", err)
	}

	var absent *FileRuleConfig

	for i := range cfg.FileRules {
		if cfg.FileRules[i].Name == "no_dependabot" {
			absent = &cfg.FileRules[i]
		}
	}

	if absent == nil {
		t.Fatal("no_dependabot rule not decoded")
	}

	if absent.CheckMode() != CheckAbsent {
		t.Errorf("CheckMode() = %q, want %q", absent.CheckMode(), CheckAbsent)
	}

	if absent.When == nil || absent.When.RuleSatisfied != "renovate_config" {
		t.Errorf("When = %+v, want RuleSatisfied=renovate_config", absent.When)
	}
}

// TestValidate_WhenAbsentMatrix covers every validation-matrix row from
// DESIGN-0020 as a failing fixture asserting the exact error substring.
func TestValidate_WhenAbsentMatrix(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "absent forbids template",
			content: `
rule "file" "no_dep" {
  check    = "absent"
  paths    = [".github/dependabot.yml"]
  template = "x.tmpl"
}`,
			wantErr: `check = "absent" forbids template`,
		},
		{
			name: "absent forbids target",
			content: `
rule "file" "no_dep" {
  check  = "absent"
  paths  = [".github/dependabot.yml"]
  target = ".github/dependabot.yml"
}`,
			wantErr: `check = "absent" forbids target`,
		},
		{
			name: "absent forbids assertion",
			content: `
rule "file" "no_dep" {
  check = "absent"
  paths = [".github/dependabot.yml"]
  assertion {
    pattern = "x"
    message = "m"
  }
}`,
			wantErr: `check = "absent" forbids assertion blocks`,
		},
		{
			name: "absent forbids reconcile",
			content: `
rule "file" "no_dep" {
  check = "absent"
  paths = [".github/dependabot.yml"]
  reconcile "custom_properties" {
    mode = "api"
  }
}`,
			wantErr: `check = "absent" forbids reconcile blocks`,
		},
		{
			name: "non-absent requires target",
			content: `
rule "file" "co" {
  check    = "exists"
  paths    = ["CODEOWNERS"]
  template = "codeowners.tmpl"
}`,
			wantErr: "target must be non-empty",
		},
		{
			name: "non-absent requires template",
			content: `
rule "file" "co" {
  check  = "exists"
  paths  = ["CODEOWNERS"]
  target = ".github/CODEOWNERS"
}`,
			wantErr: "template must be non-empty",
		},
		{
			name: "when references nonexistent rule",
			content: `
rule "file" "no_dep" {
  check = "absent"
  paths = [".github/dependabot.yml"]
  when {
    rule_satisfied = "nope"
  }
}`,
			wantErr: `rule_satisfied "nope" names no file rule`,
		},
		{
			name: "when references setting rule",
			content: `
rule "setting" "issues" {
  property = "has_issues"
  expected = true
}

rule "file" "no_dep" {
  check = "absent"
  paths = [".github/dependabot.yml"]
  when {
    rule_satisfied = "issues"
  }
}`,
			wantErr: `is a "setting" rule; when gates reference file rules only`,
		},
		{
			name: "when references disabled rule",
			content: `
rule "file" "renovate_config" {
  enabled  = false
  check    = "exists"
  paths    = ["renovate.json"]
  target   = "renovate.json"
  template = "renovate.tmpl"
}

rule "file" "no_dep" {
  check = "absent"
  paths = [".github/dependabot.yml"]
  when {
    rule_satisfied = "renovate_config"
  }
}`,
			wantErr: "references a disabled rule",
		},
		{
			name: "when self-reference",
			content: `
rule "file" "self" {
  check = "absent"
  paths = [".github/dependabot.yml"]
  when {
    rule_satisfied = "self"
  }
}`,
			wantErr: "cannot reference its own rule",
		},
		{
			name: "empty when block",
			content: `
rule "file" "no_dep" {
  check = "absent"
  paths = [".github/dependabot.yml"]
  when {
  }
}`,
			wantErr: "empty when {} block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadHCL(t, tt.content)
			if err == nil {
				t.Fatalf("Load() error = nil, want error containing %q", tt.wantErr)
			}

			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() error = %v\nwant substring %q", err, tt.wantErr)
			}
		})
	}
}

// TestValidate_WhenGateCycles asserts the DFS cycle detector reports
// cycles of length 2 and 3 with the a -> b -> a chain.
func TestValidate_WhenGateCycles(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name: "cycle length 2",
			content: `
rule "file" "a" {
  check = "absent"
  paths = ["a.yml"]
  when { rule_satisfied = "b" }
}

rule "file" "b" {
  check = "absent"
  paths = ["b.yml"]
  when { rule_satisfied = "a" }
}`,
		},
		{
			name: "cycle length 3",
			content: `
rule "file" "a" {
  check = "absent"
  paths = ["a.yml"]
  when { rule_satisfied = "b" }
}

rule "file" "b" {
  check = "absent"
  paths = ["b.yml"]
  when { rule_satisfied = "c" }
}

rule "file" "c" {
  check = "absent"
  paths = ["c.yml"]
  when { rule_satisfied = "a" }
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadHCL(t, tt.content)
			if err == nil {
				t.Fatal("Load() error = nil, want when gate cycle error")
			}

			if !strings.Contains(err.Error(), "when gate cycle detected") {
				t.Errorf("Load() error = %v, want a cycle diagnostic", err)
			}
		})
	}
}

// TestLoad_When_UnknownAttribute_FailsLoad asserts the strict when {}
// body schema rejects a typo'd attribute at load (INV-0010 precedent).
func TestLoad_When_UnknownAttribute_FailsLoad(t *testing.T) {
	content := `
rule "file" "no_dep" {
  check = "absent"
  paths = [".github/dependabot.yml"]
  when {
    rule_satisfed = "renovate_config"
  }
}`

	_, err := loadHCL(t, content)
	if err == nil {
		t.Fatal("Load() error = nil, want error for unknown when attribute")
	}

	if !strings.Contains(err.Error(), "rule_satisfed") &&
		!strings.Contains(err.Error(), "Unsupported argument") {
		t.Errorf("Load() error = %v, want an unsupported-argument diagnostic", err)
	}
}

// TestLoad_When_NullRuleSatisfied_CleanDiagnostic is the INV-0011 A8
// regression guard: a typed-null or wrong-typed rule_satisfied value
// must return a clean load diagnostic rather than panicking in
// decodeWhenBlock's AsString().
func TestLoad_When_NullRuleSatisfied_CleanDiagnostic(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		wantErr string
	}{
		{
			name:    "explicit null",
			expr:    "null",
			wantErr: "rule_satisfied must be a non-null string",
		},
		{
			name:    "conditional yielding typed-null",
			expr:    `false ? "renovate_config" : null`,
			wantErr: "rule_satisfied must be a non-null string",
		},
		{
			name:    "wrong type",
			expr:    "true",
			wantErr: "rule_satisfied must be a non-null string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := `
rule "file" "no_dep" {
  check = "absent"
  paths = [".github/dependabot.yml"]
  when {
    rule_satisfied = ` + tt.expr + `
  }
}`

			// Must return an error, never panic.
			_, err := loadHCL(t, content)
			if err == nil {
				t.Fatalf("Load() error = nil, want %q", tt.wantErr)
			}

			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}
