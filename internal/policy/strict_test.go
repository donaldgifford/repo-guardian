package policy_test

import (
	"strings"
	"testing"

	"github.com/donaldgifford/repo-guardian/internal/policy"
)

func TestValidatePRTemplates_AcceptsSafeTemplates(t *testing.T) {
	t.Parallel()

	hcl := `
defaults {
  pr {
    title = "chore: guardian update for {{ .Repo }}"
    body  = "Owner: {{ .Owner }}"
  }
}
`
	cfg := mustLoadHCL(t, hcl)

	if err := policy.ValidatePRTemplates(cfg); err != nil {
		t.Errorf("safe templates should pass strict validation, got: %v", err)
	}
}

func TestValidatePRTemplates_FlagsRulePRWithNilField(t *testing.T) {
	t.Parallel()

	// .Catalog is a FileVars field; it does NOT exist on PRVars.
	// missingkey=error trips on this when rendered against zero PRVars.
	hcl := `
rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  pr {
    title = "{{ .Catalog.Owner }}"
  }
}
`
	cfg := mustLoadHCL(t, hcl)

	err := policy.ValidatePRTemplates(cfg)
	if err == nil {
		t.Fatal("expected strict validation to flag .Catalog.Owner reference on PRVars-context template")
	}

	if !strings.Contains(err.Error(), `rule "codeowners".pr.title`) {
		t.Errorf("expected error to include rule scope; got: %v", err)
	}
}

func TestValidatePRTemplates_NoTemplates_PassesTrivially(t *testing.T) {
	t.Parallel()

	cfg := &policy.PolicyConfig{}

	if err := policy.ValidatePRTemplates(cfg); err != nil {
		t.Errorf("empty config should pass strict validation, got: %v", err)
	}
}

func TestValidatePRTemplates_AggregatesMultipleFailures(t *testing.T) {
	t.Parallel()

	hcl := `
defaults {
  pr {
    title = "{{ .Catalog.Owner }}"
  }
}

rule "file" "codeowners" {
  paths    = ["CODEOWNERS"]
  target   = ".github/CODEOWNERS"
  template = "codeowners"

  pr {
    body = "{{ .Catalog.Component }}"
  }
}
`
	cfg := mustLoadHCL(t, hcl)

	err := policy.ValidatePRTemplates(cfg)
	if err == nil {
		t.Fatal("expected strict validation to flag both failures")
	}

	if !strings.Contains(err.Error(), "defaults.pr.title") {
		t.Errorf("expected defaults.pr.title in error: %v", err)
	}

	if !strings.Contains(err.Error(), `rule "codeowners".pr.body`) {
		t.Errorf("expected rule.pr.body in error: %v", err)
	}
}
